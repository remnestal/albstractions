package schedule

import (
	"math/rand/v2"
	"sync"
	"time"
)

// Uniform returns a uniformly distributed random delay in [lo, hi] on every call.
type Uniform struct {
	lo  time.Duration
	hi  time.Duration
	mu  sync.Mutex
	rng *rand.Rand
}

// UniformOption configures a Uniform schedule.
type UniformOption func(*Uniform)

// WithSource sets the random source used to generate delays.
//
// Useful for deterministic tests. If not set, a random seed is used.
//
// Panics if src is nil. A typed nil pointer, such as a nil *rand.PCG, is a
// non-nil interface and is not detected.
func WithSource(src rand.Source) UniformOption {
	if src == nil {
		panic("schedule.WithSource: src must not be nil")
	}
	return func(u *Uniform) { u.rng = rand.New(src) }
}

// NewUniform returns a [Schedule] that picks a random delay in [lo, hi] on
// each call.
//
// Panics if lo < 0 or hi < lo.
func NewUniform(lo, hi time.Duration, opts ...UniformOption) *Uniform {
	if lo < 0 {
		panic("schedule.NewUniform: lo must be >= 0")
	}
	if hi < lo {
		panic("schedule.NewUniform: hi must be >= lo")
	}
	u := &Uniform{
		lo:  lo,
		hi:  hi,
		rng: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
	}
	for _, opt := range opts {
		opt(u)
	}
	return u
}

// Next returns a uniformly distributed random duration in [lo, hi].
func (u *Uniform) Next() time.Duration {
	u.mu.Lock()
	// Draw in uint64: when the range spans every Duration, hi-lo+1 is 2^63,
	// which does not fit in an int64.
	n := u.rng.Uint64N(uint64(u.hi-u.lo) + 1)
	u.mu.Unlock()
	return u.lo + time.Duration(n)
}
