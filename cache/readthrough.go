package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"sync"
	"time"
)

// Loader fetches the value for a key from a backing source on a cache miss.
//
// A loader signals genuine absence by returning an error that [errors.Is]
// matches [ErrCacheMiss] or an error registered with [WithMissError]; any other
// error is treated as a transient failure and propagated.
type Loader[K comparable, V any] func(ctx context.Context, key K) (V, error)

// ReadThroughOption configures a [ReadThrough].
type ReadThroughOption func(*readThroughConfig)

type readThroughConfig struct {
	singleFlight bool
	missErrs     []error
}

// WithSingleFlight collapses concurrent loads of the same key into a single
// call to the [Loader], sharing its result among the waiters.
//
// It prevents a thundering herd of loads when a popular key is missing.
func WithSingleFlight() ReadThroughOption {
	return func(c *readThroughConfig) { c.singleFlight = true }
}

// WithMissError registers loader errors that mean "genuine miss" in addition to
// the built-in [ErrCacheMiss].
//
// A loader error matching any of them (via [errors.Is]) makes a read return an
// ordinary miss instead of propagating the error.
func WithMissError(errs ...error) ReadThroughOption {
	return func(c *readThroughConfig) { c.missErrs = append(c.missErrs, errs...) }
}

// ReadThrough is a [Cache] that loads missing entries from a backing source and
// populates a front cache.
//
// On a miss in the front cache it calls the [Loader]; a loaded value is stored
// in the front under [Default] and returned. A loader error recognised as a
// miss surfaces as an ordinary miss (false, nil) so the caller can create the
// value while keeping the cache layer; any other error propagates. Writes and
// deletes pass straight through to the front, and [ReadThrough.Items] iterates
// the front only.
//
// A value written with [ReadThrough.Set] while a load for the same key is in
// flight may be overwritten by the load's now-stale result: read-through
// populates the front without coordinating with concurrent writers.
//
// ReadThrough is safe for concurrent use when its front and loader are.
type ReadThrough[K comparable, V any] struct {
	front    Cache[K, V]
	load     Loader[K, V]
	missErrs []error
	flight   *flightGroup[K, V]
}

var _ Cache[int, int] = (*ReadThrough[int, int])(nil)

// NewReadThrough wraps front with read-through loading via load.
//
// By default each missing key triggers its own load; add [WithSingleFlight] to
// collapse concurrent loads of the same key, and [WithMissError] to treat extra
// loader errors as misses.
//
// Panics if front or load is nil.
func NewReadThrough[K comparable, V any](front Cache[K, V], load Loader[K, V], opts ...ReadThroughOption) *ReadThrough[K, V] {
	if front == nil {
		panic("cache.NewReadThrough: front must not be nil")
	}
	if load == nil {
		panic("cache.NewReadThrough: loader must not be nil")
	}
	var cfg readThroughConfig
	for _, o := range opts {
		o(&cfg)
	}
	r := &ReadThrough[K, V]{
		front:    front,
		load:     load,
		missErrs: cfg.missErrs,
	}
	if cfg.singleFlight {
		r.flight = &flightGroup[K, V]{m: make(map[K]*call[V])}
	}
	return r
}

// LoaderFromCache adapts a [Cache] into a [Loader], returning [ErrCacheMiss] on
// a miss.
//
// It lets a slower cache act as the backing source for a faster one, so that
// read-through tiers compose: NewReadThrough(l1, LoaderFromCache(l2)).
func LoaderFromCache[K comparable, V any](c Cache[K, V]) Loader[K, V] {
	return func(ctx context.Context, key K) (V, error) {
		v, ok, err := c.GetContext(ctx, key)
		if err != nil {
			return v, err
		}
		if !ok {
			var zero V
			return zero, ErrCacheMiss
		}
		return v, nil
	}
}

// Get implements [Cache.Get].
func (r *ReadThrough[K, V]) Get(key K) (V, bool) {
	return miss(r.GetContext(context.Background(), key))
}

// Set implements [Cache.Set]. It passes through to the front cache.
func (r *ReadThrough[K, V]) Set(key K, val V, exp Expiry) time.Time {
	return r.front.Set(key, val, exp)
}

// Delete implements [Cache.Delete]. It passes through to the front cache.
func (r *ReadThrough[K, V]) Delete(key K) {
	r.front.Delete(key)
}

// GetContext implements [Cache.GetContext], loading and populating on a miss.
func (r *ReadThrough[K, V]) GetContext(ctx context.Context, key K) (V, bool, error) {
	if v, ok, err := r.front.GetContext(ctx, key); err != nil || ok {
		return v, ok, err
	}
	v, err := r.loadAndStore(ctx, key)
	if err != nil {
		var zero V
		if r.isMiss(err) {
			return zero, false, nil
		}
		return zero, false, err
	}
	return v, true, nil
}

// SetContext implements [Cache.SetContext]. It passes through to the front.
func (r *ReadThrough[K, V]) SetContext(ctx context.Context, key K, val V, exp Expiry) (time.Time, error) {
	return r.front.SetContext(ctx, key, val, exp)
}

// DeleteContext implements [Cache.DeleteContext]. It passes through to the front.
func (r *ReadThrough[K, V]) DeleteContext(ctx context.Context, key K) error {
	return r.front.DeleteContext(ctx, key)
}

// Items implements [Cache.Items], iterating the front cache only.
func (r *ReadThrough[K, V]) Items() iter.Seq2[K, V] {
	return r.front.Items()
}

// ItemsContext implements [Cache.ItemsContext], iterating the front cache only.
func (r *ReadThrough[K, V]) ItemsContext(ctx context.Context) (iter.Seq2[K, V], func() error) {
	return r.front.ItemsContext(ctx)
}

// Close closes the front cache if it implements io.Closer.
//
// Closing a wrapper closes the layer beneath it, so closing the outermost cache
// in a stack stops the background maintenance of every [Memory] within it.
func (r *ReadThrough[K, V]) Close() error {
	if c, ok := r.front.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// loadAndStore loads key and, on success, stores it in the front cache,
// collapsing concurrent loads when single-flight is enabled.
func (r *ReadThrough[K, V]) loadAndStore(ctx context.Context, key K) (V, error) {
	load := func() (V, error) {
		v, err := r.load(ctx, key)
		if err != nil {
			return v, err
		}
		_, _ = r.front.SetContext(ctx, key, v, Default)
		return v, nil
	}
	if r.flight != nil {
		return r.flight.Do(key, load)
	}
	return load()
}

// isMiss reports whether err signals a genuine absence rather than a failure.
func (r *ReadThrough[K, V]) isMiss(err error) bool {
	if errors.Is(err, ErrCacheMiss) {
		return true
	}
	for _, e := range r.missErrs {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// call is a single in-flight load shared by [flightGroup].
type call[V any] struct {
	wg  sync.WaitGroup
	val V
	err error
}

// flightGroup deduplicates concurrent loads of the same key.
type flightGroup[K comparable, V any] struct {
	mu sync.Mutex
	m  map[K]*call[V]
}

// Do runs fn for key, or waits for and returns the result of an in-flight call
// for the same key.
//
// If fn panics, the in-flight entry is removed and waiters receive an error
// instead of blocking forever; the panic is then re-raised on the calling
// goroutine.
func (g *flightGroup[K, V]) Do(key K, fn func() (V, error)) (V, error) {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &call[V]{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	// Clean up and release waiters even if fn panics: a panicking loader must not
	// wedge the key, or waiters and every future caller would block forever.
	// Waiters receive the error; the panic is re-raised on this goroutine.
	defer func() {
		r := recover()
		if r != nil {
			c.err = fmt.Errorf("cache: read-through loader panicked: %v", r)
		}
		g.mu.Lock()
		delete(g.m, key)
		g.mu.Unlock()
		c.wg.Done()
		if r != nil {
			panic(r)
		}
	}()

	c.val, c.err = fn()
	return c.val, c.err
}
