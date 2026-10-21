package schedule

import "time"

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
func (t *TokenBucket[R]) Next() time.Duration {
	r := t.limiter.Reserve()
	if !r.OK() {
		// burst is 0 or rate is 0 — return zero so callers are not blocked forever.
		return 0
	}
	return r.Delay()
}
