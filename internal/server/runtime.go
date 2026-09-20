package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"taskmanager-ai/internal/ai"
	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/memory"
	"taskmanager-ai/internal/storage"
)

var ErrSnapshotNotReady = errors.New("snapshot is not ready")
var ErrAIUnavailable = errors.New("AI is not configured: set DEEPSEEK_API_KEY")

const maxSnapshotAge = 10 * time.Second

type Runtime struct {
	collector *collector.Collector
	buffer    *storage.RingBuffer
	log       *slog.Logger

	state       State
	agentMu     sync.RWMutex
	aiAgent     *ai.Agent
	history     ai.HistoryStore
	memory      *memory.Model
	memoryStore memoryStore
	profiles    profileStore
	askMu       sync.Mutex

	subscriberMu sync.Mutex
	subscribers  map[chan collector.SystemSnapshot]struct{}
	startMu      sync.Mutex
	cancel       context.CancelFunc
}

type memoryStore interface {
	SaveMemory(context.Context, memory.Record) error
	ListMemory(context.Context) ([]memory.Record, error)
	DeleteMemory(context.Context, string) error
}

type profileStore interface {
	SaveProfile(context.Context, memory.Profile) error
	ListProfiles(context.Context) ([]memory.Profile, error)
}

// State хранит только текущее состояние мониторинга приложения.
type State struct {
	mu          sync.RWMutex
	snapshot    collector.SystemSnapshot
	hasSnapshot bool
}

func NewRuntime(c *collector.Collector, buffer *storage.RingBuffer, logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{collector: c, buffer: buffer, log: logger, memory: memory.NewModel(), subscribers: make(map[chan collector.SystemSnapshot]struct{})}
}

func (s *Runtime) SetAIAgent(agent *ai.Agent) {
	s.agentMu.Lock()
	s.aiAgent = agent
	if agent != nil {
		agent.SetMemoryModel(s.memory)
		agent.SetConversation(s.ConversationID())
	}
	s.agentMu.Unlock()
}

func (s *Runtime) MemoryModel() *memory.Model { return s.memory }

func (s *Runtime) SetProfile(ctx context.Context, profile memory.Profile) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	s.askMu.Lock()
	defer s.askMu.Unlock()
	if s.profiles != nil {
		if err := s.profiles.SaveProfile(ctx, profile); err != nil {
			return err
		}
	}
	if err := s.memory.SetProfile(profile); err != nil {
		return err
	}
	s.agentMu.RLock()
	agent := s.aiAgent
	s.agentMu.RUnlock()
	if agent != nil {
		agent.SetConversation(s.ConversationID())
	}
	return nil
}

func (s *Runtime) Profile() memory.Profile {
	return s.memory.Profile()
}

func (s *Runtime) SetProfileStore(store profileStore) {
	s.profiles = store
}

func (s *Runtime) Profiles(ctx context.Context) ([]memory.Profile, error) {
	if s.profiles == nil {
		return []memory.Profile{}, nil
	}
	return s.profiles.ListProfiles(ctx)
}

func (s *Runtime) ConversationID() string {
	if userID := s.memory.Profile().UserID; userID != "" {
		return "profile:" + userID
	}
	return storage.DefaultConversationID
}

func (s *Runtime) SetMemoryStore(ctx context.Context, store memoryStore) error {
	s.memoryStore = store
	if store == nil {
		return nil
	}
	records, err := store.ListMemory(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := s.memory.Add(record); err != nil {
			return err
		}
	}
	return nil
}

func (s *Runtime) SaveMemory(ctx context.Context, record memory.Record) error {
	if s.memoryStore == nil {
		return nil
	}
	return s.memoryStore.SaveMemory(ctx, record)
}

func (s *Runtime) DeleteMemory(ctx context.Context, id string) error {
	if s.memoryStore != nil {
		if err := s.memoryStore.DeleteMemory(ctx, id); err != nil {
			return err
		}
	}
	s.memory.Remove(id)
	return nil
}

func (s *Runtime) SetHistoryStore(store ai.HistoryStore) {
	s.agentMu.Lock()
	s.history = store
	s.agentMu.Unlock()
}

func (s *Runtime) Start(parent context.Context, interval time.Duration) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.collector.Start(ctx, interval, func(snapshot collector.SystemSnapshot) {
		s.state.mu.Lock()
		s.state.snapshot = cloneSnapshot(snapshot)
		s.state.hasSnapshot = true
		s.state.mu.Unlock()
		s.buffer.Add(snapshot)
		s.broadcast(snapshot)
	})
}

func (s *Runtime) Stop() {
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

func (s *Runtime) GetSnapshot() (collector.SystemSnapshot, error) {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	if !s.state.hasSnapshot {
		return collector.SystemSnapshot{}, ErrSnapshotNotReady
	}
	return cloneSnapshot(s.state.snapshot), nil
}
func (s *Runtime) GetHistory(limit int) []collector.SystemSnapshot {
	if limit <= 0 || limit > 300 {
		limit = 300
	}
	return s.buffer.GetLast(limit)
}

func (s *Runtime) GetConversationHistory(ctx context.Context) ([]domain.ConversationEvent, error) {
	s.agentMu.RLock()
	store := s.history
	s.agentMu.RUnlock()
	if store == nil {
		return nil, errors.New("conversation history unavailable")
	}
	return store.ListEvents(ctx, s.ConversationID())
}
func (s *Runtime) AskAI(ctx context.Context, question string) (*domain.AnalysisResponse, error) {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return nil, errors.New("question exceeds 4000 characters")
	}
	s.agentMu.RLock()
	agent := s.aiAgent
	s.agentMu.RUnlock()
	s.state.mu.RLock()
	snapshot := cloneSnapshot(s.state.snapshot)
	ready := s.state.hasSnapshot
	s.state.mu.RUnlock()
	if agent == nil {
		return nil, ErrAIUnavailable
	}
	if !ready {
		return nil, ErrSnapshotNotReady
	}
	capturedAt := snapshot.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = snapshot.Timestamp
	}
	if capturedAt.IsZero() || time.Since(capturedAt) > maxSnapshotAge {
		return nil, fmt.Errorf("%w: snapshot is stale", ErrSnapshotNotReady)
	}

	return agent.AskWithSnapshot(ctx, question, snapshot)
}

func (s *Runtime) Subscribe() (<-chan collector.SystemSnapshot, func()) {
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
func (s *Runtime) broadcast(snapshot collector.SystemSnapshot) {
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
	s.CPUPerCore = slices.Clone(s.CPUPerCore)
	s.Disks = slices.Clone(s.Disks)
	s.GPU = slices.Clone(s.GPU)
	s.Processes = slices.Clone(s.Processes)
	s.MissingData = slices.Clone(s.MissingData)
	return s
}
