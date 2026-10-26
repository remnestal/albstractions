package cache

import (
	"context"
	"io"
	"iter"
	"sync"
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
// Concurrent writes and deletes of the same key through one WriteThrough are
// serialised, each holding the key across its writer or deleter call and the
// front update, so the front ends in the same state as the store. This does not cover other writers to the
// store (other processes or WriteThrough instances), or a front that fills
// itself, such as a [ReadThrough].
//
// [WriteThrough.SetContext] calls the [Writer] first and updates the front only
// if it succeeds. [WriteThrough.DeleteContext] calls the [Deleter] (if one was
// configured with [WithDeleter]) first and likewise updates the front only on
// success, so a backing failure never leaves the front ahead of the store.
// Reads and iteration come from the front; the backing store is never read.
//
// WriteThrough is safe for concurrent use when its front, writer, and deleter
// are. The writer, deleter and front must not write or delete through the same
// WriteThrough for the key they are handling, or that call deadlocks.
type WriteThrough[K comparable, V any] struct {
	front   Cache[K, V]
	writer  Writer[K, V]
	deleter Deleter[K, V]

	mu    sync.Mutex
	locks map[K]*keyLock // per-key write locks, removed when unused
}

// keyLock serialises writes to one key. A one-slot channel rather than a mutex
// lets a waiter give up when its context ends.
type keyLock struct {
	ch   chan struct{}
	refs int // holders and waiters; the entry is removed at zero
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
	return &WriteThrough[K, V]{front: front, writer: write, deleter: cfg.deleter, locks: make(map[K]*keyLock)}
}

// Get implements [Cache.Get]. It reads from the front cache.
func (w *WriteThrough[K, V]) Get(key K) (V, bool) {
	return w.front.Get(key)
}

// Set implements [Cache.Set]. It writes through, returning the front's expiry.
//
// The infallible form cannot report a backing-store failure: on a failed write
// it returns the zero time, which is indistinguishable from a successful write of
// a never-expiring entry. Use [WriteThrough.SetContext] when the caller must
// observe write errors.
func (w *WriteThrough[K, V]) Set(key K, val V, exp Expiry) time.Time {
	t, _ := w.SetContext(context.Background(), key, val, exp)
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
//
// It first waits for any other write or delete of key through w to finish. If
// ctx ends while waiting, it returns [context.Cause] of ctx without writing.
func (w *WriteThrough[K, V]) SetContext(ctx context.Context, key K, val V, exp Expiry) (time.Time, error) {
	l, err := w.lock(ctx, key)
	if err != nil {
		return time.Time{}, err
	}
	defer w.unlock(key, l)
	if err := w.writer(ctx, key, val); err != nil {
		return time.Time{}, err
	}
	return w.front.SetContext(ctx, key, val, exp)
}

// DeleteContext implements [Cache.DeleteContext], deleting from the backing
// store (when a deleter is configured) before the front cache and leaving the
// front untouched if the backing delete fails.
//
// It first waits for any other write or delete of key through w to finish. If
// ctx ends while waiting, it returns [context.Cause] of ctx without deleting.
func (w *WriteThrough[K, V]) DeleteContext(ctx context.Context, key K) error {
	l, err := w.lock(ctx, key)
	if err != nil {
		return err
	}
	defer w.unlock(key, l)
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

// ItemsContext implements [Cache.ItemsContext], iterating the front cache only.
func (w *WriteThrough[K, V]) ItemsContext(ctx context.Context) (iter.Seq2[K, V], func() error) {
	return w.front.ItemsContext(ctx)
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

// lock waits for exclusive use of key among w's writes and deletes, returning
// [context.Cause] of ctx if it ends first.
func (w *WriteThrough[K, V]) lock(ctx context.Context, key K) (*keyLock, error) {
	if key != key { // NaN never matches a map key, so it would leak an entry
		return nil, nil
	}
	w.mu.Lock()
	l := w.locks[key]
	if l == nil {
		l = &keyLock{ch: make(chan struct{}, 1)}
		w.locks[key] = l
	}
	l.refs++
	w.mu.Unlock()

	// Uncontended calls take the lock without a select, so an already-done ctx
	// reaches the writer exactly as before.
	select {
	case l.ch <- struct{}{}:
		return l, nil
	default:
	}
	select {
	case l.ch <- struct{}{}:
		return l, nil
	case <-ctx.Done():
		w.release(key, l)
		return nil, context.Cause(ctx)
	}
}

// unlock releases a lock taken by lock.
func (w *WriteThrough[K, V]) unlock(key K, l *keyLock) {
	if l == nil {
		return
	}
	<-l.ch
	w.release(key, l)
}

// release drops one reference to l, removing it from w.locks when unused.
func (w *WriteThrough[K, V]) release(key K, l *keyLock) {
	w.mu.Lock()
	if l.refs--; l.refs == 0 {
		delete(w.locks, key)
	}
	w.mu.Unlock()
}
