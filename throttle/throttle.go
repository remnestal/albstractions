// Package throttle paces function and HTTP call cadence using a Schedule.
//
// Create a Throttle with New, then call Do to run a function at the throttled
// rate. By default a Throttle runs invocations serially; use WithMaxInflight
// to allow more concurrency.
package throttle

import (
	"context"
	"sync"
	"time"
)

// Schedule returns the duration to wait before the next event.
//
// Any type satisfying this interface (e.g. types from
// github.com/remnestal/albstractions/schedule) can be used.
type Schedule interface {
	Next() time.Duration
}

// Waiter is the optional capability for schedules that decide per caller when
// it may proceed, such as a token bucket that reserves one token per call.
//
// When the [Schedule] passed to [New] implements Waiter, [Throttle.Do] calls
// Wait instead of spacing calls by Next, and returns Wait's error without
// calling fn. Next is never called for such a schedule.
type Waiter interface {
	Wait(ctx context.Context) error
}

// Throttle paces calls according to a [Schedule].
//
// By default a Throttle runs fn invocations serially (one at a time). Use
// [WithMaxInflight] to allow more concurrency, or WithMaxInflight([Unbounded])
// to remove the limit entirely.
//
// Throttle is safe for concurrent use.
type Throttle struct {
	schedule    Schedule
	mu          sync.Mutex
	nextAllowed time.Time
	sem         chan struct{}
}

// Option configures a Throttle.
type Option func(*Throttle)

// Unbounded, passed to [WithMaxInflight], disables the in-flight cap.
const Unbounded = -1

// WithMaxInflight caps the number of fn invocations in flight on a Throttle
// at n.
//
// Callers beyond the cap block (honouring ctx) until a slot frees. Pass
// [Unbounded] for no limit.
//
// A caller holds its slot while it waits for its dispatch time, so the cap
// counts paced waiters as well as running calls. This does not lower the start
// rate, which the schedule sets, but it bounds how far ahead the cadence can be
// reserved.
//
// Panics if n == 0 or n < -1. Default: 1 (serial).
func WithMaxInflight(n int) Option {
	if n == 0 || n < -1 {
		panic("throttle.WithMaxInflight: n must be > 0 or Unbounded (-1)")
	}
	return func(t *Throttle) {
		if n == Unbounded {
			t.sem = nil
		} else {
			t.sem = make(chan struct{}, n)
		}
	}
}

// New returns a [Throttle] paced by s.
//
// By default invocations are serial; pass [WithMaxInflight] to change this.
func New(s Schedule, opts ...Option) *Throttle {
	t := &Throttle{
		schedule: s,
		sem:      make(chan struct{}, 1),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Do waits for an in-flight slot, then for its dispatch time, then runs fn.
//
// Consecutive Do calls on the same Throttle start at least s.Next() apart,
// even when callers queued behind a slow fn; the first call is not delayed.
// When the schedule is a [Waiter], it decides instead when each caller may
// proceed, and may delay or refuse even the first call.
//
// If ctx is done before fn runs, Do returns ctx.Err() without calling fn. A
// caller cancelled during the paced wait has already taken its turn, so the
// cadence still advances and a later caller cannot burst. A caller cancelled
// before Do or while queued for an in-flight slot takes no turn and leaves the
// cadence untouched. With a [Waiter], what a cancelled caller gives back is up
// to the waiter; schedule.TokenBucket returns its token to later reservations.
func (t *Throttle) Do(ctx context.Context, fn func(context.Context) error) error {
	// Take the in-flight slot before the pacing slot, so a caller that queued
	// behind a slow fn still waits a full delay after the previous start.
	if t.sem != nil {
		select {
		case t.sem <- struct{}{}:
			defer func() { <-t.sem }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// The select above picks at random when both cases are ready; a cancelled
	// caller must not take a turn either way.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.pace(ctx); err != nil {
		return err
	}
	return fn(ctx)
}

// pace blocks until the caller's dispatch time, advancing the cadence, or
// defers to the schedule when it implements [Waiter].
func (t *Throttle) pace(ctx context.Context) error {
	if w, ok := t.schedule.(Waiter); ok {
		return w.Wait(ctx)
	}
	return sleep(ctx, t.nextSlot(t.schedule.Next()))
}

// nextSlot advances the schedule by delay and returns how long the caller must
// wait before dispatching. Resets debt if the throttle has been idle.
func (t *Throttle) nextSlot(delay time.Duration) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if t.nextAllowed.Before(now) {
		t.nextAllowed = now
	}
	wait := t.nextAllowed.Sub(now)
	t.nextAllowed = t.nextAllowed.Add(delay)
	return wait
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
