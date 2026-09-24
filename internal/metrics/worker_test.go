package metrics

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"taskmanager-ai/internal/collector"
)

type workerStore struct {
	mu sync.Mutex

	buildCalls []time.Time
	pruneCalls []time.Time
	buildErr   error
	pruneErr   error
	notified   chan struct{}
}

func (s *workerStore) SaveMetricSample(context.Context, collector.SystemSnapshot) error { return nil }

func (s *workerStore) BuildMetricRollups(_ context.Context, now time.Time) error {
	s.mu.Lock()
	s.buildCalls = append(s.buildCalls, now)
	if s.notified != nil {
		s.notified <- struct{}{}
	}
	err := s.buildErr
	s.mu.Unlock()
	return err
}

func (s *workerStore) PruneMetricHistory(_ context.Context, now time.Time) error {
	s.mu.Lock()
	s.pruneCalls = append(s.pruneCalls, now)
	err := s.pruneErr
	s.mu.Unlock()
	return err
}

func (s *workerStore) counts() (build, prune int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buildCalls), len(s.pruneCalls)
}

func TestNextBoundaryUsesUTCAlignedHalfOpenBuckets(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 4, 30, 0, time.FixedZone("MSK", 3*60*60))
	want := time.Date(2026, 9, 24, 9, 6, 0, 0, time.UTC)
	if got := nextBoundary(now, 3*time.Minute); !got.Equal(want) {
		t.Fatalf("nextBoundary() = %s, want %s", got, want)
	}
}

func TestRunOnceBuildsRollupsAndPrunesImmediately(t *testing.T) {
	store := &workerStore{}
	worker := NewWorkerWithOptions(store, slog.New(slog.DiscardHandler), WorkerOptions{
		PruneInterval: time.Hour,
	})
	now := time.Date(2026, 9, 24, 12, 4, 30, 0, time.UTC)
	if err := worker.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	build, prune := store.counts()
	if build != 1 || prune != 1 {
		t.Fatalf("store calls = build %d, prune %d; want 1, 1", build, prune)
	}

	if err := worker.RunOnce(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	build, prune = store.counts()
	if build != 2 || prune != 1 {
		t.Fatalf("store calls after same retention window = build %d, prune %d; want 2, 1", build, prune)
	}
}

func TestRunOnceRetriesFailedPrune(t *testing.T) {
	store := &workerStore{pruneErr: errors.New("database unavailable")}
	worker := NewWorker(store, nil)
	now := time.Date(2026, 9, 24, 12, 4, 30, 0, time.UTC)
	if err := worker.RunOnce(context.Background(), now); err == nil || !errors.Is(err, store.pruneErr) {
		t.Fatalf("RunOnce() error = %v, want prune error", err)
	}
	if err := worker.RunOnce(context.Background(), now.Add(time.Minute)); err == nil || !errors.Is(err, store.pruneErr) {
		t.Fatalf("second RunOnce() error = %v, want retry prune error", err)
	}
	_, prune := store.counts()
	if prune != 2 {
		t.Fatalf("prune calls = %d, want 2", prune)
	}
}

func TestWorkerRunsImmediatelyAndStopsWithContext(t *testing.T) {
	store := &workerStore{notified: make(chan struct{}, 8)}
	worker := NewWorkerWithOptions(store, nil, WorkerOptions{
		RollupInterval: 10 * time.Millisecond,
		PruneInterval:  time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	select {
	case <-store.notified:
	case <-time.After(time.Second):
		cancel()
		worker.Wait()
		t.Fatal("worker did not run immediately")
	}
	select {
	case <-store.notified:
	case <-time.After(time.Second):
		cancel()
		worker.Wait()
		t.Fatal("worker did not run at the next aligned boundary")
	}
	cancel()
	worker.Wait()
	build, _ := store.counts()
	if build < 2 {
		t.Fatalf("build calls = %d, want at least 2", build)
	}
}

func TestWorkerContinuesAfterStorageError(t *testing.T) {
	store := &workerStore{buildErr: errors.New("temporary database error"), notified: make(chan struct{}, 8)}
	worker := NewWorkerWithOptions(store, nil, WorkerOptions{RollupInterval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	for i := 0; i < 2; i++ {
		select {
		case <-store.notified:
		case <-time.After(time.Second):
			cancel()
			worker.Wait()
			t.Fatal("worker stopped after storage error")
		}
	}
	cancel()
	worker.Wait()
	build, _ := store.counts()
	if build < 2 {
		t.Fatalf("build calls = %d, want at least 2", build)
	}
}
