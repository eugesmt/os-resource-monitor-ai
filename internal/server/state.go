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
	aiAgent     *ai.Agent
	aiHistory   ai.HistoryStore
	aiAskMu     sync.Mutex

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

func (s *State) SetAIAgent(agent *ai.Agent) { s.mu.Lock(); s.aiAgent = agent; s.mu.Unlock() }
func (s *State) SetAIHistory(history ai.HistoryStore) {
	s.mu.Lock()
	s.aiHistory = history
	s.mu.Unlock()
}

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
func (s *State) AskAI(ctx context.Context, question string, branchID ...int64) (*domain.AnalysisResponse, domain.AICallUsage, error) {
	s.mu.RLock()
	agent := s.aiAgent
	snapshot := cloneSnapshot(s.snapshot)
	ready := s.hasSnapshot
	s.mu.RUnlock()
	if agent == nil {
		return nil, domain.AICallUsage{}, ErrAIUnavailable
	}
	if !ready {
		return nil, domain.AICallUsage{}, ErrSnapshotNotReady
	}

	snapshotHistory := s.buffer.GetLast(10)
	// The collector updates the state and ring buffer in two separate steps.
	// Make the current snapshot explicit so the AI never compares against a
	// stale or skipped final buffer entry.
	if len(snapshotHistory) == 0 || snapshotHistory[len(snapshotHistory)-1].Timestamp != snapshot.Timestamp {
		snapshotHistory = append(snapshotHistory, snapshot)
		if len(snapshotHistory) > 10 {
			snapshotHistory = snapshotHistory[len(snapshotHistory)-10:]
		}
	}
	contextText := ai.BuildContext(snapshot, snapshotHistory)

	if len(branchID) > 0 && branchID[0] != 0 {
		return agent.AskOnBranch(ctx, question, contextText, branchID[0])
	}
	return agent.Ask(ctx, question, contextText)
}

// AskAIWithMessageID returns the persisted assistant message ID for UI actions
// that must target the just-completed turn, such as creating a checkpoint.
func (s *State) AskAIWithMessageID(ctx context.Context, question string, branchID ...int64) (*domain.AnalysisResponse, domain.AICallUsage, int64, error) {
	s.aiAskMu.Lock()
	defer s.aiAskMu.Unlock()

	answer, usage, err := s.AskAI(ctx, question, branchID...)
	if err != nil {
		return nil, usage, 0, err
	}
	items, err := s.GetAIHistory(ctx, usage.BranchID)
	if err != nil {
		return answer, usage, 0, err
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Role == domain.ChatRoleAssistant {
			return answer, usage, items[i].ID, nil
		}
	}
	return answer, usage, 0, nil
}

func (s *State) GetAIHistory(ctx context.Context, branchID ...int64) ([]domain.ChatMessage, error) {
	s.mu.RLock()
	agent := s.aiAgent
	history := s.aiHistory
	s.mu.RUnlock()
	if history != nil {
		if len(branchID) > 0 {
			if scoped, ok := history.(interface {
				ResolveScope(context.Context, int64) (domain.ContextScope, error)
				LoadLineage(context.Context, domain.ContextScope) ([]domain.ChatMessage, error)
			}); ok {
				scope, err := scoped.ResolveScope(ctx, branchID[0])
				if err != nil {
					return nil, err
				}
				return scoped.LoadLineage(ctx, scope)
			}
		}
		return history.Load(ctx)
	}
	if agent == nil {
		return nil, ErrAIUnavailable
	}
	return agent.History(ctx)
}

func (s *State) ContextInfo(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	agent := s.aiAgent
	history := s.aiHistory
	s.mu.RUnlock()
	if agent == nil {
		return nil, ErrAIUnavailable
	}
	out := map[string]any{"context_strategy": agent.ContextStrategy()}
	if scoped, ok := history.(interface {
		GetActiveScope(context.Context) (domain.ContextScope, error)
	}); ok {
		scope, err := scoped.GetActiveScope(ctx)
		if err != nil {
			return nil, err
		}
		out["scope"] = scope
	}
	return out, nil
}

func (s *State) BranchStore() interface{} { s.mu.RLock(); defer s.mu.RUnlock(); return s.aiHistory }
func (s *State) ContextStrategy() domain.ContextStrategyName {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.aiAgent == nil {
		return ""
	}
	return s.aiAgent.ContextStrategy()
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
