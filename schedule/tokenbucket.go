package schedule

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// Reservation is a claim on capacity held by a rate limiter.
//
// It is satisfied by *golang.org/x/time/rate.Reservation.
type Reservation interface {
	// OK reports whether the capacity can be granted. A false result means the
	// reservation was refused and no capacity will become available.
	OK() bool
	// Delay returns how long to wait until the reserved capacity is available.
	Delay() time.Duration
}

// Limiter hands out reservations against a rate limit.
//
// It is satisfied by *golang.org/x/time/rate.Limiter, so a limiter can be
// passed straight to [NewTokenBucket] without this module depending on
// golang.org/x/time.
//
// The reservation is a type parameter because Reserve returns a concrete type.
// A plain Reserve() [Reservation] method would match no real limiter, since Go
// does not treat a concrete return type as satisfying an interface one.
type Limiter[R Reservation] interface {
	Reserve() R
}

// TokenBucket adapts a [Limiter] to the [Schedule] interface. Each call to
// [TokenBucket.Next] reserves capacity and returns the time to wait until it
// is available.
//
// Note: reservations are consumed on every Next call regardless of whether the
// caller actually waits. Rapid successive calls will accumulate growing delays
// as the bucket drains.
//
// A limiter that refuses reservations fails closed: [TokenBucket.Next] reports
// an infinite delay rather than letting callers run unthrottled.
type TokenBucket[R Reservation] struct {
	limiter Limiter[R]
}

// NewTokenBucket returns a [Schedule] backed by the given limiter.
//
// Panics if limiter is nil. A typed nil pointer, such as a nil *rate.Limiter,
// is a non-nil interface and is not detected; it panics on the first call to
// [TokenBucket.Next] instead.
func NewTokenBucket[R Reservation](limiter Limiter[R]) *TokenBucket[R] {
	if limiter == nil {
		panic("schedule.NewTokenBucket: limiter must not be nil")
	}
	return &TokenBucket[R]{limiter: limiter}
}

// Next reserves capacity and returns the delay until it is available.
//
// If the limiter refuses the reservation (for example a
// golang.org/x/time/rate limiter with burst 0), Next returns math.MaxInt64,
// the same as rate.InfDuration, so callers wait until their context ends
// rather than running unthrottled.
func (t *TokenBucket[R]) Next() time.Duration {
	r := t.limiter.Reserve()
	if !r.OK() {
		// The limiter will never grant this reservation. Report an infinite
		// delay, as rate.InfDuration does, rather than letting the caller run
		// unthrottled. A constant is used because Reservation does not define
		// Delay for a refused reservation.
		return math.MaxInt64
	}
	return r.Delay()
}

// ErrRefused reports that a [TokenBucket]'s limiter will never grant a
// reservation, for example a golang.org/x/time/rate limiter with burst 0.
var ErrRefused = errors.New("schedule: limiter refused the reservation")

// Wait blocks until the bucket grants one unit of capacity, or ctx is done.
//
// When the limiter has its own Wait(context.Context) error method, as
// *rate.Limiter does, Wait delegates to it: a caller cancelled while waiting
// hands its token back to reservations made after the cancellation, and a
// deadline the limiter cannot meet fails at once. Otherwise Wait reserves,
// sleeps for the reservation's delay, and cancels the reservation, if it has a
// Cancel method, when ctx ends first.
//
// Wait fails closed. It returns ctx's error if ctx is done first, an error
// matching context.DeadlineExceeded if the limiter reports that the deadline
// cannot be met, and an error matching [ErrRefused] if the limiter will never
// grant capacity.
func (t *TokenBucket[R]) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w, ok := t.limiter.(interface{ Wait(context.Context) error }); ok {
		return waitErr(ctx, t.limiter, w.Wait(ctx))
	}
	r := t.limiter.Reserve()
	if !r.OK() {
		return ErrRefused
	}
	d := r.Delay()
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		if c, ok := any(r).(interface{ Cancel() }); ok {
			c.Cancel()
		}
		return ctx.Err()
	}
}

// waitErr classifies an error from a limiter's own Wait. Limiters such as
// *rate.Limiter report refusals as opaque strings, so a burst of 0 (the only
// permanent refusal) is told apart from an unmeetable deadline by asking the
// limiter for its burst.
func waitErr(ctx context.Context, limiter any, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if b, ok := limiter.(interface{ Burst() int }); ok && b.Burst() == 0 {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if _, ok := ctx.Deadline(); ok {
		return fmt.Errorf("schedule: %w: %w", context.DeadlineExceeded, err)
	}
	return fmt.Errorf("%w: %w", ErrRefused, err)
}
