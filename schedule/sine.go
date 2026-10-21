package schedule

import (
	"math"
	"sync"
	"time"
)

// Sine oscillates the wait duration between lo and hi following a raised cosine
// curve over the given period. The delay is lo at the start of each period,
// rises to hi at the midpoint, and returns to lo at the end — giving a smooth
// ease-in / ease-out cadence.
//
// The phase is the time elapsed since the first call to [Sine.Next], not the
// call count. Two calls made at the same instant return the same delay, and the
// oscillation continues independently of how often Next is called.
//
// Each Sine starts its own cycle on its first Next, so two instances first
// called at different times are out of phase. The default clock measures that
// elapsed time monotonically, so a wall-clock jump does not shift the phase.
type Sine struct {
	lo      time.Duration
	hi      time.Duration
	period  time.Duration
	clockFn func() time.Time

	mu    sync.Mutex
	start time.Time
}

// SineOption configures a Sine schedule.
type SineOption func(*Sine)

// WithClock overrides the clock function used to measure elapsed time.
//
// Useful for deterministic tests.
//
// Panics if fn is nil.
func WithClock(fn func() time.Time) SineOption {
	if fn == nil {
		panic("schedule.WithClock: fn must not be nil")
	}
	return func(s *Sine) { s.clockFn = fn }
}

// NewSine returns a [Schedule] whose delays oscillate between lo and hi over
// the given period.
//
// Panics if lo < 0, hi < lo, or period <= 0.
func NewSine(lo, hi, period time.Duration, opts ...SineOption) *Sine {
	if lo < 0 {
		panic("schedule.NewSine: lo must be >= 0")
	}
	if hi < lo {
		panic("schedule.NewSine: hi must be >= lo")
	}
	if period <= 0 {
		panic("schedule.NewSine: period must be positive")
	}
	s := &Sine{
		lo:      lo,
		hi:      hi,
		period:  period,
		clockFn: time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Next returns the delay for the current phase of the cosine cycle.
func (s *Sine) Next() time.Duration {
	s.mu.Lock()
	if s.start.IsZero() {
		s.start = s.clockFn()
	}
	elapsed := s.clockFn().Sub(s.start)
	s.mu.Unlock()

	phase := float64(elapsed%s.period) / float64(s.period) * 2 * math.Pi
	// (1-cos(phase))/2 maps [0,2π]→[0,1]: 0 at phase 0, 1 at phase π, 0 at phase 2π.
	scale := (1 - math.Cos(phase)) / 2
	span := s.hi - s.lo
	offset := float64(span) * scale
	if offset >= float64(span) { // also guards the float-to-int overflow near math.MaxInt64
		return s.hi
	}
	return s.lo + time.Duration(offset)
}
