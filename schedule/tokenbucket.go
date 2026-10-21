package schedule

import (
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
