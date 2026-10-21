package cache

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFlightGroup_Do(t *testing.T) {
	t.Parallel()

	t.Run("waiters receive an error when the leader's loader calls Goexit", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			g := &flightGroup[string, int]{m: make(map[string]*call[int])}
			var calls atomic.Int64
			fn := func() (int, error) {
				calls.Add(1)
				time.Sleep(time.Second)
				runtime.Goexit()
				return 42, nil
			}

			var wg sync.WaitGroup
			leaderReturned := false
			wg.Go(func() {
				_, _ = g.Do(t.Context(), "k", fn)
				leaderReturned = true
			})
			synctest.Wait() // the leader is inside fn

			var (
				v   int
				err error
			)
			wg.Go(func() { v, err = g.Do(t.Context(), "k", fn) })
			synctest.Wait() // the waiter has joined the flight
			wg.Wait()

			assert.False(t, leaderReturned, "Goexit unwinds the leader without returning")
			assert.ErrorContains(t, err, "runtime.Goexit", "a waiter must not see a spurious nil-error result")
			assert.Zero(t, v)
			assert.Equal(t, int64(1), calls.Load(), "the waiter shares the leader's flight")
		})
	})
}
