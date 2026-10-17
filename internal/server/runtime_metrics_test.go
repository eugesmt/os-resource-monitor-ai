package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"system-monitor-ai/internal/collector"
	"system-monitor-ai/internal/storage"
)

type runtimeMetricsStore struct {
	mu      sync.Mutex
	saved   int
	saveErr error
	rollups int
	prunes  int
}

func (s *runtimeMetricsStore) SaveMetricSample(context.Context, collector.SystemSnapshot) error {
	s.mu.Lock()
	s.saved++
	err := s.saveErr
	s.mu.Unlock()
	return err
}

func (s *runtimeMetricsStore) BuildMetricRollups(context.Context, time.Time) error {
	s.mu.Lock()
	s.rollups++
	s.mu.Unlock()
	return nil
}

func (s *runtimeMetricsStore) PruneMetricHistory(context.Context, time.Time) error {
	s.mu.Lock()
	s.prunes++
	s.mu.Unlock()
	return nil
}

func (s *runtimeMetricsStore) savedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved
}

func TestRuntimeStorageFailureDoesNotPreventSnapshotHistory(t *testing.T) {
	store := &runtimeMetricsStore{saveErr: errors.New("metrics database unavailable")}
	runtime := NewRuntime(collector.New(1, nil), storage.NewRingBuffer(2), nil)
	runtime.SetMetricsStore(store)
	ctx, cancel := context.WithCancel(context.Background())
	runtime.Start(ctx, time.Hour)
	defer func() {
		cancel()
		runtime.Stop()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := runtime.GetSnapshot(); err == nil && len(runtime.GetHistory(2)) > 0 {
			if store.savedCount() == 0 {
				t.Fatal("snapshot was published without attempting persistence")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("collector did not publish a snapshot")
}
