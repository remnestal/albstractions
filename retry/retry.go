// Package retry re-invokes a fallible function until it succeeds, with
// between-attempt spacing supplied by a Schedule and an explicit stop
// condition supplied by a StopFunc.
//
// Create a Retry with New, then call Do to run a function with retries.
// Use [MaxAttempts], [MaxElapsed], [OnError], [Any], and [All] to assemble
// the stop condition.
package retry

import (
	"context"
	"time"
)

// Schedule returns the duration to wait between attempts.
//
// Any type satisfying this interface (e.g. types from
// github.com/remnestal/albstractions/schedule) can be used.
type Schedule interface {
	Next() time.Duration
}

// Resetter is the optional capability for stateful Schedules.
//
// If a Schedule also implements Resetter, [Retry.Do] calls Reset at the
// start of every invocation so a single *Retry is safe to reuse across
// many independent Do calls.
type Resetter interface {
	Reset()
}

// AttemptInfo is delivered to the observation hook installed via
// [WithHook].
type AttemptInfo struct {
	// Attempt is the 1-based attempt counter.
	Attempt int
	// Elapsed is the time since [Retry.Do] started.
	Elapsed time.Duration
	// Err is the error returned by this attempt, or nil on success.
	Err error
	// Next is the upcoming sleep before the next attempt. It is zero when
	// Do is about to return (success or stop condition reached).
	Next time.Duration
}

// Retry executes a function until success or until a StopFunc halts.
//
// Retry holds no mutable state of its own. It is safe to call [Retry.Do]
// concurrently provided the supplied [Schedule] is goroutine-safe; note
// that concurrent calls share the Schedule's state.
type Retry struct {
	schedule  Schedule
	stop      StopFunc
	onAttempt func(AttemptInfo)
}

// Option configures a Retry.
type Option func(*Retry)

// WithHook installs an observation hook fired exactly once per attempt,
// after fn returns. It is invoked for the successful attempt (Err == nil)
// as well as failed ones, allowing callers to log "succeeded after N tries".
//
// The hook is called on the goroutine driving [Retry.Do] and must not block.
// It is purely observational — it cannot influence retry behaviour.
//
// Panics if fn is nil.
func WithHook(fn func(AttemptInfo)) Option {
	if fn == nil {
		panic("retry.WithHook: fn must not be nil")
	}
	return func(r *Retry) { r.onAttempt = fn }
}

// New returns a [Retry] paced by s and bounded by stop.
//
// Panics if s or stop is nil.
func New(s Schedule, stop StopFunc, opts ...Option) *Retry {
	if s == nil {
		panic("retry.New: schedule must not be nil")
	}
	if stop == nil {
		panic("retry.New: stop must not be nil")
	}
	r := &Retry{
		schedule: s,
		stop:     stop,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Do calls fn repeatedly until it returns nil, the StopFunc halts, or ctx
// is done.
//
// On success Do returns nil. On halt Do returns the error from the final
// attempt. On context cancellation during the inter-attempt sleep Do
// returns ctx.Err(); cancellation during fn surfaces as whatever fn
// returns. If the supplied Schedule satisfies [Resetter], Reset is
// called once at the start of Do.
func (r *Retry) Do(ctx context.Context, fn func(context.Context) error) error {
	if resetter, ok := r.schedule.(Resetter); ok {
		resetter.Reset()
	}

	start := time.Now()
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		elapsed := time.Since(start)

		if err == nil {
			r.fire(AttemptInfo{Attempt: attempt, Elapsed: elapsed})
			return nil
		}

		if r.stop(attempt, elapsed, err) {
			r.fire(AttemptInfo{Attempt: attempt, Elapsed: elapsed, Err: err})
			return err
		}

		next := r.schedule.Next()
		r.fire(AttemptInfo{Attempt: attempt, Elapsed: elapsed, Err: err, Next: next})

		if err := sleep(ctx, next); err != nil {
			return err
		}
	}
}

// fire delivers info to the observation hook if one is installed.
func (r *Retry) fire(info AttemptInfo) {
	if r.onAttempt != nil {
		r.onAttempt(info)
	}
}

// sleep waits for d, returning ctx.Err() if the context is cancelled first.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
