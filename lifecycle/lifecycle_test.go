package lifecycle_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/remnestal/albstractions/lifecycle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("context not cancelled before Run", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		assert.NoError(t, g.Context().Err())
	})

	t.Run("WithGracefulTimeout panics on zero", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { lifecycle.New(lifecycle.WithGracefulTimeout(0)) })
	})

	t.Run("WithGracefulTimeout panics on negative", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { lifecycle.New(lifecycle.WithGracefulTimeout(-time.Second)) })
	})
}

func TestShutdown(t *testing.T) {
	t.Parallel()

	t.Run("idempotent across concurrent calls", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		var wg sync.WaitGroup
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				assert.NotPanics(t, g.Shutdown)
			}()
		}
		wg.Wait()
		assert.ErrorIs(t, g.Context().Err(), context.Canceled)
	})

	t.Run("before Run causes Run to return immediately", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		g.Shutdown()
		assert.NoError(t, g.Run())
	})
}

func TestRequire(t *testing.T) {
	t.Parallel()

	t.Run("error triggers shutdown and is returned from Run", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("unexpected failure")
		g := lifecycle.New()
		g.Require("failing", func(_ context.Context) error { return sentinel })
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		err := g.Run()
		assert.ErrorIs(t, err, sentinel)
	})

	t.Run("premature exit triggers shutdown without error", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		g.Require("early-exit", func(_ context.Context) error { return nil })
		var received atomic.Bool
		g.Require("waiting", func(ctx context.Context) error {
			<-ctx.Done()
			received.Store(true)
			return nil
		})

		assert.NoError(t, g.Run())
		assert.True(t, received.Load())
	})

	t.Run("context error is not collected", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		g.Require("ctx-aware", func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		go func() { time.Sleep(10 * time.Millisecond); g.Shutdown() }()

		assert.NoError(t, g.Run())
	})

	t.Run("dynamic registration after Run starts takes effect", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		dynamicRan := make(chan struct{})
		g.Require("outer", func(ctx context.Context) error {
			g.Require("inner", func(ctx context.Context) error {
				close(dynamicRan)
				<-ctx.Done()
				return nil
			})
			<-ctx.Done()
			return nil
		})
		go func() { <-dynamicRan; g.Shutdown() }()

		assert.NoError(t, g.Run())
	})

	t.Run("no-op after shutdown completes", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		done := make(chan error, 1)
		go func() { done <- g.Run() }()
		time.Sleep(10 * time.Millisecond)
		g.Shutdown()
		require.NoError(t, <-done)

		var ran atomic.Bool
		assert.NotPanics(t, func() {
			g.Require("late", func(_ context.Context) error { ran.Store(true); return nil })
		})
		assert.False(t, ran.Load())
	})
}

func TestOnShutdown(t *testing.T) {
	t.Parallel()

	t.Run("callbacks run in fifo registration order", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		var order []int
		for i := range 5 {
			i := i
			g.Defer(100*time.Millisecond, func(_ context.Context) { order = append(order, i) })
		}
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })
		go func() { time.Sleep(10 * time.Millisecond); g.Shutdown() }()

		require.NoError(t, g.Run())
		assert.Equal(t, []int{0, 1, 2, 3, 4}, order)
	})

	t.Run("no-op after shutdown begins", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		done := make(chan error, 1)
		go func() { done <- g.Run() }()
		time.Sleep(10 * time.Millisecond)
		g.Shutdown()
		require.NoError(t, <-done)

		var called atomic.Bool
		assert.NotPanics(t, func() {
			g.Defer(time.Second, func(_ context.Context) { called.Store(true) })
		})
		assert.False(t, called.Load())
	})

	t.Run("panics on zero timeout", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		assert.Panics(t, func() { g.Defer(0, func(_ context.Context) {}) })
	})

	t.Run("per-callback timeout bounds individual callback duration", func(t *testing.T) {
		t.Parallel()
		const cbTimeout = 50 * time.Millisecond
		g := lifecycle.New(lifecycle.WithGracefulTimeout(time.Second))
		var cbCtxCancelled atomic.Bool
		g.Defer(cbTimeout, func(ctx context.Context) {
			select {
			case <-ctx.Done():
				cbCtxCancelled.Store(true)
			case <-time.After(10 * cbTimeout):
			}
		})
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })
		go func() { time.Sleep(10 * time.Millisecond); g.Shutdown() }()

		start := time.Now()
		_ = g.Run()
		assert.Less(t, time.Since(start), 4*cbTimeout)
		assert.True(t, cbCtxCancelled.Load())
	})
}

func TestServe(t *testing.T) {
	t.Parallel()

	t.Run("starts server and shuts it down gracefully", func(t *testing.T) {
		t.Parallel()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := l.Addr().String()
		require.NoError(t, l.Close())

		server := &http.Server{
			Addr: addr,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
		}
		g := lifecycle.New()
		g.Serve("api", server)

		runErr := make(chan error, 1)
		go func() { runErr <- g.Run() }()

		require.Eventually(t, func() bool {
			resp, err := http.Get("http://" + addr + "/")
			return err == nil && resp.StatusCode == http.StatusOK
		}, time.Second, 10*time.Millisecond)

		g.Shutdown()
		assert.NoError(t, <-runErr)
	})

	t.Run("server bind failure triggers shutdown with error", func(t *testing.T) {
		t.Parallel()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { require.NoError(t, l.Close()) }()

		g := lifecycle.New(lifecycle.WithGracefulTimeout(100 * time.Millisecond))
		g.Serve("api", &http.Server{Addr: l.Addr().String()})

		require.Error(t, g.Run())
	})

	t.Run("servers drain before Defer callbacks regardless of registration order", func(t *testing.T) {
		t.Parallel()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := l.Addr().String()
		require.NoError(t, l.Close())

		var order []string
		var mu sync.Mutex
		record := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }

		// Defer registered BEFORE Serve — must still run after server drains.
		g := lifecycle.New()
		g.Defer(time.Second, func(_ context.Context) { record("callback") })
		g.Serve("api", &http.Server{
			Addr: addr,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				record("request")
				w.WriteHeader(http.StatusOK)
			}),
		})

		runErr := make(chan error, 1)
		go func() { runErr <- g.Run() }()

		require.Eventually(t, func() bool {
			resp, err := http.Get("http://" + addr + "/")
			return err == nil && resp.StatusCode == http.StatusOK
		}, time.Second, 10*time.Millisecond)

		g.Shutdown()
		require.NoError(t, <-runErr)

		mu.Lock()
		defer mu.Unlock()
		// server drained (request completed) before callback ran
		assert.Equal(t, []string{"request", "callback"}, order)
	})
}

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("graceful timeout bounds total shutdown duration", func(t *testing.T) {
		t.Parallel()
		const graceful = 50 * time.Millisecond
		g := lifecycle.New(lifecycle.WithGracefulTimeout(graceful))
		g.Defer(10*graceful, func(ctx context.Context) {
			select {
			case <-ctx.Done():
			case <-time.After(10 * graceful):
			}
		})
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })
		go func() { time.Sleep(10 * time.Millisecond); g.Shutdown() }()

		start := time.Now()
		_ = g.Run()
		assert.Less(t, time.Since(start), 4*graceful)
	})

	t.Run("configured signal triggers shutdown", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New(lifecycle.WithSignals(syscall.SIGUSR1))
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		runErr := make(chan error, 1)
		go func() { runErr <- g.Run() }()

		time.Sleep(10 * time.Millisecond)
		require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR1))

		select {
		case err := <-runErr:
			assert.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return after signal")
		}
	})

	t.Run("second call blocks and returns same error", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("fail")
		g := lifecycle.New()

		var teardownCount atomic.Int32
		g.Defer(time.Second, func(_ context.Context) { teardownCount.Add(1) })
		g.Require("failing", func(_ context.Context) error { return sentinel })
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		err1 := make(chan error, 1)
		err2 := make(chan error, 1)
		go func() { err1 <- g.Run() }()
		go func() { err2 <- g.Run() }()

		assert.ErrorIs(t, <-err1, sentinel)
		assert.ErrorIs(t, <-err2, sentinel)
		assert.Equal(t, int32(1), teardownCount.Load(), "teardown must run exactly once")
	})

	t.Run("concurrent registration and shutdown without data races", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New(lifecycle.WithGracefulTimeout(100 * time.Millisecond))

		var setup sync.WaitGroup
		for range 10 {
			setup.Add(1)
			go func() {
				defer setup.Done()
				g.Require("worker", func(ctx context.Context) error { <-ctx.Done(); return nil })
			}()
		}
		for range 5 {
			setup.Add(1)
			go func() {
				defer setup.Done()
				g.Defer(50*time.Millisecond, func(_ context.Context) {})
			}()
		}

		runErr := make(chan error, 1)
		go func() { runErr <- g.Run() }()
		setup.Wait()
		time.Sleep(10 * time.Millisecond)
		g.Shutdown()

		assert.NoError(t, <-runErr)
	})
}

func TestDone(t *testing.T) {
	t.Parallel()

	t.Run("closed only after Run returns", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		select {
		case <-g.Done():
			t.Fatal("Done should not be closed before Run")
		default:
		}

		runErr := make(chan error, 1)
		go func() { runErr <- g.Run() }()
		time.Sleep(10 * time.Millisecond)

		select {
		case <-g.Done():
			t.Fatal("Done should not be closed while Run is still running")
		default:
		}

		g.Shutdown()
		require.NoError(t, <-runErr)

		select {
		case <-g.Done():
		case <-time.After(time.Second):
			t.Fatal("Done was not closed after Run returned")
		}
	})
}

func TestReady(t *testing.T) {
	t.Parallel()

	t.Run("closed after pre-registered goroutines are spawned", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()

		started := make(chan struct{})
		g.Require("worker", func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})

		select {
		case <-g.Ready():
			t.Fatal("Ready should not be closed before Run")
		default:
		}

		go func() {
			<-g.Ready()
			g.Shutdown()
		}()

		assert.NoError(t, g.Run())

		select {
		case <-g.Ready():
		default:
			t.Fatal("Ready should be closed after Run")
		}
	})

	t.Run("closed immediately when no goroutines are pre-registered", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		g.Shutdown()
		_ = g.Run()

		select {
		case <-g.Ready():
		case <-time.After(time.Second):
			t.Fatal("Ready was not closed")
		}
	})
}

func TestShutdownCause(t *testing.T) {
	t.Parallel()

	t.Run("nil for explicit Shutdown call", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		go func() { time.Sleep(10 * time.Millisecond); g.Shutdown() }()
		require.NoError(t, g.Run())
		assert.Nil(t, g.ShutdownCause())
	})

	t.Run("ErrSignal for OS signal", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New(lifecycle.WithSignals(syscall.SIGUSR2))
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		runErr := make(chan error, 1)
		go func() { runErr <- g.Run() }()

		time.Sleep(10 * time.Millisecond)
		require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR2))

		select {
		case <-runErr:
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return")
		}
		assert.ErrorIs(t, g.ShutdownCause(), lifecycle.ErrSignal)
	})

	t.Run("goroutine error for goroutine failure", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("worker failed")
		g := lifecycle.New()
		g.Require("failing", func(_ context.Context) error { return sentinel })
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		require.ErrorIs(t, g.Run(), sentinel)
		assert.ErrorIs(t, g.ShutdownCause(), sentinel)
	})

	t.Run("goroutine name appears in cause on error", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("boom")
		g := lifecycle.New()
		g.Require("my-worker", func(_ context.Context) error { return sentinel })
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		require.ErrorIs(t, g.Run(), sentinel)
		cause := g.ShutdownCause()
		assert.ErrorIs(t, cause, sentinel)
		assert.Contains(t, cause.Error(), "my-worker")
	})

	t.Run("premature goroutine exit produces non-nil cause with name", func(t *testing.T) {
		t.Parallel()
		g := lifecycle.New()
		g.Require("early-exit", func(_ context.Context) error { return nil })
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		assert.NoError(t, g.Run())
		cause := g.ShutdownCause()
		assert.NotNil(t, cause)
		assert.Contains(t, cause.Error(), "early-exit")
	})
}

func TestWithContext(t *testing.T) {
	t.Parallel()

	t.Run("parent cancellation triggers shutdown", func(t *testing.T) {
		t.Parallel()
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()

		g := lifecycle.New(lifecycle.WithContext(parent))
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		runErr := make(chan error, 1)
		go func() { runErr <- g.Run() }()

		time.Sleep(10 * time.Millisecond)
		cancel()

		select {
		case err := <-runErr:
			assert.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return after parent context was cancelled")
		}
	})

	t.Run("cause is parent context error", func(t *testing.T) {
		t.Parallel()
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()

		g := lifecycle.New(lifecycle.WithContext(parent))
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		go func() { time.Sleep(10 * time.Millisecond); cancel() }()
		require.NoError(t, g.Run())
		assert.ErrorIs(t, g.ShutdownCause(), context.Canceled)
	})
}

func TestWithErrorHandler(t *testing.T) {
	t.Parallel()

	t.Run("called with error when goroutine fails", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("handler test")
		var handled atomic.Value
		g := lifecycle.New(lifecycle.WithErrorHandler(func(err error) {
			handled.Store(err)
		}))
		g.Require("failing", func(_ context.Context) error { return sentinel })
		g.Require("waiting", func(ctx context.Context) error { <-ctx.Done(); return nil })

		err := g.Run()
		require.ErrorIs(t, err, sentinel)
		require.NotNil(t, handled.Load())
		assert.ErrorIs(t, handled.Load().(error), sentinel)
	})
}
