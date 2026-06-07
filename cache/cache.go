// Package cache provides a generic in-memory cache behind a small interface
// that external backends such as Redis or a database can satisfy, together
// with composable sharding, read-through, and write-through layers.
//
// [Memory] is the headline implementation. Wrap any [Cache] with [NewSharded],
// [NewReadThrough], or [NewWriteThrough] to build multi-tier stacks; every
// layer is itself a [Cache], so composition nests freely.
package cache

import (
	"context"
	"errors"
	"iter"
	"time"
)

// Cache stores values of type V keyed by K, with a per-entry time-to-live.
//
// Every operation has two forms: an ergonomic infallible form ([Cache.Get],
// [Cache.Set], [Cache.Delete], [Cache.Items]) and a context form for backends
// that perform I/O ([Cache.GetContext], [Cache.SetContext],
// [Cache.DeleteContext]). The context methods are authoritative; the infallible
// methods call them with a background context and treat any error as a miss on
// reads or ignore it on writes.
//
// Implementations must be safe for concurrent use.
type Cache[K comparable, V any] interface {
	// Get returns the live value for key, or ok == false if it is absent,
	// expired, or the backend errored.
	Get(key K) (V, bool)

	// Set stores val for key with the given [Expiry] and returns the resulting
	// absolute expiry, or the zero time if the entry never expires.
	//
	// exp is an [After] or [At] value, or one of [Never], [Default], or [Keep];
	// see [Expiry] for how each resolves against the configured bounds.
	Set(key K, val V, exp Expiry) time.Time

	// Delete removes key, if present.
	Delete(key K)

	// Items returns an iterator over the cache's live entries.
	//
	// Iteration is a point-in-time snapshot: entries inserted or removed after
	// the call may or may not be observed, and the loop body may call back into
	// the cache. A backend that cannot enumerate yields nothing.
	Items() iter.Seq2[K, V]

	// GetContext returns the live value for key and whether it was present. A
	// non-nil error reports a backend failure, which is distinct from a plain
	// miss (false, nil).
	GetContext(ctx context.Context, key K) (V, bool, error)

	// SetContext stores val for key with the given [Expiry] and returns the
	// resulting expiry.
	//
	// See [Cache.Set] for the meaning of exp and the returned time.
	SetContext(ctx context.Context, key K, val V, exp Expiry) (time.Time, error)

	// DeleteContext removes key, if present.
	DeleteContext(ctx context.Context, key K) error
}

// ErrCacheMiss reports that a key was absent from a backing source.
//
// [LoaderFromCache] returns it on a miss, and [NewReadThrough] recognises it as
// a miss by default so that cache tiers compose. A loader over another source
// should return it (or an error wrapping it) to signal genuine absence rather
// than a transient failure.
var ErrCacheMiss = errors.New("cache: miss")

// miss collapses a context-read result into the infallible (value, ok) form,
// reporting any error as a miss.
func miss[V any](value V, ok bool, err error) (V, bool) {
	if err != nil {
		var zero V
		return zero, false
	}
	return value, ok
}
