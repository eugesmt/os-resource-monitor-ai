package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/invariants"
	"taskmanager-ai/internal/memory"
)

type fakeCompletion struct {
	systemPrompt string
	userContent  string
	request      CompletionRequest
	calls        int
	answer       string
	err          error
	result       CompletionResult
	before       func()
}

func TestGeneralAnswerAllowsDetailedMetricExplanation(t *testing.T) {
	result := domain.AssistantResponseV2{
		SchemaVersion: domain.AssistantSchemaVersion,
		ResponseType:  domain.ResponseTypeGeneralAnswer,
		GeneralAnswer: &domain.GeneralAnswerResponse{Answer: strings.Repeat("метрика ", 700), Topic: "os_monitoring"},
	}
	if err := validateAssistantResponseStructure(result, 16<<10); err != nil {
		t.Fatalf("detailed general answer rejected: %v", err)
	}
}

func TestParseAssistantResponseKeepsV2ParseError(t *testing.T) {
	_, err := parseAssistantResponse(`{"assistant_schema_version":"assistant.v2","response_type":"general_answer","general_answer":{"answer":"GPU","topic":"gpu","unexpected":true}}`)
	if err == nil || !strings.Contains(err.Error(), `unknown field "unexpected"`) {
		t.Fatalf("parse error = %v, want V2 field error", err)
	}
}

type recordingHistory struct {
	mu           sync.Mutex
	events       []domain.ConversationEvent
	userErr      error
	assistantErr error
	recentErr    error
}

func (h *recordingHistory) AppendUser(_ context.Context, conversationID, turnID, content, snapshotID string) (domain.ConversationEvent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.userErr != nil {
		return domain.ConversationEvent{}, h.userErr
	}
	event := domain.ConversationEvent{ID: int64(len(h.events) + 1), ConversationID: conversationID, TurnID: turnID, Role: domain.EventRoleUser, Content: content, SnapshotID: snapshotID, CreatedAt: time.Now()}
	h.events = append(h.events, event)
	return event, nil
}

func (h *recordingHistory) AppendAssistant(_ context.Context, conversationID, turnID, content, snapshotID string) (domain.ConversationEvent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.assistantErr != nil {
		return domain.ConversationEvent{}, h.assistantErr
	}
	event := domain.ConversationEvent{ID: int64(len(h.events) + 1), ConversationID: conversationID, TurnID: turnID, Role: domain.EventRoleAssistant, Content: content, SnapshotID: snapshotID, CreatedAt: time.Now()}
	h.events = append(h.events, event)
	return event, nil
}

func (h *recordingHistory) ListEvents(context.Context, string) ([]domain.ConversationEvent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]domain.ConversationEvent(nil), h.events...), nil
}

func (h *recordingHistory) ListRecentEvents(_ context.Context, conversationID string, beforeEventID int64, limit int) ([]domain.ConversationEvent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.recentErr != nil {
		return nil, h.recentErr
	}
	items := make([]domain.ConversationEvent, 0, limit)
	for i := len(h.events) - 1; i >= 0 && len(items) < limit; i-- {
		event := h.events[i]
		if event.ConversationID == conversationID && event.ID < beforeEventID {
			items = append(items, event)
		}
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return items, nil
}

func (h *recordingHistory) snapshot() []domain.ConversationEvent {
	items, _ := h.ListEvents(context.Background(), "default")
	return items
}

func freshSnapshot(id string) collector.SystemSnapshot {
	now := time.Now()
	return collector.SystemSnapshot{SnapshotID: id, CapturedAt: now, Timestamp: now, CPUPercent: 10}
}

const validAgentAnswer = `{"assistant_schema_version":"assistant.v2","response_type":"diagnosis","diagnosis":{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"answer","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}}`

func (f *fakeCompletion) Complete(_ context.Context, request CompletionRequest) (CompletionResult, error) {
	if f.before != nil {
		f.before()
	}
	f.calls++
	f.request = request
	if len(request.Messages) > 0 {
		f.systemPrompt = request.Messages[0].Content
		f.userContent = request.Messages[len(request.Messages)-1].Content
	}
	result := f.result
	if result.Content == "" {
		result.Content = f.answer
	}
	// Test fixtures historically use the inner diagnosis payload. Production
	// parsing is V2-only; normalize fixtures here so tests exercise that path.
	var legacy domain.DiagnosisResultV1
	decoder := json.NewDecoder(strings.NewReader(result.Content))
	decoder.DisallowUnknownFields()
	var trailing any
	if strings.Contains(result.Content, `"diagnosis_schema_version"`) && decoder.Decode(&legacy) == nil && decoder.Decode(&trailing) == io.EOF {
		wrapped := domain.AssistantResponseV2{SchemaVersion: domain.AssistantSchemaVersion, ResponseType: domain.ResponseTypeDiagnosis, Diagnosis: &legacy}
		if encoded, err := json.Marshal(wrapped); err == nil {
			result.Content = string(encoded)
		}
	}
	return result, f.err
}

func TestAgentAskValidatesAndComposesPrompt(t *testing.T) {
	fake := &fakeCompletion{answer: `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"answer","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}`}
	answer, err := newAgent(fake, "test system prompt", &recordingHistory{}).ask(context.Background(), "  what is happening?  ", "runtime context: unique", "", nil)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer == nil || answer.Summary != "answer" {
		t.Fatalf("answer = %#v, want parsed analysis", answer)
	}
	if fake.calls != 1 {
		t.Fatalf("transport calls = %d, want 1", fake.calls)
	}
	if !strings.HasPrefix(fake.systemPrompt, "test system prompt") {
		t.Fatalf("system prompt = %q", fake.systemPrompt)
	}
	if !strings.Contains(fake.userContent, "CURRENT_USER_QUESTION_JSON:") ||
		!strings.Contains(fake.userContent, "runtime context: unique") ||
		!strings.Contains(fake.userContent, "what is happening?") {
		t.Fatalf("user content has wrong composition: %q", fake.userContent)
	}
	if strings.Contains(fake.userContent, "contextTextMock") {
		t.Fatal("mock context leaked")
	}
}

func TestAgentIncludesCurrentUserProfileOnEveryRequest(t *testing.T) {
	completion := &fakeCompletion{answer: validAgentAnswer}
	agent := newAgent(completion, "test system prompt", &recordingHistory{})
	if err := agent.MemoryModel().SetProfile(memory.Profile{UserID: "user-ru", Style: "concise", Format: "short bullets", Constraints: []string{"answer in Russian"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.ask(context.Background(), "first question", "runtime context", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"USER_PERSONALIZATION", "user_id: user-ru", "style: concise", "format: short bullets", "constraint: answer in Russian"} {
		if !strings.Contains(completion.userContent, want) {
			t.Fatalf("first request missing %q: %q", want, completion.userContent)
		}
	}

	if err := agent.MemoryModel().SetProfile(memory.Profile{UserID: "user-en", Style: "detailed", Format: "numbered steps", Constraints: []string{"answer in English"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.ask(context.Background(), "second question", "runtime context", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"user_id: user-en", "style: detailed", "format: numbered steps", "constraint: answer in English"} {
		if !strings.Contains(completion.userContent, want) {
			t.Fatalf("second request missing %q: %q", want, completion.userContent)
		}
	}
	if strings.Contains(completion.userContent, "user-ru") || strings.Contains(completion.userContent, "answer in Russian") {
		t.Fatalf("previous profile leaked into second request: %q", completion.userContent)
	}
}

func TestAgentReturnsValidatedInvariantConflict(t *testing.T) {
	answer := `{"assistant_schema_version":"assistant.v2","response_type":"invariant_conflict","invariant_conflict":{"answer":"Не могу предложить Kubernetes: это нарушает правило проекта.","invariant_ids":["stack-no-kubernetes"]}}`
	completion := &fakeCompletion{answer: answer}
	agent := newAgent(completion, "test system prompt", &recordingHistory{})
	if err := agent.invariants.Replace([]invariants.Invariant{{ID: "stack-no-kubernetes", Category: "stack", Statement: "Kubernetes is forbidden", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	got, err := agent.ask(context.Background(), "Перенеси сервис на Kubernetes", "runtime context", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResponseType != domain.ResponseTypeInvariantConflict || got.InvariantConflict == nil || got.InvariantConflict.InvariantIDs[0] != "stack-no-kubernetes" {
		t.Fatalf("response = %#v", got)
	}
	if !strings.Contains(completion.systemPrompt, "PROJECT_INVARIANTS") || !strings.Contains(completion.systemPrompt, "stack-no-kubernetes") {
		t.Fatalf("invariants missing from system prompt: %q", completion.systemPrompt)
	}
}

func TestAgentRejectsUnknownInvariantConflict(t *testing.T) {
	answer := `{"assistant_schema_version":"assistant.v2","response_type":"invariant_conflict","invariant_conflict":{"answer":"refusal","invariant_ids":["unknown"]}}`
	agent := newAgent(&fakeCompletion{answer: answer}, "test", &recordingHistory{})
	if err := agent.invariants.Replace([]invariants.Invariant{{ID: "known", Category: "stack", Statement: "rule", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.ask(context.Background(), "question", "context", "", nil); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseAssistantResponseNormalizesConflictingIDs(t *testing.T) {
	content := `{"assistant_schema_version":"assistant.v2","response_type":"invariant_conflict","invariant_conflict":{"answer":"refusal","conflicting_ids":["rule-1"]}}`
	got, err := parseAssistantResponse(content)
	if err != nil {
		t.Fatal(err)
	}
	if got.InvariantConflict == nil || !reflect.DeepEqual(got.InvariantConflict.InvariantIDs, []string{"rule-1"}) || len(got.InvariantConflict.ConflictingIDs) != 0 {
		t.Fatalf("conflict = %#v", got.InvariantConflict)
	}
}

func TestAgentRequiresComplianceReviewWhenInvariantsAreActive(t *testing.T) {
	agent := newAgent(&fakeCompletion{answer: validAgentAnswer}, "test", &recordingHistory{})
	if err := agent.invariants.Replace([]invariants.Invariant{{ID: "known", Category: "architecture", Statement: "keep monolith", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.ask(context.Background(), "question", "context", "", nil); err == nil || !strings.Contains(err.Error(), "compliance review") {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentIsolatesConversationAndLongTermMemoryByProfile(t *testing.T) {
	history := &recordingHistory{}
	completion := &fakeCompletion{answer: validAgentAnswer}
	agent := newAgent(completion, "test system prompt", history)
	if err := agent.MemoryModel().SetProfile(memory.Profile{UserID: "user-1", Style: "concise"}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.MemoryModel().RememberLongTerm("user-1", "preference", "only user one"); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.MemoryModel().RememberLongTerm("default", "preference", "default user only"); err != nil {
		t.Fatal(err)
	}
	agent.SetConversation("user-1")
	if _, err := agent.AskWithSnapshot(context.Background(), "question", freshSnapshot("profile-snapshot")); err != nil {
		t.Fatal(err)
	}
	if agent.ConversationID() != "user-1" {
		t.Fatalf("conversation ID = %q, want user-1", agent.ConversationID())
	}
	events, err := history.ListEvents(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ConversationID != "user-1" || events[1].ConversationID != "user-1" {
		t.Fatalf("profile events = %#v", events)
	}
	if !strings.Contains(completion.userContent, "only user one") || strings.Contains(completion.userContent, "default user only") {
		t.Fatalf("profile memory crossed scopes: %q", completion.userContent)
	}
}

func TestAgentAskValidationDoesNotCallTransport(t *testing.T) {
	for name, question := range map[string]string{"empty": " \t\n", "too long": strings.Repeat("я", 4001)} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeCompletion{}
			_, err := newAgent(fake, "test system prompt", &recordingHistory{}).ask(context.Background(), question, "context", "", nil)
			if err == nil {
				t.Fatal("Ask() error = nil")
			}
			if fake.calls != 0 {
				t.Fatalf("transport calls = %d, want 0", fake.calls)
			}
		})
	}
}

func TestAgentAskReturnsCompletionError(t *testing.T) {
	wantErr := errors.New("upstream")
	_, err := newAgent(&fakeCompletion{err: wantErr}, "test system prompt", &recordingHistory{}).ask(context.Background(), "question", "context", "", nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestAgentAskRejectsInvalidStructuredResponse(t *testing.T) {
	cases := map[string]string{
		"missing summary":          `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}`,
		"null observations":        `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"ok","observations":null,"hypotheses":[],"missing_data":[],"recommendations":[]}`,
		"empty metric":             `{"diagnosis_schema_version":"diagnosis.v1","status":"warning","summary":"ok","observations":[{"metric":"","value":1,"unit":"%","interpretation":"bad"}],"hypotheses":[],"missing_data":[],"recommendations":[]}`,
		"too many recommendations": `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"ok","observations":[],"hypotheses":[],"missing_data":[],"recommendations":["1","2","3","4","5","6"]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := newAgent(&fakeCompletion{answer: content}, "test system prompt", &recordingHistory{}).ask(context.Background(), "question", "context", "", nil)
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestAgentAskRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	valid := `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"ok","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}`
	for name, content := range map[string]string{
		"unknown field":   strings.TrimSuffix(valid, "}") + `,"extra":"nope"}`,
		"trailing object": valid + ` {"another":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newAgent(&fakeCompletion{answer: content}, "test system prompt", &recordingHistory{}).ask(context.Background(), "question", "context", "", nil)
			if !errors.Is(err, ErrInvalidResponse) || !errors.Is(err, ErrResponseParse) {
				t.Fatalf("error = %v, want strict parse failure", err)
			}
		})
	}
}

func TestAgentTelemetryKeepsOutcomeAndUnavailableUsage(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	completion := &fakeCompletion{
		result: CompletionResult{Content: `{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"ok","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}`, Model: "test-model", ConfigVersion: "test-config", ResponseID: "resp-1", FinishReason: "stop", Latency: 12},
	}
	agent := newAgent(completion, "test system prompt", &recordingHistory{})
	agent.SetLogger(logger)
	if _, err := agent.ask(context.Background(), "secret question", "secret snapshot", "", nil); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	line := logs.String()
	for _, want := range []string{"outcome=success", "model=test-model", "config_version=test-config", "prompt_tokens=unavailable", "usage_available=false", "context_budget=8000", "estimated_input_tokens=", "context_block_kinds=", "selected_event_count=0"} {
		if !strings.Contains(line, want) {
			t.Fatalf("telemetry missing %q: %s", want, line)
		}
	}
	if strings.Contains(line, "secret question") || strings.Contains(line, "secret snapshot") {
		t.Fatalf("sensitive content leaked in telemetry: %s", line)
	}
}

func TestAgentAskWithoutSnapshotIsRejectedBeforeProvider(t *testing.T) {
	completion := &fakeCompletion{answer: validAgentAnswer}
	agent := newAgent(completion, "test system prompt", &recordingHistory{})
	if _, err := agent.Ask(context.Background(), "question", "context"); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("Ask() error = %v, want ErrSnapshotUnavailable", err)
	}
	if completion.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", completion.calls)
	}
}

func TestAgentPersistsAcceptedTurnAroundValidatedCompletion(t *testing.T) {
	history := &recordingHistory{}
	completion := &fakeCompletion{answer: validAgentAnswer, before: func() {
		events := history.snapshot()
		if len(events) != 1 || events[0].Role != domain.EventRoleUser {
			t.Fatalf("events at provider call = %#v, want committed user", events)
		}
	}}
	agent := newAgent(completion, "test system prompt", history)

	answer, err := agent.AskWithSnapshot(context.Background(), "  question  ", freshSnapshot("snap-1"))
	if err != nil {
		t.Fatalf("AskWithSnapshot() error = %v", err)
	}
	if answer.Summary != "answer" || completion.calls != 1 {
		t.Fatalf("answer=%#v calls=%d", answer, completion.calls)
	}
	events := history.snapshot()
	if len(events) != 2 || events[0].Role != domain.EventRoleUser || events[1].Role != domain.EventRoleAssistant {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Content != "question" || events[1].Content != validAgentAnswer || events[0].TurnID != events[1].TurnID || events[0].SnapshotID != "snap-1" || events[1].SnapshotID != "snap-1" {
		t.Fatalf("events are not linked to raw request/response: %#v", events)
	}
	rawTurnID := strings.TrimPrefix(events[0].TurnID, "turn-")
	parsed, err := uuid.Parse(rawTurnID)
	if err != nil || parsed[6]>>4 != 4 || parsed[8]>>6 != 2 {
		t.Fatalf("turn ID %q is not RFC 9562 UUIDv4: bytes=%x err=%v", events[0].TurnID, parsed, err)
	}
}

func TestAgentRecoversRecallWithCitationOutsideSelectedHistory(t *testing.T) {
	history := &recordingHistory{events: []domain.ConversationEvent{
		historyEvent(1, "turn-1", domain.EventRoleUser, "first question"),
		historyEvent(2, "turn-1", domain.EventRoleAssistant, validAgentAnswer),
	}}
	completion := &fakeCompletion{answer: `{"assistant_schema_version":"assistant.v2","response_type":"conversation_recall","conversation_recall":{"answer":"ungrounded answer","event_ids":[999]}}`}
	agent := newAgent(completion, "test system prompt", history)

	if _, err := agent.AskWithSnapshot(context.Background(), "what did you say before?", freshSnapshot("snap-recall")); err != nil {
		t.Fatalf("AskWithSnapshot() error = %v", err)
	}
	if completion.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", completion.calls)
	}
	events := history.snapshot()
	if len(events) != 4 {
		t.Fatalf("events = %#v, want user and recovered assistant event", events)
	}
	var persisted domain.AssistantResponseV2
	if err := json.Unmarshal([]byte(events[3].Content), &persisted); err != nil {
		t.Fatalf("decode recovered assistant response: %v", err)
	}
	if persisted.ConversationRecall == nil || persisted.ConversationRecall.EventIDs[0] != 2 || !strings.Contains(persisted.ConversationRecall.Answer, "Не могу надёжно подтвердить") {
		t.Fatalf("persisted recall = %#v", persisted.ConversationRecall)
	}
}

func TestAgentFirstAndSecondTurnStructuredMessageOrder(t *testing.T) {
	history := &recordingHistory{}
	completion := &fakeCompletion{answer: validAgentAnswer}
	agent := newAgent(completion, "test system prompt", history)
	if _, err := agent.AskWithSnapshot(context.Background(), "first question", freshSnapshot("snap-1")); err != nil {
		t.Fatal(err)
	}
	if len(completion.request.Messages) != 2 || completion.request.Messages[0].Role != "system" || completion.request.Messages[1].Role != "user" {
		t.Fatalf("first-turn messages = %#v", completion.request.Messages)
	}
	if completion.request.MaxTokens != DefaultMaxTokens {
		t.Fatalf("max tokens = %d", completion.request.MaxTokens)
	}

	if _, err := agent.AskWithSnapshot(context.Background(), "second question", freshSnapshot("snap-2")); err != nil {
		t.Fatal(err)
	}
	wantRoles := []string{"system", "user", "assistant", "user"}
	roles := make([]string, 0, len(completion.request.Messages))
	for _, message := range completion.request.Messages {
		roles = append(roles, message.Role)
	}
	if !reflect.DeepEqual(roles, wantRoles) {
		t.Fatalf("second-turn roles = %v, want %v", roles, wantRoles)
	}
	if !strings.Contains(completion.request.Messages[1].Content, "first question") || !strings.Contains(completion.request.Messages[2].Content, `\"summary\":\"answer\"`) || !strings.Contains(completion.request.Messages[3].Content, "second question") || !strings.Contains(completion.request.Messages[3].Content, "snap-2") {
		t.Fatalf("second-turn messages = %#v", completion.request.Messages)
	}
}

func TestAgentIncludesTaskStateForContinuation(t *testing.T) {
	completion := &fakeCompletion{answer: `{"assistant_schema_version":"assistant.v2","response_type":"diagnosis","task_decision":{"action":"stay","phase":"execution","current_step":"run checks","expected_action_kind":"agent","expected_action":"wait for result"},"diagnosis":{"diagnosis_schema_version":"diagnosis.v1","status":"healthy","summary":"answer","observations":[],"hypotheses":[],"missing_data":[],"recommendations":[]}}`}
	agent := newAgent(completion, "test system prompt", &recordingHistory{})
	state := domain.TaskState{TaskID: "default", Phase: domain.TaskPhaseExecution, Step: "run checks", ExpectedActionKind: domain.TaskActionKindAgent, ExpectedAction: "wait for result", Status: domain.TaskStatusPaused, Outcome: domain.TaskOutcomeOpen, Version: 3}
	agent.SetTaskState(&state)
	if _, err := agent.AskWithSnapshot(context.Background(), "continue", freshSnapshot("snap-continue")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.request.Messages[len(completion.request.Messages)-1].Content, "TASK_STATE") ||
		!strings.Contains(completion.request.Messages[len(completion.request.Messages)-1].Content, "run checks") ||
		!strings.Contains(completion.request.Messages[len(completion.request.Messages)-1].Content, "wait for result") {
		t.Fatalf("continuation context = %q", completion.request.Messages[len(completion.request.Messages)-1].Content)
	}
}

func TestTaskPlanningAcceptsGeneralPlanForGenericGoal(t *testing.T) {
	state := domain.NewTaskState("task-plan")
	result := domain.AssistantResponseV2{
		SchemaVersion: domain.AssistantSchemaVersion,
		ResponseType:  domain.ResponseTypeGeneralAnswer,
		GeneralAnswer: &domain.GeneralAnswerResponse{Answer: "Составляю план.", Topic: "os_monitoring"},
	}
	if err := validateTaskAwareAssistantResponse(result, ContextEvidence{Question: "Продолжить"}, &state); err != nil {
		t.Fatalf("planning response rejected: %v", err)
	}
}

func TestAgentContextFailuresKeepUserAndSkipProvider(t *testing.T) {
	for _, tc := range []struct {
		name    string
		history *recordingHistory
		budget  int
		wantErr error
	}{
		{name: "history unavailable", history: &recordingHistory{recentErr: errors.New("read failed")}, budget: 8000, wantErr: ErrHistoryUnavailable},
		{name: "required overflow", history: &recordingHistory{}, budget: 1, wantErr: ErrRequiredContextTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			completion := &fakeCompletion{answer: validAgentAnswer}
			agent := newAgent(completion, "test system prompt", tc.history, tc.budget)
			_, err := agent.AskWithSnapshot(context.Background(), "question", freshSnapshot("snap-context-failure"))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if completion.calls != 0 {
				t.Fatalf("provider calls = %d, want 0", completion.calls)
			}
			events := tc.history.snapshot()
			if len(events) != 1 || events[0].Role != domain.EventRoleUser {
				t.Fatalf("events = %#v, want persisted user only", events)
			}
		})
	}
}

func TestAgentFailuresLeaveOnlyAcceptedUserEvent(t *testing.T) {
	cases := []struct {
		name       string
		completion *fakeCompletion
		wantErr    error
	}{
		{name: "provider", completion: &fakeCompletion{err: ErrUpstream}, wantErr: ErrUpstream},
		{name: "parse", completion: &fakeCompletion{answer: `{`}, wantErr: ErrResponseParse},
		{name: "structure", completion: &fakeCompletion{answer: `{"diagnosis_schema_version":"diagnosis.v1"}`}, wantErr: ErrResponseStructure},
		{name: "semantic", completion: &fakeCompletion{answer: `{"diagnosis_schema_version":"diagnosis.v1","status":"warning","summary":"bad","observations":[{"metric":"cpu.percent","value":99,"unit":"%","interpretation":"high"}],"hypotheses":[],"missing_data":[],"recommendations":[]}`}, wantErr: ErrSemanticValidation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			history := &recordingHistory{}
			agent := newAgent(tc.completion, "test system prompt", history)
			_, err := agent.AskWithSnapshot(context.Background(), "question", freshSnapshot("snap-failure"))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			events := history.snapshot()
			if len(events) != 1 || events[0].Role != domain.EventRoleUser {
				t.Fatalf("events = %#v, want one user", events)
			}
		})
	}
}

func TestAgentPersistenceFailuresControlProviderAndSuccess(t *testing.T) {
	userFailure := errors.New("user write failed")
	history := &recordingHistory{userErr: userFailure}
	completion := &fakeCompletion{answer: validAgentAnswer}
	agent := newAgent(completion, "test system prompt", history)
	_, err := agent.AskWithSnapshot(context.Background(), "question", freshSnapshot("snap-user"))
	if !errors.Is(err, ErrConversationPersistence) || completion.calls != 0 {
		t.Fatalf("user persistence error=%v calls=%d", err, completion.calls)
	}

	assistantFailure := errors.New("assistant write failed")
	history = &recordingHistory{assistantErr: assistantFailure}
	completion = &fakeCompletion{answer: validAgentAnswer}
	agent = newAgent(completion, "test system prompt", history)
	answer, err := agent.AskWithSnapshot(context.Background(), "question", freshSnapshot("snap-assistant"))
	if answer != nil || !errors.Is(err, ErrConversationPersistence) || completion.calls != 1 {
		t.Fatalf("assistant persistence answer=%#v error=%v calls=%d", answer, err, completion.calls)
	}
	events := history.snapshot()
	if len(events) != 1 || events[0].Role != domain.EventRoleUser {
		t.Fatalf("events = %#v, want orphan user", events)
	}
}

type cancelingCompletion struct{}

func (cancelingCompletion) Complete(ctx context.Context, _ CompletionRequest) (CompletionResult, error) {
	<-ctx.Done()
	return CompletionResult{}, ctx.Err()
}

func TestAgentCancellationAfterUserCommitKeepsUser(t *testing.T) {
	history := &recordingHistory{}
	agent := newAgent(cancelingCompletion{}, "test system prompt", history)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := agent.AskWithSnapshot(ctx, "question", freshSnapshot("snap-cancel"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	events := history.snapshot()
	if len(events) != 1 || events[0].Role != domain.EventRoleUser {
		t.Fatalf("events = %#v, want committed user", events)
	}
}

type concurrentCompletion struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (c *concurrentCompletion) Complete(context.Context, CompletionRequest) (CompletionResult, error) {
	c.mu.Lock()
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	c.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	return CompletionResult{Content: validAgentAnswer}, nil
}

func TestAgentConcurrentAsksSerializeWholeTurns(t *testing.T) {
	history := &recordingHistory{}
	completion := &concurrentCompletion{}
	agent := newAgent(completion, "test system prompt", history)
	const asks = 6
	var wg sync.WaitGroup
	errs := make(chan error, asks)
	for i := 0; i < asks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := agent.AskWithSnapshot(context.Background(), "question", freshSnapshot("snap-concurrent"))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AskWithSnapshot() error = %v", err)
		}
	}
	completion.mu.Lock()
	maxActive := completion.maxActive
	completion.mu.Unlock()
	if maxActive != 1 {
		t.Fatalf("max concurrent provider calls = %d, want 1", maxActive)
	}
	events := history.snapshot()
	if len(events) != asks*2 {
		t.Fatalf("event count = %d, want %d", len(events), asks*2)
	}
	seen := make(map[string]bool)
	for i := 0; i < len(events); i += 2 {
		if events[i].Role != domain.EventRoleUser || events[i+1].Role != domain.EventRoleAssistant || events[i].TurnID != events[i+1].TurnID || seen[events[i].TurnID] {
			t.Fatalf("events are not serialized turn pairs: %#v", events)
		}
		seen[events[i].TurnID] = true
	}
}
