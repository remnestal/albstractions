// Package schedule provides configurable delay strategies for throttling,
// rate-limiting, and backoff. Implementations are stateful and goroutine-safe.
package schedule

import "time"

// Schedule returns the duration to wait before the next event.
// Implementations must be goroutine-safe.
type Schedule interface {
	Next() time.Duration
}
