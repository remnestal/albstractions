package cache

import (
	"context"
	"io"
	"iter"
	"time"
)

// Writer persists a key/value pair to a backing store.
type Writer[K comparable, V any] func(ctx context.Context, key K, val V) error

// Deleter removes a key from a backing store.
type Deleter[K comparable, V any] func(ctx context.Context, key K) error

// WriteThroughOption configures a [WriteThrough].
type WriteThroughOption[K comparable, V any] func(*writeThroughConfig[K, V])

type writeThroughConfig[K comparable, V any] struct {
	deleter Deleter[K, V]
}

// WithDeleter makes [WriteThrough.Delete] remove the key from the backing store
// before removing it from the front cache.
//
// Without it, deletes affect the front cache only.
//
// Panics if del is nil.
func WithDeleter[K comparable, V any](del Deleter[K, V]) WriteThroughOption[K, V] {
	if del == nil {
		panic("cache.WithDeleter: deleter must not be nil")
	}
	return func(c *writeThroughConfig[K, V]) { c.deleter = del }
}

// WriteThrough is a [Cache] that writes through to a backing store before
// updating a front cache, keeping the two consistent.
//
// [WriteThrough.SetContext] calls the [Writer] first and updates the front only
// if it succeeds. [WriteThrough.DeleteContext] calls the [Deleter] (if one was
// configured with [WithDeleter]) first and likewise updates the front only on
// success, so a backing failure never leaves the front ahead of the store.
// Reads and iteration come from the front; the backing store is never read.
//
// WriteThrough is safe for concurrent use when its front, writer, and deleter
// are.
type WriteThrough[K comparable, V any] struct {
	front   Cache[K, V]
	writer  Writer[K, V]
	deleter Deleter[K, V]
}

var _ Cache[int, int] = (*WriteThrough[int, int])(nil)

// NewWriteThrough wraps front with write-through persistence via write.
//
// By default only writes are mirrored to the backing store; add [WithDeleter]
// to mirror deletes as well.
//
// Panics if front or write is nil.
func NewWriteThrough[K comparable, V any](front Cache[K, V], write Writer[K, V], opts ...WriteThroughOption[K, V]) *WriteThrough[K, V] {
	if front == nil {
		panic("cache.NewWriteThrough: front must not be nil")
	}
	if write == nil {
		panic("cache.NewWriteThrough: writer must not be nil")
	}
	var cfg writeThroughConfig[K, V]
	for _, o := range opts {
		o(&cfg)
	}
	return &WriteThrough[K, V]{front: front, writer: write, deleter: cfg.deleter}
}

// Get implements [Cache.Get]. It reads from the front cache.
func (w *WriteThrough[K, V]) Get(key K) (V, bool) {
	return w.front.Get(key)
}

// Set implements [Cache.Set]. It writes through, returning the front's expiry
// (or the zero time if the backing write fails).
func (w *WriteThrough[K, V]) Set(key K, val V, ttl time.Duration) time.Time {
	t, _ := w.SetContext(context.Background(), key, val, ttl)
	return t
}

// Delete implements [Cache.Delete]. It deletes through to the backing store
// when a deleter is configured.
func (w *WriteThrough[K, V]) Delete(key K) {
	_ = w.DeleteContext(context.Background(), key)
}

// GetContext implements [Cache.GetContext]. It reads from the front cache.
func (w *WriteThrough[K, V]) GetContext(ctx context.Context, key K) (V, bool, error) {
	return w.front.GetContext(ctx, key)
}

// SetContext implements [Cache.SetContext], writing to the backing store before
// the front cache and leaving the front untouched if the write fails.
func (w *WriteThrough[K, V]) SetContext(ctx context.Context, key K, val V, ttl time.Duration) (time.Time, error) {
	if err := w.writer(ctx, key, val); err != nil {
		return time.Time{}, err
	}
	return w.front.SetContext(ctx, key, val, ttl)
}

// DeleteContext implements [Cache.DeleteContext], deleting from the backing
// store (when a deleter is configured) before the front cache and leaving the
// front untouched if the backing delete fails.
func (w *WriteThrough[K, V]) DeleteContext(ctx context.Context, key K) error {
	if w.deleter != nil {
		if err := w.deleter(ctx, key); err != nil {
			return err
		}
	}
	return w.front.DeleteContext(ctx, key)
}

// Items implements [Cache.Items], iterating the front cache only.
func (w *WriteThrough[K, V]) Items() iter.Seq2[K, V] {
	return w.front.Items()
}

// Close closes the front cache if it implements io.Closer.
//
// Closing a wrapper closes the layer beneath it, so closing the outermost cache
// in a stack stops the background maintenance of every [Memory] within it.
func (w *WriteThrough[K, V]) Close() error {
	if c, ok := w.front.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
