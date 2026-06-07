// Package mock provides a configurable test double for [cache.Cache], intended
// for import by other projects' tests.
package mock

import (
	"context"
	"iter"
	"sync"
	"time"

	"github.com/remnestal/albstractions/cache"
)

// Call records a single invocation of a [Backend] method.
//
// Val and Exp are set only for Set calls; they are zero for other operations.
type Call[K comparable, V any] struct {
	Op  string
	Key K
	Val V
	Exp cache.Expiry
}

// Backend is a configurable [cache.Cache] test double.
//
// Set the exported function fields to control behaviour. An unset function
// behaves as an empty cache: reads miss, writes and deletes succeed without
// storing anything, and iteration yields nothing. Every call is recorded and
// retrievable with [Backend.Calls].
//
// Backend is safe for concurrent use up to the behaviour of the configured
// functions.
type Backend[K comparable, V any] struct {
	GetFunc          func(ctx context.Context, key K) (V, bool, error)
	SetFunc          func(ctx context.Context, key K, val V, exp cache.Expiry) (time.Time, error)
	DeleteFunc       func(ctx context.Context, key K) error
	ItemsContextFunc func(ctx context.Context) (iter.Seq2[K, V], func() error)

	mu    sync.Mutex
	calls []Call[K, V]
}

var _ cache.Cache[int, int] = (*Backend[int, int])(nil)

// Get implements [cache.Cache.Get], treating an error from GetFunc as a miss.
func (b *Backend[K, V]) Get(key K) (V, bool) {
	v, ok, err := b.GetContext(context.Background(), key)
	if err != nil {
		var zero V
		return zero, false
	}
	return v, ok
}

// Set implements [cache.Cache.Set].
func (b *Backend[K, V]) Set(key K, val V, exp cache.Expiry) time.Time {
	t, _ := b.SetContext(context.Background(), key, val, exp)
	return t
}

// Delete implements [cache.Cache.Delete].
func (b *Backend[K, V]) Delete(key K) {
	_ = b.DeleteContext(context.Background(), key)
}

// GetContext implements [cache.Cache.GetContext], delegating to GetFunc.
func (b *Backend[K, V]) GetContext(ctx context.Context, key K) (V, bool, error) {
	b.record(Call[K, V]{Op: "Get", Key: key})
	if b.GetFunc != nil {
		return b.GetFunc(ctx, key)
	}
	var zero V
	return zero, false, nil
}

// SetContext implements [cache.Cache.SetContext], delegating to SetFunc.
func (b *Backend[K, V]) SetContext(ctx context.Context, key K, val V, exp cache.Expiry) (time.Time, error) {
	b.record(Call[K, V]{Op: "Set", Key: key, Val: val, Exp: exp})
	if b.SetFunc != nil {
		return b.SetFunc(ctx, key, val, exp)
	}
	return time.Time{}, nil
}

// DeleteContext implements [cache.Cache.DeleteContext], delegating to DeleteFunc.
func (b *Backend[K, V]) DeleteContext(ctx context.Context, key K) error {
	b.record(Call[K, V]{Op: "Delete", Key: key})
	if b.DeleteFunc != nil {
		return b.DeleteFunc(ctx, key)
	}
	return nil
}

// Items implements [cache.Cache.Items].
func (b *Backend[K, V]) Items() iter.Seq2[K, V] {
	seq, _ := b.ItemsContext(context.Background())
	return seq
}

// ItemsContext implements [cache.Cache.ItemsContext], delegating to
// ItemsContextFunc.
func (b *Backend[K, V]) ItemsContext(ctx context.Context) (iter.Seq2[K, V], func() error) {
	b.record(Call[K, V]{Op: "Items"})
	if b.ItemsContextFunc != nil {
		return b.ItemsContextFunc(ctx)
	}
	return func(func(K, V) bool) {}, func() error { return nil }
}

// Calls returns a copy of the recorded calls in invocation order.
func (b *Backend[K, V]) Calls() []Call[K, V] {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Call[K, V](nil), b.calls...)
}

func (b *Backend[K, V]) record(c Call[K, V]) {
	b.mu.Lock()
	b.calls = append(b.calls, c)
	b.mu.Unlock()
}
