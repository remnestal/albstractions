package schedule

import (
	"math"
	"sync"
	"time"
)

// Exponential implements an exponential backoff schedule.
//
// Each call to [Exponential.Next] multiplies the current delay by the
// configured factor until it reaches maxDelay. The delay is stateful — it
// grows across successive calls. Call [Exponential.Reset] to start a new
// backoff sequence.
type Exponential struct {
	base     time.Duration
	maxDelay time.Duration
	factor   float64

	mu      sync.Mutex
	current time.Duration
}

// ExponentialOption configures an Exponential schedule.
type ExponentialOption func(*Exponential)

// WithFactor sets the growth multiplier applied on each call to Next.
//
// The default is 2.0.
func WithFactor(f float64) ExponentialOption {
	return func(e *Exponential) { e.factor = f }
}

// NewExponential returns an [Exponential] schedule starting at base and capped
// at maxDelay.
//
// Panics if base <= 0 or maxDelay < base.
func NewExponential(base, maxDelay time.Duration, opts ...ExponentialOption) *Exponential {
	if base <= 0 {
		panic("schedule.NewExponential: base must be positive")
	}
	if maxDelay < base {
		panic("schedule.NewExponential: maxDelay must be >= base")
	}
	e := &Exponential{
		base:     base,
		maxDelay: maxDelay,
		factor:   2.0,
		current:  base,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Next returns the current delay and advances the schedule.
func (e *Exponential) Next() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()

	d := e.current
	e.current = time.Duration(math.Min(float64(e.maxDelay), float64(e.current)*e.factor))
	return d
}

// Reset restores the delay to the base value.
//
// Call this before starting a new backoff sequence.
func (e *Exponential) Reset() {
	e.mu.Lock()
	e.current = e.base
	e.mu.Unlock()
}
