package cache

import (
	"context"
	"errors"
	"hash/maphash"
	"io"
	"iter"
	"time"
)

// integer is the set of integer key types supported by [ModuloShard].
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// HashShard returns a shard function that hashes any comparable key with
// hash/maphash.
//
// Each call captures a fresh seed, so independent HashShard functions spread the
// same keys differently. It is the general-purpose choice for [NewSharded].
func HashShard[K comparable]() func(K) uint64 {
	seed := maphash.MakeSeed()
	return func(key K) uint64 {
		return maphash.Comparable(seed, key)
	}
}

// ModuloShard returns a shard function that maps an integer key to its unsigned
// value, distributing keys across shards by value.
//
// It suits dense or sequential identifiers; for sparse or adversarial keys
// prefer [HashShard].
func ModuloShard[K integer]() func(K) uint64 {
	return func(key K) uint64 {
		return uint64(key)
	}
}

// Sharded is a [Cache] that partitions keys across a fixed number of backend
// caches, reducing lock contention on the hot path.
//
// Each backend is independent and an operation is routed to exactly one of them
// by the shard function. [Sharded.Items] visits the backends one at a time, and
// [Sharded.Close] closes any backend that implements io.Closer.
//
// Sharded is safe for concurrent use when its backends are.
type Sharded[K comparable, V any] struct {
	algorithm func(K) uint64
	shards    []Cache[K, V]
}

var _ Cache[int, int] = (*Sharded[int, int])(nil)

// NewSharded returns a [Sharded] cache of size backends.
//
// algorithm maps a key to an unsigned hash; the backend index is that hash
// modulo size. Use a preset such as [HashShard] or [ModuloShard], or supply
// your own. constructor builds each backend and is called size times.
//
// Panics if size is less than 1 or if algorithm or constructor is nil.
func NewSharded[K comparable, V any](size int, algorithm func(K) uint64, constructor func() Cache[K, V]) *Sharded[K, V] {
	if size < 1 {
		panic("cache.NewSharded: size must be at least 1")
	}
	if algorithm == nil {
		panic("cache.NewSharded: algorithm must not be nil")
	}
	if constructor == nil {
		panic("cache.NewSharded: constructor must not be nil")
	}
	shards := make([]Cache[K, V], size)
	for i := range shards {
		shards[i] = constructor()
	}
	return &Sharded[K, V]{algorithm: algorithm, shards: shards}
}

// Get implements [Cache.Get].
func (s *Sharded[K, V]) Get(key K) (V, bool) {
	return s.shard(key).Get(key)
}

// Set implements [Cache.Set].
func (s *Sharded[K, V]) Set(key K, val V, exp Expiry) time.Time {
	return s.shard(key).Set(key, val, exp)
}

// Delete implements [Cache.Delete].
func (s *Sharded[K, V]) Delete(key K) {
	s.shard(key).Delete(key)
}

// GetContext implements [Cache.GetContext].
func (s *Sharded[K, V]) GetContext(ctx context.Context, key K) (V, bool, error) {
	return s.shard(key).GetContext(ctx, key)
}

// SetContext implements [Cache.SetContext].
func (s *Sharded[K, V]) SetContext(ctx context.Context, key K, val V, exp Expiry) (time.Time, error) {
	return s.shard(key).SetContext(ctx, key, val, exp)
}

// DeleteContext implements [Cache.DeleteContext].
func (s *Sharded[K, V]) DeleteContext(ctx context.Context, key K) error {
	return s.shard(key).DeleteContext(ctx, key)
}

// Items implements [Cache.Items].
func (s *Sharded[K, V]) Items() iter.Seq2[K, V] {
	seq, _ := s.ItemsContext(context.Background())
	return seq
}

// ItemsContext implements [Cache.ItemsContext].
//
// It visits the backends in order, locking only one at a time, so a large cache
// stays available to writers throughout the scan. It stops at the first backend
// that reports an error or when ctx is cancelled, and the terminal accessor
// returns that error.
func (s *Sharded[K, V]) ItemsContext(ctx context.Context) (iter.Seq2[K, V], func() error) {
	var err error
	seq := func(yield func(K, V) bool) {
		for _, shard := range s.shards {
			if err = ctx.Err(); err != nil {
				return
			}
			sub, errf := shard.ItemsContext(ctx)
			for k, v := range sub {
				if !yield(k, v) {
					return
				}
			}
			if err = errf(); err != nil {
				return
			}
		}
	}
	return seq, func() error { return err }
}

// Close closes every backend that implements io.Closer and joins their errors.
//
// Backends that do not implement io.Closer are skipped, so Close is safe to
// call regardless of the backend type.
func (s *Sharded[K, V]) Close() error {
	var errs []error
	for _, shard := range s.shards {
		if c, ok := shard.(io.Closer); ok {
			if err := c.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// shard returns the backend responsible for key.
func (s *Sharded[K, V]) shard(key K) Cache[K, V] {
	return s.shards[s.algorithm(key)%uint64(len(s.shards))]
}
