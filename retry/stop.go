package retry

import (
	"strconv"
	"time"
)

// StopFunc decides whether [Retry.Do] should give up.
//
// It is consulted after every failed attempt with the 1-based attempt count,
// the elapsed time since Do started, and the error returned by that attempt.
// Returning true halts the loop; Do then returns err. The first attempt
// always runs — StopFunc is not consulted before it.
//
// StopFuncs are pure and may be composed with [Any] and [All].
type StopFunc func(attempt int, elapsed time.Duration, err error) bool

// MaxAttempts stops after n failed attempts.
//
// Panics if n <= 0.
func MaxAttempts(n int) StopFunc {
	if n <= 0 {
		panic("retry.MaxAttempts: n must be > 0")
	}
	return func(attempt int, _ time.Duration, _ error) bool {
		return attempt >= n
	}
}

// MaxElapsed stops once the cumulative elapsed time since [Retry.Do] started
// reaches d.
//
// Panics if d <= 0.
func MaxElapsed(d time.Duration) StopFunc {
	if d <= 0 {
		panic("retry.MaxElapsed: d must be > 0")
	}
	return func(_ int, elapsed time.Duration, _ error) bool {
		return elapsed >= d
	}
}

// OnError stops when pred(err) returns true.
//
// It is the way to short-circuit on errors that are not worth retrying, such
// as authentication or validation failures.
//
// Panics if pred is nil.
func OnError(pred func(error) bool) StopFunc {
	if pred == nil {
		panic("retry.OnError: pred must not be nil")
	}
	return func(_ int, _ time.Duration, err error) bool {
		return pred(err)
	}
}

// Any returns a StopFunc that halts if any of stops halts (logical OR).
//
// Use this to combine independent budgets — for example, "stop after 5
// attempts OR after 30 seconds, whichever comes first".
//
// Panics if stops is empty or contains a nil entry.
func Any(stops ...StopFunc) StopFunc {
	if len(stops) == 0 {
		panic("retry.Any: at least one StopFunc is required")
	}
	for i, s := range stops {
		if s == nil {
			panic("retry.Any: stops[" + strconv.Itoa(i) + "] is nil")
		}
	}
	return func(attempt int, elapsed time.Duration, err error) bool {
		for _, s := range stops {
			if s(attempt, elapsed, err) {
				return true
			}
		}
		return false
	}
}

// All returns a StopFunc that halts only if all of stops halt (logical AND).
//
// Panics if stops is empty or contains a nil entry.
func All(stops ...StopFunc) StopFunc {
	if len(stops) == 0 {
		panic("retry.All: at least one StopFunc is required")
	}
	for i, s := range stops {
		if s == nil {
			panic("retry.All: stops[" + strconv.Itoa(i) + "] is nil")
		}
	}
	return func(attempt int, elapsed time.Duration, err error) bool {
		for _, s := range stops {
			if !s(attempt, elapsed, err) {
				return false
			}
		}
		return true
	}
}
