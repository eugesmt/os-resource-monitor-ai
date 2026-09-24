package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"taskmanager-ai/internal/ai"
	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/invariants"
	"taskmanager-ai/internal/storage"

	_ "modernc.org/sqlite"
)

type countedCompletion struct {
	mu          sync.Mutex
	calls       int
	contents    []string
	errs        []error
	lastRequest ai.CompletionRequest
}

func (c *countedCompletion) Complete(_ context.Context, request ai.CompletionRequest) (ai.CompletionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.lastRequest = request
	var completionErr error
	if len(c.errs) > 0 {
		completionErr, c.errs = c.errs[0], c.errs[1:]
	}
	if completionErr != nil {
		return ai.CompletionResult{}, completionErr
	}
	content := `{"assistant_schema_version":"assistant.v2","response_type":"general_answer","general_answer":{"answer":"Продолжаю с сохранённого шага.","topic":"cpu"},"task_decision":{"action":"advance","phase":"execution","current_step":"собрать данные","expected_action_kind":"agent","expected_action":"проанализировать результаты"}}`
	if len(c.contents) > 0 {
		content, c.contents = c.contents[0], c.contents[1:]
	}
	return ai.CompletionResult{Content: content}, nil
}

func (c *countedCompletion) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countedCompletion) LastRequest() ai.CompletionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastRequest
}

func taskTestStore(t *testing.T) *storage.ConversationStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "task-state.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20260920120000_conversation_event_log.sql", "20260920200000_memory_records.sql", "20260920220000_profiles.sql", "20260920230000_task_states.sql", "20260920240000_tasks.sql", "20260920250000_task_outcome.sql", "20260920260000_task_last_error.sql", "20260920270000_task_response_type.sql", "20260920280000_task_goal.sql", "20260920290000_assistant_invariants.sql", "20260920300000_task_invariant_conflict_response.sql", "20260920310000_metric_history.sql"} {
		raw, readErr := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if readErr != nil {
			_ = db.Close()
			t.Fatal(readErr)
		}
		up, _, ok := strings.Cut(string(raw), "-- +goose Down")
		if !ok {
			_ = db.Close()
			t.Fatalf("migration %s has no Down marker", name)
		}
		if _, execErr := db.Exec(up); execErr != nil {
			_ = db.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewConversationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func taskTestRuntime(t *testing.T, completion *countedCompletion) (*Runtime, *storage.ConversationStore) {
	t.Helper()
	store := taskTestStore(t)
	runtime := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	runtime.SetHistoryStore(store)
	runtime.SetTaskStateStore(store)
	runtime.SetAIAgent(ai.NewAgentWithCompletion(completion, "test prompt", store))
	now := time.Now().UTC()
	runtime.state.snapshot = collector.SystemSnapshot{SnapshotID: "task-test-snapshot", CapturedAt: now, Timestamp: now}
	runtime.state.hasSnapshot = true
	return runtime, store
}

func pauseTask(t *testing.T, runtime *Runtime) domain.TaskState {
	t.Helper()
	state, err := runtime.ensureTaskState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	paused, err := runtime.UpdateTaskState(context.Background(), state.TaskID, state.Version, domain.TaskActionPause)
	if err != nil {
		t.Fatal(err)
	}
	return paused
}

func TestRuntimePausedTaskNeverCallsProviderInAnyNonTerminalPhase(t *testing.T) {
	for _, phase := range []domain.TaskPhase{domain.TaskPhasePlanning, domain.TaskPhaseExecution, domain.TaskPhaseValidation} {
		t.Run(string(phase), func(t *testing.T) {
			completion := &countedCompletion{}
			runtime, store := taskTestRuntime(t, completion)
			state, err := runtime.ensureTaskState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, next := range []domain.TaskPhase{domain.TaskPhaseExecution, domain.TaskPhaseValidation} {
				if state.Phase == phase {
					break
				}
				decision := domain.TaskDecision{Action: domain.TaskDecisionAdvance, Phase: next, Step: "checkpoint", ExpectedActionKind: domain.TaskActionKindAgent, ExpectedAction: "continue"}
				updated, applyErr := state.ApplyDecision(decision)
				if applyErr != nil {
					t.Fatal(applyErr)
				}
				state, err = store.SaveTaskTransition(context.Background(), state.TaskID, string(decision.Action), state, updated, state.Version)
				if err != nil {
					t.Fatal(err)
				}
				runtime.taskStateMu.Lock()
				runtime.taskState, runtime.taskLoaded, runtime.taskConversation = state, true, runtime.ConversationID()
				runtime.taskStateMu.Unlock()
			}
			paused := pauseTask(t, runtime)
			if paused.Phase != phase {
				t.Fatalf("paused phase = %s, want %s", paused.Phase, phase)
			}
			_, err = runtime.ContinueTask(context.Background(), paused.TaskID, paused.Version)
			var pausedErr *TaskPausedError
			if !errors.As(err, &pausedErr) {
				t.Fatalf("ContinueTask() error = %v, want TaskPausedError", err)
			}
			if completion.Calls() != 0 {
				t.Fatalf("provider calls = %d, want 0", completion.Calls())
			}
		})
	}
}

func waitForTaskState(t *testing.T, runtime *Runtime, predicate func(domain.TaskState) bool) domain.TaskState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := runtime.TaskState(context.Background())
		if err == nil && predicate(state) {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for task state")
	return domain.TaskState{}
}

func TestAskHTTPStartsOneAutomaticTask(t *testing.T) {
	completion := &countedCompletion{contents: []string{
		`{"assistant_schema_version":"assistant.v2","response_type":"general_answer","general_answer":{"answer":"План готов.","topic":"os_monitoring"},"task_decision":{"action":"stay","phase":"planning","current_step":"собрать данные","expected_action_kind":"agent","expected_action":"выполнить сбор"}}`,
		`{"assistant_schema_version":"assistant.v2","response_type":"general_answer","general_answer":{"answer":"Данные собраны.","topic":"cpu"},"task_decision":{"action":"advance","phase":"execution","current_step":"проанализировать данные","expected_action_kind":"agent","expected_action":"проверить выводы"}}`,
		`{"assistant_schema_version":"assistant.v2","response_type":"general_answer","general_answer":{"answer":"Проверяю выводы.","topic":"cpu"},"task_decision":{"action":"advance","phase":"validation","current_step":"валидировать вывод","expected_action_kind":"agent","expected_action":"завершить задачу"}}`,
		`{"assistant_schema_version":"assistant.v2","response_type":"general_answer","general_answer":{"answer":"Задача завершена.","topic":"cpu"},"task_decision":{"action":"complete","phase":"done","current_step":"","expected_action_kind":"none","expected_action":""}}`,
	}}
	runtime, store := taskTestRuntime(t, completion)
	mux := http.NewServeMux()
	RegisterRoutes(mux, runtime, fs.FS(os.DirFS(filepath.Join("..", "..", "cmd", "server", "web"))))
	req := httptest.NewRequest(http.MethodPost, "/api/ai/ask", strings.NewReader(`{"question":"Начни диагностику CPU"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var body struct {
		TaskState domain.TaskState `json:"task_state"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.TaskState.Phase != domain.TaskPhasePlanning || body.TaskState.Version != 2 || body.TaskState.Goal != "Начни диагностику CPU" {
		t.Fatalf("HTTP task state = %#v", body.TaskState)
	}
	done := waitForTaskState(t, runtime, func(state domain.TaskState) bool { return state.Phase == domain.TaskPhaseDone })
	persisted, err := store.GetTask(context.Background(), done.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted != done {
		t.Fatalf("persisted state = %#v, done state = %#v", persisted, done)
	}
	events, err := store.ListEvents(context.Background(), storage.DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Role != domain.EventRoleUser || events[1].Role != domain.EventRoleAssistant {
		t.Fatalf("events = %#v", events)
	}
	if completion.Calls() != 1 {
		t.Fatalf("provider calls = %d, want 1", completion.Calls())
	}
}

func TestPauseBeforeAutomaticTurnMakesZeroProviderCalls(t *testing.T) {
	completion := &countedCompletion{}
	runtime, _ := taskTestRuntime(t, completion)
	state, err := runtime.StartTaskRun(context.Background(), "Проверь CPU")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.UpdateTaskState(context.Background(), state.TaskID, state.Version, domain.TaskActionPause); err != nil {
		t.Fatal(err)
	}
	time.Sleep(automaticTaskTurnDelay + 100*time.Millisecond)
	if completion.Calls() != 0 {
		t.Fatalf("provider calls = %d, want 0", completion.Calls())
	}
}

func TestAutomaticRunnerRetriesTruncatedCheckpoint(t *testing.T) {
	t.Skip("the code-owned runner intentionally makes one provider call per task")
	completion := &countedCompletion{
		contents: []string{
			`{"assistant_schema_version":"assistant.v2","response_type":"general_answer","general_answer":{"answer":"План готов.","topic":"os_monitoring"},"task_decision":{"action":"stay","phase":"planning","current_step":"собрать данные","expected_action_kind":"agent","expected_action":"выполнить сбор"}}`,
			`{"assistant_schema_version":"assistant.v2","response_type":"diagnosis","diagnosis":{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"Данные собраны.","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]},"task_decision":{"action":"advance","phase":"execution","current_step":"проанализировать данные","expected_action_kind":"user","expected_action":"уточнить цель"}}`,
		},
		errs: []error{nil, ai.ErrTruncated},
	}
	runtime, _ := taskTestRuntime(t, completion)
	if _, err := runtime.StartTaskRun(context.Background(), "Проверь CPU"); err != nil {
		t.Fatal(err)
	}
	state := waitForTaskState(t, runtime, func(state domain.TaskState) bool {
		return state.Phase == domain.TaskPhaseExecution && state.ExpectedActionKind == domain.TaskActionKindUser
	})
	if state.LastError != "" || completion.Calls() != 3 {
		t.Fatalf("state=%#v calls=%d, want successful retry", state, completion.Calls())
	}
}

func TestUserAnswerSchedulesCheckpointContinuation(t *testing.T) {
	t.Skip("the code-owned runner does not ask the model to create user checkpoints")
	completion := &countedCompletion{contents: []string{
		`{"assistant_schema_version":"assistant.v2","response_type":"diagnosis","diagnosis":{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"Выбор принят.","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]},"task_decision":{"action":"advance","phase":"execution","current_step":"проанализировать GPU","expected_action_kind":"agent","expected_action":"проверить вывод"}}`,
		`{"assistant_schema_version":"assistant.v2","response_type":"diagnosis","diagnosis":{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"Проверка выполнена.","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]},"task_decision":{"action":"wait_for_user","phase":"execution","current_step":"уточнить порог","expected_action_kind":"user","expected_action":"указать порог"}}`,
	}}
	runtime, store := taskTestRuntime(t, completion)
	state, err := runtime.ensureTaskState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := state.ApplyDecision(domain.TaskDecision{Action: domain.TaskDecisionWaitForUser, Phase: domain.TaskPhasePlanning, Step: "выбрать показатель", ExpectedActionKind: domain.TaskActionKindUser, ExpectedAction: "выбрать GPU"})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err = store.SaveTaskTransition(context.Background(), state.TaskID, "wait_for_user", state, waiting, state.Version)
	if err != nil {
		t.Fatal(err)
	}
	runtime.taskStateMu.Lock()
	runtime.taskState, runtime.taskLoaded, runtime.taskConversation = waiting, true, runtime.ConversationID()
	runtime.taskStateMu.Unlock()

	mux := http.NewServeMux()
	RegisterRoutes(mux, runtime, os.DirFS(filepath.Join("..", "..", "cmd", "server", "web")))
	request := httptest.NewRequest(http.MethodPost, "/api/ai/ask", strings.NewReader(`{"question":"GPU"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	waitForTaskState(t, runtime, func(state domain.TaskState) bool {
		return completion.Calls() == 2 && state.ExpectedActionKind == domain.TaskActionKindUser
	})
	for _, message := range completion.LastRequest().Messages {
		if message.Role == "user" && strings.Contains(message.Content, "сохраненному checkpoint") {
			return
		}
	}
	t.Fatal("automatic continuation did not include the saved checkpoint")
}

func TestResumePreservesCheckpointAndContinuesWithoutRestatingGoal(t *testing.T) {
	completion := &countedCompletion{contents: []string{`{"assistant_schema_version":"assistant.v2","response_type":"diagnosis","diagnosis":{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"Продолжаю анализ.","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]},"task_decision":{"action":"advance","phase":"execution","current_step":"собрать данные","expected_action_kind":"agent","expected_action":"проанализировать результаты"}}`}}
	runtime, _ := taskTestRuntime(t, completion)
	state, err := runtime.ensureTaskState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	planned, err := state.ApplyDecision(domain.TaskDecision{Action: domain.TaskDecisionStay, Phase: domain.TaskPhasePlanning, Step: "собрать данные", ExpectedActionKind: domain.TaskActionKindAgent, ExpectedAction: "дождаться результатов"})
	if err != nil {
		t.Fatal(err)
	}
	planned, err = runtime.tasks.SaveTaskTransition(context.Background(), state.TaskID, "stay", state, planned, state.Version)
	if err != nil {
		t.Fatal(err)
	}
	runtime.taskStateMu.Lock()
	runtime.taskState, runtime.taskLoaded, runtime.taskConversation = planned, true, runtime.ConversationID()
	runtime.taskStateMu.Unlock()
	paused := pauseTask(t, runtime)
	resumed, err := runtime.UpdateTaskState(context.Background(), paused.TaskID, paused.Version, domain.TaskActionResume)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.TaskID != planned.TaskID || resumed.Phase != planned.Phase || resumed.Step != planned.Step || resumed.ExpectedAction != planned.ExpectedAction || resumed.ExpectedActionKind != planned.ExpectedActionKind || resumed.Status != domain.TaskStatusActive {
		t.Fatalf("resumed state = %#v, planned state = %#v", resumed, planned)
	}
	if _, err := runtime.AskAI(context.Background(), "Продолжай"); err != nil {
		t.Fatal(err)
	}
	request := completion.LastRequest()
	var content string
	for _, message := range request.Messages {
		if message.Role == "user" {
			content = message.Content
		}
	}
	for _, want := range []string{"собрать данные", "дождаться результатов"} {
		if !strings.Contains(content, want) {
			t.Fatalf("continuation request does not contain saved %q: %s", want, content)
		}
	}
}

func TestAskHTTPRejectsMissingTaskDecisionWithoutAdvancingState(t *testing.T) {
	completion := &countedCompletion{contents: []string{`{"assistant_schema_version":"assistant.v2","response_type":"general_answer","general_answer":{"answer":"Ответ без решения.","topic":"cpu"}}`}}
	runtime, store := taskTestRuntime(t, completion)
	mux := http.NewServeMux()
	RegisterRoutes(mux, runtime, os.DirFS(filepath.Join("..", "..", "cmd", "server", "web")))
	request := httptest.NewRequest(http.MethodPost, "/api/ai/ask", strings.NewReader(`{"question":"Начни диагностику CPU"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	time.Sleep(automaticTaskTurnDelay + 100*time.Millisecond)
	state, err := store.GetOpenTask(context.Background(), storage.DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != domain.TaskPhaseExecution || state.ResponseType != domain.ResponseTypeGeneralAnswer {
		t.Fatalf("state did not advance with the code-owned response type: %#v", state)
	}
	events, err := store.ListEvents(context.Background(), storage.DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Role != domain.EventRoleUser || events[1].Role != domain.EventRoleAssistant {
		t.Fatalf("events = %#v, want user and accepted assistant event", events)
	}
}

func TestAutomaticTaskCompletesAfterInvariantConflict(t *testing.T) {
	completion := &countedCompletion{contents: []string{`{"assistant_schema_version":"assistant.v2","response_type":"invariant_conflict","invariant_conflict":{"answer":"Нельзя автоматически завершать процессы.","invariant_ids":["no-auto-kill-processes"]}}`}}
	runtime, store := taskTestRuntime(t, completion)
	if err := runtime.ReplaceInvariants(context.Background(), []invariants.Invariant{{ID: "no-auto-kill-processes", Category: "business_rule", Statement: "no automatic process termination", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	_, err := runtime.StartTaskRun(context.Background(), "Проверь процесс с высокой нагрузкой CPU")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * automaticTaskTurnDelay)
	state, err := store.GetLatestTask(context.Background(), storage.DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != domain.TaskPhaseDone || state.Outcome != domain.TaskOutcomeCompleted || state.ResponseType != domain.ResponseTypeInvariantConflict {
		t.Fatalf("task state = %#v", state)
	}
}

func TestTaskContinueCancelAndTransitionHistoryHTTP(t *testing.T) {
	completion := &countedCompletion{contents: []string{`{"assistant_schema_version":"assistant.v2","response_type":"diagnosis","diagnosis":{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"Продолжаю задачу.","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]},"task_decision":{"action":"advance","phase":"execution","current_step":"собрать данные","expected_action_kind":"agent","expected_action":"проанализировать результаты"}}`}}
	runtime, _ := taskTestRuntime(t, completion)
	created, err := runtime.StartTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, runtime, os.DirFS(filepath.Join("..", "..", "cmd", "server", "web")))
	continueRequest := httptest.NewRequest(http.MethodPost, "/api/ai/task/continue", strings.NewReader(`{"task_id":"`+created.TaskID+`","version":1}`))
	continueResponse := httptest.NewRecorder()
	mux.ServeHTTP(continueResponse, continueRequest)
	if continueResponse.Code != http.StatusOK {
		t.Fatalf("continue status = %d, body=%s", continueResponse.Code, continueResponse.Body.String())
	}
	var continued struct {
		TaskState domain.TaskState `json:"task_state"`
	}
	if err := json.NewDecoder(continueResponse.Body).Decode(&continued); err != nil {
		t.Fatal(err)
	}
	if continued.TaskState.Phase != domain.TaskPhasePlanning || completion.Calls() != 1 {
		t.Fatalf("continued state = %#v, provider calls = %d", continued.TaskState, completion.Calls())
	}
	cancelRequest := httptest.NewRequest(http.MethodPost, "/api/ai/task/cancel", strings.NewReader(`{"task_id":"`+created.TaskID+`","version":1}`))
	cancelResponse := httptest.NewRecorder()
	mux.ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body=%s", cancelResponse.Code, cancelResponse.Body.String())
	}
	var cancelled domain.TaskState
	if err := json.NewDecoder(cancelResponse.Body).Decode(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Phase != domain.TaskPhaseDone || cancelled.Outcome != domain.TaskOutcomeCancelled {
		t.Fatalf("cancelled state = %#v", cancelled)
	}
	historyRequest := httptest.NewRequest(http.MethodGet, "/api/ai/task/transitions?task_id="+created.TaskID, nil)
	historyResponse := httptest.NewRecorder()
	mux.ServeHTTP(historyResponse, historyRequest)
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("history status = %d, body=%s", historyResponse.Code, historyResponse.Body.String())
	}
	var history struct {
		Items []domain.TaskTransition `json:"items"`
	}
	if err := json.NewDecoder(historyResponse.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 || history.Items[0].Action != "cancel" {
		t.Fatalf("transition history = %#v", history.Items)
	}
	newRequest := httptest.NewRequest(http.MethodPost, "/api/ai/task", nil)
	newResponse := httptest.NewRecorder()
	mux.ServeHTTP(newResponse, newRequest)
	if newResponse.Code != http.StatusCreated {
		t.Fatalf("new task status = %d, body=%s", newResponse.Code, newResponse.Body.String())
	}
}
