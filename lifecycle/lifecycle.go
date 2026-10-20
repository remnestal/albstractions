// Package lifecycle manages the full lifecycle of a Go application.
//
// Create a [Group] with [New], register required goroutines with
// [Group.Require], attach HTTP servers with [Group.Serve], and register
// teardown callbacks with [Group.Defer]. Call [Group.Run] to start
// everything and block until shutdown completes.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"
)

const defaultGracefulTimeout time.Duration = 30 * time.Second

// ErrSignal is returned by [Group.ShutdownCause] when an OS signal triggered
// shutdown.
var ErrSignal = errors.New("signal received")

// teardown pairs a per-callback deadline with its shutdown function.
type teardown struct {
	timeout time.Duration
	fn      func(context.Context)
}

// config holds the immutable options set during [New].
type config struct {
	graceful   time.Duration
	signals    []os.Signal
	errHandler func(error)
	parentCtx  context.Context // nil unless WithContext provided
}

// Group coordinates the startup and graceful shutdown of an application.
//
// Register required goroutines with [Group.Require], HTTP servers with
// [Group.Serve], and teardown callbacks with [Group.Defer]. All three
// can be called before or after [Group.Run] — registrations after Run starts
// take effect immediately; registrations after shutdown begins are silently
// ignored.
//
// Group is safe for concurrent use.
type Group struct {
	cfg config // frozen after New

	ctx          context.Context
	cancel       context.CancelFunc
	shutdownOnce sync.Once // Shutdown idempotency + cause recording

	runOnce sync.Once     // run() executes at most once
	done    chan struct{} // closed when run() returns
	runErr  error         // stored result for idempotent Run() callers
	ready   chan struct{} // closed once all pre-Run goroutines are spawned

	mu              sync.Mutex
	cause           error // why shutdown was triggered; set inside shutdownOnce
	started         bool
	stopped         bool
	pending         []func()
	serverTeardowns []func(context.Context) error // always run before regular teardowns
	teardowns       []teardown
	errs            []error
	wg              sync.WaitGroup
}

// Option configures a Group.
type Option func(*Group)

// WithGracefulTimeout sets the total time budget for graceful shutdown,
// covering server draining, teardown callbacks, and goroutine drain combined.
//
// Defaults to 30s if not provided.
//
// Panics if d <= 0.
func WithGracefulTimeout(d time.Duration) Option {
	if d <= 0 {
		panic("lifecycle.WithGracefulTimeout: duration must be > 0")
	}
	return func(g *Group) {
		g.cfg.graceful = d
	}
}

// WithContext ties the group's lifetime to a parent context.
//
// When the parent context is cancelled, shutdown is triggered and
// [Group.ShutdownCause] returns the parent's error.
func WithContext(ctx context.Context) Option {
	return func(g *Group) {
		g.cfg.parentCtx = ctx
	}
}

// WithSignals configures OS signals that trigger shutdown.
func WithSignals(sigs ...os.Signal) Option {
	return func(g *Group) {
		g.cfg.signals = append(g.cfg.signals, sigs...)
	}
}

// WithErrorHandler registers a callback invoked each time a goroutine's error
// is collected.
//
// A context error returned once shutdown has begun is a clean stop, not a
// failure, and is not passed to the callback.
//
// The callback is called from the goroutine that produced the error and may
// be invoked concurrently if multiple goroutines fail simultaneously — it
// must be safe for concurrent use.
//
// Errors from HTTP servers that fail to drain during teardown are not passed
// to the callback; they appear only in [Group.Run]'s returned error.
func WithErrorHandler(fn func(error)) Option {
	return func(g *Group) {
		g.cfg.errHandler = fn
	}
}

// New creates a Group.
//
// The graceful shutdown budget defaults to 30s and can be overridden with
// [WithGracefulTimeout]. Use [WithSignals] to trigger shutdown on OS signals,
// and [WithContext] to tie the group's lifetime to a parent context.
func New(opts ...Option) *Group {
	ctx, cancel := context.WithCancel(context.Background())
	g := &Group{
		cfg:    config{graceful: defaultGracefulTimeout},
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		ready:  make(chan struct{}),
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Require registers a required goroutine to run within the group.
//
// The name identifies the goroutine in [Group.ShutdownCause] when it is the
// trigger. If fn returns a non-nil error, shutdown is triggered and the error
// is included in [Group.Run]'s return value. If fn returns nil while the
// group's context is still active (premature exit), shutdown is triggered and
// [Group.ShutdownCause] records the name.
//
// A context error (context.Canceled, context.DeadlineExceeded) counts as a
// clean stop only once shutdown has begun. Returned while the group is still
// running, for example from fn's own timeout, it is a failure like any other.
//
// Require may be called before or after Run. If called while shutdown is in
// progress, the call is a no-op.
func (g *Group) Require(name string, fn func(ctx context.Context) error) {
	g.requireNamed("required process", name, fn)
}

// Defer registers a teardown callback executed during graceful shutdown.
//
// Each callback receives its own context bounded by timeout, which is itself
// bounded by the overall graceful deadline. Callbacks run sequentially in
// registration order, after all HTTP servers registered with [Group.Serve]
// have been fully drained.
//
// Defer may be called before or after [Group.Run]. If called while
// shutdown is already in progress, the call is a no-op.
//
// Panics if timeout <= 0.
func (g *Group) Defer(timeout time.Duration, fn func(ctx context.Context)) {
	if timeout <= 0 {
		panic("lifecycle.Defer: timeout must be > 0")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	g.teardowns = append(g.teardowns, teardown{timeout: timeout, fn: fn})
}

// Serve registers an HTTP server as a required goroutine.
//
// ListenAndServe is started as a goroutine; server.Shutdown is registered to
// run at the start of teardown, before any [Group.Defer] callbacks,
// regardless of registration order. http.ErrServerClosed is swallowed — it is
// the expected return value once server.Shutdown is called.
//
// All servers are shut down concurrently and share the graceful budget. A
// server that has not drained by the deadline has its remaining connections
// dropped with server.Close, and an error naming it, wrapping
// context.DeadlineExceeded, is included in [Group.Run]'s error.
func (g *Group) Serve(name string, server *http.Server) {
	// The teardown goes first: once registered, it is in any later shutdown,
	// so the goroutine is never left running without it. If shutdown wins the
	// race in between, the goroutine is dropped and the teardown shuts down a
	// server that never started, which is harmless.
	g.mu.Lock()
	if !g.stopped {
		g.serverTeardowns = append(g.serverTeardowns, func(ctx context.Context) error {
			if err := server.Shutdown(ctx); err != nil {
				_ = server.Close()
				return fmt.Errorf("http server %q: shutdown: %w", name, err)
			}
			return nil
		})
	}
	g.mu.Unlock()
	g.requireNamed("http server", name, func(_ context.Context) error {
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
}

// Shutdown triggers graceful shutdown.
//
// It is safe to call Shutdown multiple times and from multiple goroutines;
// only the first call has any effect. [Group.ShutdownCause] returns nil when
// shutdown was triggered by this method — use it to distinguish autonomous
// shutdowns (signal, parent context, goroutine failure) from intentional ones.
func (g *Group) Shutdown() {
	g.shutdown(nil)
}

// Context returns the group's lifecycle context, cancelled when shutdown begins.
//
// The context is valid before [Group.Run] is called. Goroutines holding this
// context should return promptly once it is cancelled.
func (g *Group) Context() context.Context {
	return g.ctx
}

// Done returns a channel that is closed when [Group.Run] has fully returned.
//
// It can be used to observe group completion without blocking on Run.
func (g *Group) Done() <-chan struct{} {
	return g.done
}

// Ready returns a channel that is closed once all goroutines registered before
// [Group.Run] was called have been spawned and any [WithSignals] handlers are
// installed.
//
// It provides a synchronisation point for dependent services or health checks
// that should not start until the group is fully running.
func (g *Group) Ready() <-chan struct{} {
	return g.ready
}

// ShutdownCause returns why shutdown was triggered autonomously by the group.
//
// Returns nil if shutdown was triggered by an explicit [Group.Shutdown] call.
// Returns [ErrSignal] if an OS signal was received, the parent context's error
// if [WithContext] was used, or a wrapped error naming the goroutine when a
// registered goroutine fails or exits prematurely. When multiple goroutines
// fail simultaneously, the cause is the first error recorded; [Group.Run]'s
// return value contains all errors.
//
// The returned value is stable once Run returns (or [Group.Done] is closed).
func (g *Group) ShutdownCause() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cause
}

// Run starts all registered goroutines and blocks until shutdown completes.
//
// Shutdown begins when an OS signal is received (if configured with
// [WithSignals]), [Group.Shutdown] is called, the parent context is cancelled
// (if configured with [WithContext]), or a goroutine fails or exits
// prematurely. Run then drains all registered HTTP servers concurrently,
// executes teardown callbacks in registration order within their individual
// deadlines, waits for goroutines to exit, up to the graceful budget, and
// returns.
//
// Run returns the combined errors from goroutines and from HTTP servers that
// failed to drain, via errors.Join. Context errors (context.Canceled,
// context.DeadlineExceeded) that goroutines return after shutdown has begun do
// not contribute; one returned while the group is still running does, as does
// a drain cut short, with an error wrapping context.DeadlineExceeded. So
// errors.Is(err, context.DeadlineExceeded) does not mean a clean stop.
//
// Run is safe to call multiple times: the second and subsequent calls block
// until the first call completes, then return the same error.
func (g *Group) Run() error {
	g.runOnce.Do(func() {
		g.runErr = g.run()
		close(g.done)
	})
	<-g.done
	return g.runErr
}

// run is the internal single-execution body of Run.
func (g *Group) run() error {
	g.mu.Lock()
	g.started = true
	pending := g.pending
	g.pending = nil
	g.mu.Unlock()

	for _, fn := range pending {
		g.wg.Go(func() {
			fn()
		})
	}

	if g.cfg.parentCtx != nil {
		go func() {
			select {
			case <-g.cfg.parentCtx.Done():
				g.shutdown(g.cfg.parentCtx.Err())
			case <-g.ctx.Done():
			}
		}()
	}

	if len(g.cfg.signals) > 0 {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, g.cfg.signals...)
		go func() {
			defer signal.Stop(sigCh)
			select {
			case <-sigCh:
				g.shutdown(ErrSignal)
			case <-g.ctx.Done():
			}
		}()
	}

	close(g.ready)

	<-g.ctx.Done()

	g.mu.Lock()
	g.stopped = true
	serverTeardowns := g.serverTeardowns
	teardowns := g.teardowns
	g.mu.Unlock()

	overallCtx, overallCancel := context.WithTimeout(context.Background(), g.cfg.graceful)
	defer overallCancel()

	// Servers drain concurrently, and every one is shut down even once the
	// budget is spent: Shutdown with an expired context still closes the
	// listeners. Errors are kept in registration order.
	drainErrs := make([]error, len(serverTeardowns))
	var drains sync.WaitGroup
	for i, fn := range serverTeardowns {
		drains.Go(func() { drainErrs[i] = fn(overallCtx) })
	}
	drains.Wait()
	g.mu.Lock()
	for _, err := range drainErrs {
		if err != nil {
			g.errs = append(g.errs, err)
		}
	}
	g.mu.Unlock()

	for _, td := range teardowns {
		if overallCtx.Err() != nil {
			break
		}
		cbCtx, cbCancel := context.WithTimeout(overallCtx, td.timeout)
		td.fn(cbCtx)
		cbCancel()
	}

	drain := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(drain)
	}()
	select {
	case <-drain:
	case <-overallCtx.Done():
	}

	g.mu.Lock()
	err := errors.Join(g.errs...)
	g.mu.Unlock()
	return err
}

// requireNamed registers a goroutine with a labelled kind and name so that
// both appear in [Group.ShutdownCause] if the goroutine triggers shutdown.
func (g *Group) requireNamed(kind, name string, fn func(ctx context.Context) error) {
	g.register(func() {
		err := fn(g.ctx)
		if err != nil && (!g.shuttingDown() || !isContextErr(err)) {
			g.collectError(err, fmt.Errorf("%s %q: %w", kind, name, err))
			return
		}
		if g.ctx.Err() == nil {
			g.shutdown(fmt.Errorf("%s %q: unexpected exit", kind, name))
		}
	})
}

// register adds a goroutine body to the group.
// If the group is running, the goroutine starts immediately.
// If the group is shutting down, the call is a no-op.
func (g *Group) register(fn func()) {
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return
	}
	if g.started {
		// wg.Add under the mutex prevents a WaitGroup race: stopped=true is
		// also set under the mutex, so either this Add happens before the Wait
		// (goroutine is counted) or stopped=true is seen and we return early.
		g.wg.Add(1)
		g.mu.Unlock()
		go func() {
			defer g.wg.Done()
			fn()
		}()
		return
	}
	g.pending = append(g.pending, fn)
	g.mu.Unlock()
}

// collectError records rawErr for Run's return value, invokes the error
// handler if set, and triggers shutdown with the pre-formatted cause.
func (g *Group) collectError(rawErr, cause error) {
	if g.cfg.errHandler != nil {
		g.cfg.errHandler(rawErr)
	}
	g.mu.Lock()
	g.errs = append(g.errs, rawErr)
	g.mu.Unlock()
	g.shutdown(cause)
}

// shutdown records cause and cancels the group's context. Only the first call
// has any effect; subsequent calls are no-ops.
func (g *Group) shutdown(cause error) {
	g.shutdownOnce.Do(func() {
		g.mu.Lock()
		g.cause = cause
		g.mu.Unlock()
		g.cancel()
	})
}

// isContextErr reports whether err wraps a context cancellation or deadline error.
func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// shuttingDown reports whether shutdown has begun, counting a cancelled
// [WithContext] parent whose watcher has not yet called shutdown.
func (g *Group) shuttingDown() bool {
	return g.ctx.Err() != nil || (g.cfg.parentCtx != nil && g.cfg.parentCtx.Err() != nil)
}
