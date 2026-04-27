package retry_test

import (
	"errors"
	"testing"
	"time"

	"github.com/remnestal/albstractions/retry"
	"github.com/stretchr/testify/assert"
)

func TestMaxAttempts(t *testing.T) {
	t.Parallel()

	t.Run("halts when attempt reaches n", func(t *testing.T) {
		t.Parallel()
		stop := retry.MaxAttempts(3)
		assert.False(t, stop(1, 0, errors.New("x")))
		assert.False(t, stop(2, 0, errors.New("x")))
		assert.True(t, stop(3, 0, errors.New("x")))
		assert.True(t, stop(4, 0, errors.New("x")))
	})

	t.Run("panics on non-positive n", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.MaxAttempts(0) })
		assert.Panics(t, func() { retry.MaxAttempts(-1) })
	})
}

func TestMaxElapsed(t *testing.T) {
	t.Parallel()

	t.Run("halts when elapsed reaches d", func(t *testing.T) {
		t.Parallel()
		stop := retry.MaxElapsed(100 * time.Millisecond)
		assert.False(t, stop(1, 0, nil))
		assert.False(t, stop(1, 50*time.Millisecond, nil))
		assert.True(t, stop(1, 100*time.Millisecond, nil))
		assert.True(t, stop(1, 200*time.Millisecond, nil))
	})

	t.Run("panics on non-positive d", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.MaxElapsed(0) })
		assert.Panics(t, func() { retry.MaxElapsed(-1 * time.Second) })
	})
}

func TestOnError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("permanent")

	t.Run("halts when pred returns true", func(t *testing.T) {
		t.Parallel()
		stop := retry.OnError(func(err error) bool { return errors.Is(err, sentinel) })
		assert.False(t, stop(1, 0, errors.New("transient")))
		assert.True(t, stop(1, 0, sentinel))
	})

	t.Run("panics on nil pred", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.OnError(nil) })
	})
}

func TestAny(t *testing.T) {
	t.Parallel()

	t.Run("halts if any halts", func(t *testing.T) {
		t.Parallel()
		stop := retry.Any(
			retry.MaxAttempts(10),
			retry.MaxElapsed(50*time.Millisecond),
		)
		assert.False(t, stop(1, 0, nil))
		assert.True(t, stop(1, 60*time.Millisecond, nil), "elapsed budget should trigger")
		assert.True(t, stop(10, 0, nil), "attempt budget should trigger")
	})

	t.Run("does not halt if none halts", func(t *testing.T) {
		t.Parallel()
		stop := retry.Any(retry.MaxAttempts(5), retry.MaxElapsed(time.Hour))
		assert.False(t, stop(1, 0, nil))
	})

	t.Run("panics on empty", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.Any() })
	})

	t.Run("panics on nil entry", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.Any(retry.MaxAttempts(1), nil) })
	})
}

func TestAll(t *testing.T) {
	t.Parallel()

	t.Run("halts only when all halt", func(t *testing.T) {
		t.Parallel()
		stop := retry.All(
			retry.MaxAttempts(3),
			retry.MaxElapsed(50*time.Millisecond),
		)
		assert.False(t, stop(3, 0, nil), "attempts halt, elapsed does not")
		assert.False(t, stop(1, 60*time.Millisecond, nil), "elapsed halts, attempts does not")
		assert.True(t, stop(3, 60*time.Millisecond, nil), "both halt")
	})

	t.Run("panics on empty", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.All() })
	})

	t.Run("panics on nil entry", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.All(nil, retry.MaxAttempts(1)) })
	})
}
