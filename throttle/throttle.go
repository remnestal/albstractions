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

// Do waits until the next allowed dispatch time, then runs fn.
//
// Consecutive Do calls on the same Throttle are spaced by at least s.Next();
// the first call is not delayed. If ctx is cancelled during the wait, Do
// returns ctx.Err() without calling fn; the internal cadence still advances
// so a later caller cannot burst.
func (t *Throttle) Do(ctx context.Context, fn func(context.Context) error) error {
	wait := t.nextSlot(t.schedule.Next())
	if err := sleep(ctx, wait); err != nil {
		return err
	}
	return t.doConcurrent(ctx, fn)
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

// doConcurrent acquires the semaphore (if set), calls fn, then releases the slot.
func (t *Throttle) doConcurrent(ctx context.Context, fn func(context.Context) error) error {
	if t.sem != nil {
		select {
		case t.sem <- struct{}{}:
			defer func() { <-t.sem }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fn(ctx)
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
