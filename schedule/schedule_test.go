package schedule_test

import (
	"sync"
	"testing"
	"time"

	"github.com/remnestal/albstractions/schedule"
)

// lockedLimiter serialises a fakeLimiter, which is not goroutine-safe, the way
// a real limiter such as *rate.Limiter is.
type lockedLimiter struct {
	mu sync.Mutex
	l  fakeLimiter
}

func (l *lockedLimiter) Reserve() fakeReservation {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.l.Reserve()
}

func TestSchedule(t *testing.T) {
	t.Parallel()

	// Each case is a fresh schedule that has never been called, so the first
	// calls race too (Sine sets its start time on the first Next).
	tests := []struct {
		name string
		new  func() schedule.Schedule
	}{
		{"Constant", func() schedule.Schedule { return schedule.NewConstant(time.Second) }},
		{"Exponential", func() schedule.Schedule { return schedule.NewExponential(time.Millisecond, time.Second) }},
		{"Uniform", func() schedule.Schedule { return schedule.NewUniform(time.Second, 5*time.Second) }},
		{"Sine", func() schedule.Schedule { return schedule.NewSine(time.Second, 5*time.Second, time.Minute) }},
		// TokenBucket holds no state of its own; its safety is the limiter's.
		{"TokenBucket", func() schedule.Schedule {
			return schedule.NewTokenBucket(&lockedLimiter{l: fakeLimiter{burst: 5, interval: time.Second}})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" is safe for concurrent use", func(t *testing.T) {
			t.Parallel()

			// The race detector is the oracle here, so this only proves
			// anything under go test -race. Schedules with a Reset call it
			// concurrently with Next as well.
			s := tt.new()
			start := make(chan struct{})
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					<-start
					for range 100 {
						s.Next()
						if r, ok := s.(interface{ Reset() }); ok {
							r.Reset()
						}
					}
				})
			}
			close(start)
			wg.Wait()
		})
	}
}
