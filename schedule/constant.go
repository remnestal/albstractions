package schedule

import "time"

// Constant returns the same delay on every call.
type Constant struct {
	delay time.Duration
}

// NewConstant returns a Schedule that always waits d between events.
// Panics if d < 0.
func NewConstant(d time.Duration) *Constant {
	if d < 0 {
		panic("schedule.NewConstant: d must be >= 0")
	}
	return &Constant{delay: d}
}

// Next returns the fixed delay.
func (c *Constant) Next() time.Duration {
	return c.delay
}
