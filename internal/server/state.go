package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"taskmanager-ai/internal/ai"
	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/storage"
)

var ErrSnapshotNotReady = errors.New("snapshot is not ready")
var ErrAIUnavailable = errors.New("AI is not configured: set DEEPSEEK_API_KEY")

type State struct {
	collector *collector.Collector
	buffer    *storage.RingBuffer
	log       *slog.Logger

	mu          sync.RWMutex
	snapshot    collector.SystemSnapshot
	hasSnapshot bool
	aiClient    *ai.Client

	subscriberMu sync.Mutex
	subscribers  map[chan collector.SystemSnapshot]struct{}
	startMu      sync.Mutex
	cancel       context.CancelFunc
}

func NewState(c *collector.Collector, buffer *storage.RingBuffer, logger *slog.Logger) *State {
	if logger == nil {
		logger = slog.Default()
	}
	return &State{collector: c, buffer: buffer, log: logger, subscribers: make(map[chan collector.SystemSnapshot]struct{})}
}

func (s *State) SetAIClient(client *ai.Client) { s.mu.Lock(); s.aiClient = client; s.mu.Unlock() }

func (s *State) Start(parent context.Context, interval time.Duration) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.collector.Start(ctx, interval, func(snapshot collector.SystemSnapshot) {
		s.mu.Lock()
		s.snapshot = cloneSnapshot(snapshot)
		s.hasSnapshot = true
		s.mu.Unlock()
		s.buffer.Add(snapshot)
		s.broadcast(snapshot)
	})
}

func (s *State) Stop() {
	s.startMu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.startMu.Unlock()
	if cancel != nil {
		cancel()
		s.collector.Wait()
	}
	s.subscriberMu.Lock()
	for ch := range s.subscribers {
		close(ch)
	}
	s.subscribers = make(map[chan collector.SystemSnapshot]struct{})
	s.subscriberMu.Unlock()
}

func (s *State) GetSnapshot() (collector.SystemSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.hasSnapshot {
		return collector.SystemSnapshot{}, ErrSnapshotNotReady
	}
	return cloneSnapshot(s.snapshot), nil
}
func (s *State) GetHistory(limit int) []collector.SystemSnapshot {
	if limit <= 0 || limit > 300 {
		limit = 300
	}
	return s.buffer.GetLast(limit)
}
func (s *State) AskAI(ctx context.Context, question string) (*domain.AnalysisResponse, error) {
	s.mu.RLock()
	client := s.aiClient
	snapshot := cloneSnapshot(s.snapshot)
	ready := s.hasSnapshot
	s.mu.RUnlock()
	if client == nil {
		return nil, ErrAIUnavailable
	}
	if !ready {
		return nil, ErrSnapshotNotReady
	}

	return client.Ask(ctx, question, ai.BuildContext(snapshot, s.buffer.GetLast(10)))
}

func (s *State) Subscribe() (<-chan collector.SystemSnapshot, func()) {
	ch := make(chan collector.SystemSnapshot, 2)
	s.subscriberMu.Lock()
	s.subscribers[ch] = struct{}{}
	s.subscriberMu.Unlock()
	return ch, func() {
		s.subscriberMu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.subscriberMu.Unlock()
	}
}
func (s *State) broadcast(snapshot collector.SystemSnapshot) {
	s.subscriberMu.Lock()
	defer s.subscriberMu.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- cloneSnapshot(snapshot):
		default:
		}
	}
}
func cloneSnapshot(s collector.SystemSnapshot) collector.SystemSnapshot {
	s.Disks = append([]collector.DiskInfo(nil), s.Disks...)
	s.Processes = append([]collector.ProcessInfo(nil), s.Processes...)
	return s
}
