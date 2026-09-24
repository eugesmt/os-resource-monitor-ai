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
	"taskmanager-ai/internal/invariants"
	"taskmanager-ai/internal/memory"
	"taskmanager-ai/internal/metrics"
	"taskmanager-ai/internal/storage"
)

var ErrSnapshotNotReady = errors.New("snapshot is not ready")
var ErrAIUnavailable = errors.New("AI is not configured: set DEEPSEEK_API_KEY")
var ErrTaskAlreadyOpen = errors.New("an open task already exists")
var ErrTaskNotRunnable = errors.New("task is not runnable")

const maxSnapshotAge = 10 * time.Second
const maxAutomaticTaskTurns = 8
const automaticTaskTurnDelay = 750 * time.Millisecond
const maxAutomaticTaskRetries = 1
const metricSampleWriteTimeout = 2 * time.Second

type Runtime struct {
	collector           *collector.Collector
	buffer              *storage.RingBuffer
	log                 *slog.Logger
	metricsStore        metrics.Store
	metricsSummaryStore interface {
		MetricsSummary(context.Context, time.Time, time.Time) (metrics.Summary, error)
	}
	metricsWorker *metrics.Worker

	state            State
	agentMu          sync.RWMutex
	aiAgent          *ai.Agent
	history          ai.HistoryStore
	memory           *memory.Model
	memoryStore      memoryStore
	profiles         profileStore
	invariantStore   invariantStore
	invariants       *invariants.Model
	tasks            taskStateStore
	askMu            sync.Mutex
	taskStateMu      sync.RWMutex
	taskState        domain.TaskState
	taskLoaded       bool
	taskConversation string
	runnerMu         sync.Mutex
	runningTasks     map[string]struct{}

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

type invariantStore interface {
	ReplaceInvariants(context.Context, []invariants.Invariant) error
	ListInvariants(context.Context) ([]invariants.Invariant, error)
	DeleteInvariant(context.Context, string) error
}

type taskStateStore interface {
	CreateTask(context.Context, string) (domain.TaskState, error)
	GetOpenTask(context.Context, string) (domain.TaskState, error)
	GetLatestTask(context.Context, string) (domain.TaskState, error)
	GetTask(context.Context, string) (domain.TaskState, error)
	ListTaskTransitions(context.Context, string) ([]domain.TaskTransition, error)
	SaveTaskTransition(context.Context, string, string, domain.TaskState, domain.TaskState, int64) (domain.TaskState, error)
	CommitAssistantAndTask(context.Context, string, string, string, string, string, string, domain.TaskState, domain.TaskState) (domain.ConversationEvent, domain.TaskState, error)
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
	return &Runtime{collector: c, buffer: buffer, log: logger, memory: memory.NewModel(), invariants: invariants.NewModel(), subscribers: make(map[chan collector.SystemSnapshot]struct{}), runningTasks: make(map[string]struct{})}
}

func (s *Runtime) SetAIAgent(agent *ai.Agent) {
	s.agentMu.Lock()
	s.aiAgent = agent
	if agent != nil {
		agent.SetMemoryModel(s.memory)
		agent.SetInvariantModel(s.invariants)
		agent.SetConversation(s.ConversationID())
		if s.tasks != nil {
			agent.SetTaskCommitter(s.tasks)
		}
		if state, err := s.TaskState(context.Background()); err == nil {
			agent.SetTaskState(&state)
		}
	}
	s.agentMu.Unlock()
}

func (s *Runtime) SetInvariantStore(ctx context.Context, store invariantStore) error {
	s.invariantStore = store
	if store == nil {
		return nil
	}
	items, err := store.ListInvariants(ctx)
	if err != nil {
		return err
	}
	return s.invariants.Replace(items)
}

func (s *Runtime) Invariants() []invariants.Invariant { return s.invariants.All() }

func (s *Runtime) ReplaceInvariants(ctx context.Context, items []invariants.Invariant) error {
	if err := invariants.NewModel().Replace(items); err != nil {
		return err
	}
	s.askMu.Lock()
	defer s.askMu.Unlock()
	if s.invariantStore != nil {
		if err := s.invariantStore.ReplaceInvariants(ctx, items); err != nil {
			return err
		}
	}
	return s.invariants.Replace(items)
}

func (s *Runtime) DeleteInvariant(ctx context.Context, id string) error {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	if s.invariantStore != nil {
		if err := s.invariantStore.DeleteInvariant(ctx, id); err != nil {
			return err
		}
	}
	return s.invariants.Delete(id)
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

func (s *Runtime) SetTaskStateStore(store taskStateStore) {
	s.tasks = store
	s.agentMu.RLock()
	agent := s.aiAgent
	s.agentMu.RUnlock()
	if agent != nil {
		agent.SetTaskCommitter(store)
	}
}

func (s *Runtime) TaskState(ctx context.Context) (domain.TaskState, error) {
	return s.loadTaskState(ctx, false)
}

func (s *Runtime) ensureTaskState(ctx context.Context) (domain.TaskState, error) {
	return s.loadTaskState(ctx, true)
}

func (s *Runtime) loadTaskState(ctx context.Context, create bool) (domain.TaskState, error) {
	conversationID := s.ConversationID()
	s.taskStateMu.RLock()
	loaded := s.taskLoaded && s.taskConversation == conversationID
	state := s.taskState
	s.taskStateMu.RUnlock()
	if loaded {
		if create && state.Phase == domain.TaskPhaseDone {
			return s.createTask(ctx, conversationID)
		}
		return state, nil
	}
	if s.tasks != nil {
		loadedState, err := s.tasks.GetOpenTask(ctx, conversationID)
		if err == nil {
			s.taskStateMu.Lock()
			s.taskState, s.taskLoaded, s.taskConversation = loadedState, true, conversationID
			s.taskStateMu.Unlock()
			return loadedState, nil
		}
		if !errors.Is(err, storage.ErrTaskNotFound) {
			return domain.TaskState{}, err
		}
		loadedState, err = s.tasks.GetLatestTask(ctx, conversationID)
		if err == nil {
			if create && loadedState.Phase == domain.TaskPhaseDone {
				return s.createTask(ctx, conversationID)
			}
			s.taskStateMu.Lock()
			s.taskState, s.taskLoaded, s.taskConversation = loadedState, true, conversationID
			s.taskStateMu.Unlock()
			return loadedState, nil
		}
		if !errors.Is(err, storage.ErrTaskNotFound) {
			return domain.TaskState{}, err
		}
		if !create {
			return domain.TaskState{}, storage.ErrTaskNotFound
		}
		return s.createTask(ctx, conversationID)
	}
	if !create {
		return domain.TaskState{}, storage.ErrTaskNotFound
	}
	state = domain.NewTaskState("task-memory-" + conversationID)
	s.taskStateMu.Lock()
	s.taskState, s.taskLoaded, s.taskConversation = state, true, conversationID
	s.taskStateMu.Unlock()
	return state, nil
}

func (s *Runtime) createTask(ctx context.Context, conversationID string) (domain.TaskState, error) {
	if s.tasks == nil {
		state := domain.NewTaskState("task-memory-" + conversationID)
		s.taskStateMu.Lock()
		s.taskState, s.taskLoaded, s.taskConversation = state, true, conversationID
		s.taskStateMu.Unlock()
		return state, nil
	}
	state, err := s.tasks.CreateTask(ctx, conversationID)
	if err != nil {
		return domain.TaskState{}, err
	}
	s.taskStateMu.Lock()
	s.taskState, s.taskLoaded, s.taskConversation = state, true, conversationID
	s.taskStateMu.Unlock()
	return state, nil
}

func (s *Runtime) UpdateTaskState(ctx context.Context, taskID string, expectedVersion int64, action domain.TaskStateAction) (domain.TaskState, error) {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	current, err := s.TaskState(ctx)
	if err != nil {
		return domain.TaskState{}, err
	}
	if current.TaskID != taskID {
		return domain.TaskState{}, storage.ErrTaskNotFound
	}
	if current.Version != expectedVersion {
		return domain.TaskState{}, storage.ErrTaskStateVersionConflict
	}
	next, err := current.Apply(action, "", "", "")
	if err != nil {
		return domain.TaskState{}, err
	}
	if s.tasks != nil {
		next, err = s.tasks.SaveTaskTransition(ctx, current.TaskID, string(action), current, next, current.Version)
		if err != nil {
			return domain.TaskState{}, err
		}
	} else {
		next.Version = current.Version + 1
		next.UpdatedAt = time.Now().UTC()
	}
	s.taskStateMu.Lock()
	s.taskState, s.taskLoaded, s.taskConversation = next, true, s.ConversationID()
	s.taskStateMu.Unlock()
	s.agentMu.RLock()
	agent := s.aiAgent
	s.agentMu.RUnlock()
	if agent != nil {
		agent.SetTaskState(&next)
	}
	if action == domain.TaskActionResume {
		s.scheduleTask(next.TaskID, "")
	}
	return next, nil
}

func (s *Runtime) ApplyTaskDecision(ctx context.Context, decision domain.TaskDecision) (domain.TaskState, error) {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	return s.applyTaskDecision(ctx, decision)
}

func (s *Runtime) StartTask(ctx context.Context) (domain.TaskState, error) {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	conversationID := s.ConversationID()
	if s.tasks == nil {
		state := domain.NewTaskState("task-memory-" + conversationID)
		s.taskStateMu.Lock()
		s.taskState, s.taskLoaded, s.taskConversation = state, true, conversationID
		s.taskStateMu.Unlock()
		return state, nil
	}
	if _, err := s.tasks.GetOpenTask(ctx, conversationID); err == nil {
		return domain.TaskState{}, ErrTaskAlreadyOpen
	} else if !errors.Is(err, storage.ErrTaskNotFound) {
		return domain.TaskState{}, err
	}
	return s.createTask(ctx, conversationID)
}

// StartTaskRun creates a task and lets the runner perform the saved agent checkpoints.
// A caller receives the initial state immediately and can pause or cancel while it runs.
func (s *Runtime) StartTaskRun(ctx context.Context, question string) (domain.TaskState, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return domain.TaskState{}, errors.New("question is empty")
	}
	s.askMu.Lock()
	conversationID := s.ConversationID()
	state, err := s.TaskState(ctx)
	if err == nil && state.Phase != domain.TaskPhaseDone {
		s.askMu.Unlock()
		return domain.TaskState{}, ErrTaskAlreadyOpen
	}
	if err != nil && !errors.Is(err, storage.ErrTaskNotFound) {
		s.askMu.Unlock()
		return domain.TaskState{}, err
	}
	state, err = s.createTask(ctx, conversationID)
	s.askMu.Unlock()
	if err != nil {
		return domain.TaskState{}, err
	}
	state, err = s.setTaskGoal(state, question)
	if err != nil {
		return domain.TaskState{}, err
	}
	s.scheduleTask(state.TaskID, question)
	return state, nil
}

func (s *Runtime) setTaskGoal(current domain.TaskState, goal string) (domain.TaskState, error) {
	next := current
	next.Goal = goal
	next.Step = "Подготовить ответ на запрос"
	next.ExpectedAction = "Определить тип ответа и подготовить данные"
	if err := next.Validate(); err != nil {
		return domain.TaskState{}, err
	}
	if s.tasks != nil {
		updated, err := s.tasks.SaveTaskTransition(context.Background(), current.TaskID, "start", current, next, current.Version)
		if err != nil {
			return domain.TaskState{}, err
		}
		next = updated
	} else {
		next.Version, next.UpdatedAt = current.Version+1, time.Now().UTC()
	}
	s.taskStateMu.Lock()
	s.taskState, s.taskLoaded, s.taskConversation = next, true, s.ConversationID()
	s.taskStateMu.Unlock()
	return next, nil
}

func (s *Runtime) scheduleTask(taskID, firstQuestion string) {
	s.runnerMu.Lock()
	if _, running := s.runningTasks[taskID]; running {
		s.runnerMu.Unlock()
		return
	}
	s.runningTasks[taskID] = struct{}{}
	s.runnerMu.Unlock()
	go func() {
		defer func() {
			s.runnerMu.Lock()
			delete(s.runningTasks, taskID)
			s.runnerMu.Unlock()
		}()
		question := firstQuestion
		for turn := 0; turn < maxAutomaticTaskTurns; turn++ {
			time.Sleep(automaticTaskTurnDelay)
			state, err := s.TaskState(context.Background())
			if err != nil || state.TaskID != taskID || state.Phase == domain.TaskPhaseDone || state.Status == domain.TaskStatusPaused {
				return
			}
			switch state.Phase {
			case domain.TaskPhasePlanning:
				if question == "" {
					question = state.Goal
				}
				ctx, cancel := context.WithTimeout(context.Background(), ai.RequestTimeout)
				result, askErr := s.askAI(ctx, question, taskID, state.Version)
				cancel()
				if askErr != nil {
					s.log.Error("task response failed", "task_id", taskID, "error", askErr)
					s.failTask(taskID, state.Version, "Не удалось получить валидный ответ модели. Нажмите «Возобновить», чтобы повторить задачу.")
					return
				}
				step, action := responseTypeCheckpoint(result.ResponseType)
				if err := s.advanceTask(taskID, state.Version, domain.TaskPhaseExecution, step, action, result.ResponseType); err != nil {
					s.log.Error("task transition failed", "task_id", taskID, "error", err)
					return
				}
			case domain.TaskPhaseExecution:
				if s.advanceTask(taskID, state.Version, domain.TaskPhaseValidation, "Проверить ответ по исходным данным", "Сохранить проверенный результат", state.ResponseType) != nil {
					return
				}
			case domain.TaskPhaseValidation:
				if s.completeTask(taskID, state.Version) != nil {
					return
				}
			}
		}
		s.log.Error("automatic task runner stopped after turn limit", "task_id", taskID, "limit", maxAutomaticTaskTurns)
	}()
}

func responseTypeCheckpoint(responseType domain.ResponseType) (string, string) {
	switch responseType {
	case domain.ResponseTypeConversationRecall:
		return "Сверить ответ с историей диалога", "Проверить ссылки на события истории"
	case domain.ResponseTypeMetricComparison:
		return "Сопоставить метрики с историческими точками", "Проверить источники и единицы измерения"
	case domain.ResponseTypeGeneralAnswer:
		return "Проверить ответ по контексту запроса", "Сохранить итоговый ответ"
	case domain.ResponseTypeInvariantConflict:
		return "Зафиксировать конфликт с инвариантом", "Показать отказ и затронутые правила"
	default:
		return "Сопоставить вывод с текущим snapshot", "Проверить метрики и выводы"
	}
}

func (s *Runtime) advanceTask(taskID string, expectedVersion int64, phase domain.TaskPhase, step, expectedAction string, responseType domain.ResponseType) error {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	current, err := s.TaskState(context.Background())
	if err != nil || current.TaskID != taskID || current.Version != expectedVersion || current.Status != domain.TaskStatusActive {
		return ErrTaskNotRunnable
	}
	next, err := current.Apply(domain.TaskActionAdvance, phase, step, expectedAction)
	if err != nil {
		return err
	}
	next.ResponseType = responseType
	return s.persistCodeTransition(current, next, "advance")
}

func (s *Runtime) completeTask(taskID string, expectedVersion int64) error {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	current, err := s.TaskState(context.Background())
	if err != nil || current.TaskID != taskID || current.Version != expectedVersion || current.Status != domain.TaskStatusActive {
		return ErrTaskNotRunnable
	}
	next, err := current.ApplyDecision(domain.TaskDecision{Action: domain.TaskDecisionComplete, Phase: domain.TaskPhaseDone, ExpectedActionKind: domain.TaskActionKindNone})
	if err != nil {
		return err
	}
	return s.persistCodeTransition(current, next, "complete")
}

func (s *Runtime) persistCodeTransition(current, next domain.TaskState, action string) error {
	var err error
	if s.tasks != nil {
		next, err = s.tasks.SaveTaskTransition(context.Background(), current.TaskID, action, current, next, current.Version)
		if err != nil {
			return err
		}
	} else {
		next.Version, next.UpdatedAt = current.Version+1, time.Now().UTC()
	}
	s.taskStateMu.Lock()
	s.taskState, s.taskLoaded, s.taskConversation = next, true, s.ConversationID()
	s.taskStateMu.Unlock()
	return nil
}

func (s *Runtime) failTask(taskID string, expectedVersion int64, message string) {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	current, err := s.TaskState(context.Background())
	if err != nil || current.TaskID != taskID || current.Version != expectedVersion || current.Status != domain.TaskStatusActive {
		return
	}
	next, err := current.Apply(domain.TaskActionFail, "", "", message)
	if err != nil {
		return
	}
	if s.tasks != nil {
		next, err = s.tasks.SaveTaskTransition(context.Background(), current.TaskID, string(domain.TaskActionFail), current, next, current.Version)
		if err != nil {
			return
		}
	} else {
		next.Version, next.UpdatedAt = current.Version+1, time.Now().UTC()
	}
	s.taskStateMu.Lock()
	s.taskState, s.taskLoaded, s.taskConversation = next, true, s.ConversationID()
	s.taskStateMu.Unlock()
}

func (s *Runtime) applyTaskDecision(ctx context.Context, decision domain.TaskDecision) (domain.TaskState, error) {
	current, err := s.TaskState(ctx)
	if err != nil {
		return domain.TaskState{}, err
	}
	next, err := current.ApplyDecision(decision)
	if err != nil {
		return domain.TaskState{}, err
	}
	if s.tasks != nil {
		next, err = s.tasks.SaveTaskTransition(ctx, current.TaskID, string(decision.Action), current, next, current.Version)
		if err != nil {
			return domain.TaskState{}, err
		}
	}
	s.taskStateMu.Lock()
	s.taskState, s.taskLoaded, s.taskConversation = next, true, s.ConversationID()
	s.taskStateMu.Unlock()
	return next, nil
}

type TaskPausedError struct{ State domain.TaskState }

func (e *TaskPausedError) Error() string { return "task is paused" }

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

func (s *Runtime) SetMetricsStore(store metrics.Store) {
	s.startMu.Lock()
	s.metricsStore = store
	s.metricsSummaryStore, _ = store.(interface {
		MetricsSummary(context.Context, time.Time, time.Time) (metrics.Summary, error)
	})
	s.startMu.Unlock()
}

func (s *Runtime) MetricsSummary(ctx context.Context, from, to time.Time) (metrics.Summary, error) {
	s.startMu.Lock()
	store := s.metricsSummaryStore
	s.startMu.Unlock()
	if store == nil {
		return metrics.Summary{}, errors.New("metrics store unavailable")
	}
	return store.MetricsSummary(ctx, from, to)
}

func (s *Runtime) Start(parent context.Context, interval time.Duration) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	metricsStore := s.metricsStore
	if metricsStore != nil {
		s.metricsWorker = metrics.NewWorker(metricsStore, s.log)
		s.metricsWorker.Start(ctx)
	}
	s.collector.Start(ctx, interval, func(snapshot collector.SystemSnapshot) {
		s.state.mu.Lock()
		s.state.snapshot = cloneSnapshot(snapshot)
		s.state.hasSnapshot = true
		s.state.mu.Unlock()
		s.buffer.Add(snapshot)
		s.broadcast(snapshot)
		if metricsStore != nil {
			writeCtx, writeCancel := context.WithTimeout(ctx, metricSampleWriteTimeout)
			err := metricsStore.SaveMetricSample(writeCtx, snapshot)
			writeCancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				s.log.Warn("metric sample persistence failed", "error", err, "snapshot_id", snapshot.SnapshotID)
			}
		}
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
		if s.metricsWorker != nil {
			s.metricsWorker.Wait()
		}
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
	return s.askAI(ctx, question, "", 0)
}

func (s *Runtime) ContinueTask(ctx context.Context, taskID string, expectedVersion int64) (*domain.AnalysisResponse, error) {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	return s.askAI(ctx, "Продолжай выполнение задачи мониторинга системных метрик согласно сохраненному checkpoint.", taskID, expectedVersion)
}

func (s *Runtime) CancelTask(ctx context.Context, taskID string, expectedVersion int64) (domain.TaskState, error) {
	// Cancellation is a user intent, not an edit of a checkpoint. A background
	// transition may have advanced the version between rendering and the click,
	// so cancel the latest durable state for this task.
	s.askMu.Lock()
	defer s.askMu.Unlock()
	current, err := s.TaskState(ctx)
	if err != nil {
		return domain.TaskState{}, err
	}
	if current.TaskID != taskID {
		return domain.TaskState{}, storage.ErrTaskNotFound
	}
	if current.Phase == domain.TaskPhaseDone {
		return current, nil
	}
	next, err := current.Apply(domain.TaskActionCancel, "", "", "")
	if err != nil {
		return domain.TaskState{}, err
	}
	if s.tasks != nil {
		next, err = s.tasks.SaveTaskTransition(ctx, current.TaskID, string(domain.TaskActionCancel), current, next, current.Version)
		if err != nil {
			return domain.TaskState{}, err
		}
	} else {
		next.Version, next.UpdatedAt = current.Version+1, time.Now().UTC()
	}
	s.taskStateMu.Lock()
	s.taskState, s.taskLoaded, s.taskConversation = next, true, s.ConversationID()
	s.taskStateMu.Unlock()
	s.agentMu.RLock()
	agent := s.aiAgent
	s.agentMu.RUnlock()
	if agent != nil {
		agent.SetTaskState(&next)
	}
	return next, nil
}

func (s *Runtime) TaskTransitions(ctx context.Context, taskID string) ([]domain.TaskTransition, error) {
	state, err := s.TaskState(ctx)
	if err != nil {
		return nil, err
	}
	if state.TaskID != taskID {
		return nil, storage.ErrTaskNotFound
	}
	if s.tasks == nil {
		return []domain.TaskTransition{}, nil
	}
	return s.tasks.ListTaskTransitions(ctx, taskID)
}

func (s *Runtime) askAI(ctx context.Context, question, expectedTaskID string, expectedVersion int64) (*domain.AnalysisResponse, error) {
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
	var taskState domain.TaskState
	var err error
	if expectedTaskID == "" {
		taskState, err = s.ensureTaskState(ctx)
	} else {
		taskState, err = s.TaskState(ctx)
	}
	if err != nil {
		return nil, err
	}
	if expectedTaskID != "" && (taskState.TaskID != expectedTaskID || taskState.Version != expectedVersion || taskState.Phase == domain.TaskPhaseDone) {
		if taskState.TaskID == expectedTaskID && taskState.Version != expectedVersion {
			return nil, storage.ErrTaskStateVersionConflict
		}
		return nil, ErrTaskNotRunnable
	}
	if taskState.Status == domain.TaskStatusPaused {
		return nil, &TaskPausedError{State: taskState}
	}
	if agent != nil {
		agent.SetTaskState(&taskState)
	}
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

	var result *domain.AnalysisResponse
	if expectedTaskID != "" && taskState.Phase != domain.TaskPhasePlanning {
		result, err = agent.ContinueWithSnapshot(ctx, question, snapshot)
	} else {
		result, err = agent.AskWithSnapshot(ctx, question, snapshot)
	}
	if err != nil {
		return nil, err
	}
	if s.tasks != nil {
		updated, refreshErr := s.tasks.GetTask(ctx, taskState.TaskID)
		if refreshErr != nil {
			return nil, refreshErr
		}
		s.taskStateMu.Lock()
		s.taskState, s.taskLoaded, s.taskConversation = updated, true, s.ConversationID()
		s.taskStateMu.Unlock()
	}
	return result, nil
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
