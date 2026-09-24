package metrics

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"taskmanager-ai/internal/collector"
)

const (
	DefaultRollupInterval = 3 * time.Minute
	DefaultPruneInterval  = 24 * time.Hour
)

// Store is the persistence contract used by the metrics worker and runtime.
// Implementations are expected to make rollup creation idempotent for a
// completed bucket.
type Store interface {
	SaveMetricSample(context.Context, collector.SystemSnapshot) error
	BuildMetricRollups(context.Context, time.Time) error
	PruneMetricHistory(context.Context, time.Time) error
}

type WorkerOptions struct {
	RollupInterval time.Duration
	PruneInterval  time.Duration
	Now            func() time.Time
}

// Worker periodically materializes completed metric buckets. It aligns each
// run to UTC epoch boundaries so a restart cannot create a different bucket
// layout. The first run is immediate and also performs backfill.
type Worker struct {
	store Store
	log   *slog.Logger
	now   func() time.Time

	rollupInterval time.Duration
	pruneInterval  time.Duration

	mu         sync.Mutex
	running    bool
	lastPruned time.Time
	wg         sync.WaitGroup
}

func NewWorker(store Store, logger *slog.Logger) *Worker {
	return NewWorkerWithOptions(store, logger, WorkerOptions{})
}

func NewWorkerWithOptions(store Store, logger *slog.Logger, options WorkerOptions) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	if options.RollupInterval <= 0 {
		options.RollupInterval = DefaultRollupInterval
	}
	if options.PruneInterval <= 0 {
		options.PruneInterval = DefaultPruneInterval
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Worker{
		store:          store,
		log:            logger,
		now:            options.Now,
		rollupInterval: options.RollupInterval,
		pruneInterval:  options.PruneInterval,
	}
}

// Start starts the worker once. A completed worker may be started again after
// Wait returns; Runtime uses this property when its lifecycle is restarted in
// tests or an embedding process.
func (w *Worker) Start(ctx context.Context) {
	if w == nil || w.store == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.wg.Add(1)
	w.mu.Unlock()

	go func() {
		defer w.wg.Done()
		defer func() {
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
		}()
		w.run(ctx)
	}()
}

func (w *Worker) Wait() {
	if w == nil {
		return
	}
	w.wg.Wait()
}

// RunOnce materializes all completed buckets and periodically applies data
// retention. It returns storage errors to direct callers; the background loop
// logs them and continues on the next boundary.
func (w *Worker) RunOnce(ctx context.Context, now time.Time) error {
	if w == nil || w.store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = w.now()
	}

	var errs []error
	if err := w.store.BuildMetricRollups(ctx, now.UTC()); err != nil {
		errs = append(errs, fmtError("build metric rollups", err))
	}
	if w.shouldPrune(now) {
		if err := w.store.PruneMetricHistory(ctx, now.UTC()); err != nil {
			errs = append(errs, fmtError("prune metric history", err))
		} else {
			w.mu.Lock()
			w.lastPruned = now
			w.mu.Unlock()
		}
	}
	return errors.Join(errs...)
}

func (w *Worker) shouldPrune(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastPruned.IsZero() || !now.Before(w.lastPruned.Add(w.pruneInterval))
}

func (w *Worker) run(ctx context.Context) {
	w.runCycle(ctx, w.now())
	for {
		now := w.now()
		delay := nextBoundary(now, w.rollupInterval).Sub(now)
		if delay <= 0 {
			delay = time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			w.runCycle(ctx, w.now())
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

func (w *Worker) runCycle(ctx context.Context, now time.Time) {
	if err := w.RunOnce(ctx, now); err != nil && !errors.Is(err, context.Canceled) {
		w.log.Warn("metrics worker cycle failed", "error", err)
	}
}

func nextBoundary(now time.Time, interval time.Duration) time.Time {
	if interval <= 0 {
		interval = DefaultRollupInterval
	}
	utc := now.UTC()
	return utc.Truncate(interval).Add(interval)
}

type wrappedError struct {
	operation string
	err       error
}

func (e wrappedError) Error() string { return e.operation + ": " + e.err.Error() }
func (e wrappedError) Unwrap() error { return e.err }

func fmtError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return wrappedError{operation: operation, err: err}
}
