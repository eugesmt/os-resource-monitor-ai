package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/memory"
)

const (
	maxAIResponseBytes     = 32 << 10
	maxSummaryRunes        = 2000
	maxFactFieldRunes      = 200
	maxRecommendationRunes = 500
	maxFacts               = 5
	maxRecommendations     = 5
	maxHypotheses          = 5
	maxMissingData         = 20
	DiagnosisSchemaVersion = "diagnosis.v1"
)

func validateAnalysisResponse(result domain.AnalysisResponse, responseBytes int) error {
	if result.Diagnosis == nil {
		return structureError("missing diagnosis")
	}
	return validateDiagnosisResponse(*result.Diagnosis, responseBytes)
}

func validateDiagnosisResponse(result domain.DiagnosisResultV1, responseBytes int) error {
	if responseBytes > maxAIResponseBytes || result.SchemaVersion != DiagnosisSchemaVersion || strings.TrimSpace(result.Status) == "" || strings.TrimSpace(result.Summary) == "" || result.Observations == nil || result.Hypotheses == nil || result.MissingData == nil || result.Recommendations == nil {
		return structureError("missing or oversized required fields")
	}
	validStatus := map[string]bool{"healthy": true, "warning": true, "critical": true, "unknown": true}
	if !validStatus[result.Status] {
		return structureError("invalid status")
	}
	if len(result.Observations) > maxFacts || len(result.Hypotheses) > maxHypotheses || len(result.MissingData) > maxMissingData || len(result.Recommendations) > maxRecommendations {
		return structureError("too many result items")
	}
	if len([]rune(result.Summary)) > maxSummaryRunes {
		return structureError("summary is too long")
	}
	for _, observation := range result.Observations {
		if strings.TrimSpace(observation.Metric) == "" || len([]rune(observation.Metric)) > maxFactFieldRunes || len([]rune(observation.Unit)) > maxFactFieldRunes || strings.TrimSpace(observation.Interpretation) == "" || len([]rune(observation.Interpretation)) > maxFactFieldRunes || math.IsNaN(observation.Value) || math.IsInf(observation.Value, 0) {
			return structureError("invalid observation")
		}
	}
	for _, hypothesis := range result.Hypotheses {
		if strings.TrimSpace(hypothesis) == "" || len([]rune(hypothesis)) > maxRecommendationRunes {
			return structureError("invalid hypothesis")
		}
	}
	for _, missing := range result.MissingData {
		if strings.TrimSpace(missing) == "" || len([]rune(missing)) > maxFactFieldRunes {
			return structureError("invalid missing-data item")
		}
	}
	for _, recommendation := range result.Recommendations {
		if strings.TrimSpace(recommendation) == "" || len([]rune(recommendation)) > maxRecommendationRunes {
			return structureError("invalid recommendation")
		}
	}
	return nil
}

const maxCitations = 20

func validateAssistantResponseStructure(result domain.AssistantResponseV2, responseBytes int) error {
	if responseBytes > maxAIResponseBytes || result.SchemaVersion != domain.AssistantSchemaVersion {
		return structureError("invalid assistant response envelope")
	}
	payloads := 0
	if result.Diagnosis != nil {
		payloads++
	}
	if result.ConversationRecall != nil {
		payloads++
	}
	if result.MetricComparison != nil {
		payloads++
	}
	if result.GeneralAnswer != nil {
		payloads++
	}
	if payloads != 1 {
		return structureError("exactly one response payload is required")
	}
	switch result.ResponseType {
	case domain.ResponseTypeDiagnosis:
		if result.Diagnosis == nil || result.ConversationRecall != nil || result.MetricComparison != nil || result.GeneralAnswer != nil {
			return structureError("diagnosis payload mismatch")
		}
		return validateDiagnosisResponse(*result.Diagnosis, responseBytes)
	case domain.ResponseTypeConversationRecall:
		if result.ConversationRecall == nil || result.Diagnosis != nil || result.MetricComparison != nil || result.GeneralAnswer != nil {
			return structureError("conversation recall payload mismatch")
		}
		if strings.TrimSpace(result.ConversationRecall.Answer) == "" || len([]rune(result.ConversationRecall.Answer)) > maxSummaryRunes || len(result.ConversationRecall.EventIDs) == 0 || len(result.ConversationRecall.EventIDs) > maxCitations {
			return structureError("invalid conversation recall payload")
		}
		if hasDuplicateIDs(result.ConversationRecall.EventIDs) {
			return structureError("duplicate conversation event citation")
		}
		return nil
	case domain.ResponseTypeMetricComparison:
		if result.MetricComparison == nil || result.Diagnosis != nil || result.ConversationRecall != nil || result.GeneralAnswer != nil {
			return structureError("metric comparison payload mismatch")
		}
		if strings.TrimSpace(result.MetricComparison.Answer) == "" || len([]rune(result.MetricComparison.Answer)) > maxSummaryRunes || len(result.MetricComparison.Points) < 2 || len(result.MetricComparison.Points) > maxCitations {
			return structureError("invalid metric comparison payload")
		}
		for _, point := range result.MetricComparison.Points {
			if point.Source != "current_snapshot" && point.Source != "historical_observation" || strings.TrimSpace(point.Metric) == "" || strings.TrimSpace(point.Unit) == "" || strings.TrimSpace(point.SnapshotID) == "" || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
				return structureError("invalid metric comparison point")
			}
			if point.Source == "historical_observation" && point.EventID == nil {
				return structureError("historical point requires event_id")
			}
			if point.Source == "current_snapshot" && point.EventID != nil {
				return structureError("current point cannot cite event_id")
			}
		}
		return nil
	case domain.ResponseTypeGeneralAnswer:
		if result.GeneralAnswer == nil || result.Diagnosis != nil || result.ConversationRecall != nil || result.MetricComparison != nil {
			return structureError("general answer payload mismatch")
		}
		if strings.TrimSpace(result.GeneralAnswer.Answer) == "" || len([]rune(result.GeneralAnswer.Answer)) > maxSummaryRunes || strings.TrimSpace(result.GeneralAnswer.Topic) == "" {
			return structureError("invalid general answer payload")
		}
		return nil
	default:
		return structureError("unknown response type")
	}
}

func hasDuplicateIDs(ids []int64) bool {
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return true
		}
		if _, ok := seen[id]; ok {
			return true
		}
		seen[id] = struct{}{}
	}
	return false
}

func structureError(message string) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidResponse, ErrResponseStructure, message)
}

type completion interface {
	Complete(ctx context.Context, request CompletionRequest) (CompletionResult, error)
}

type HistoryStore interface {
	AppendUser(ctx context.Context, conversationID, turnID, content, snapshotID string) (domain.ConversationEvent, error)
	AppendAssistant(ctx context.Context, conversationID, turnID, content, snapshotID string) (domain.ConversationEvent, error)
	ListEvents(ctx context.Context, conversationID string) ([]domain.ConversationEvent, error)
	ListRecentEvents(ctx context.Context, conversationID string, beforeEventID int64, limit int) ([]domain.ConversationEvent, error)
}

type Agent struct {
	systemPrompt string
	completion   completion
	log          *slog.Logger
	history      HistoryStore
	compiler     *ContextCompiler
	memory       *memory.Model
	conversation string
	turnMu       sync.Mutex
}

func NewAgent(client *Client, prompt string, history HistoryStore, contextTokenBudget ...int) *Agent {
	if history == nil {
		panic("ai.NewAgent requires a HistoryStore")
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultSystemPrompt
	}
	budget := DefaultContextTokenBudget
	if len(contextTokenBudget) > 0 {
		budget = contextTokenBudget[0]
	}
	return &Agent{completion: client, systemPrompt: strings.TrimSpace(prompt), log: slog.Default(), history: history, compiler: NewContextCompiler(history, budget), memory: memory.NewModel(), conversation: "default"}
}

func newAgent(completion completion, prompt string, history HistoryStore, contextTokenBudget ...int) *Agent {
	if history == nil {
		panic("newAgent requires a HistoryStore")
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultSystemPrompt
	}
	budget := DefaultContextTokenBudget
	if len(contextTokenBudget) > 0 {
		budget = contextTokenBudget[0]
	}
	return &Agent{completion: completion, systemPrompt: strings.TrimSpace(prompt), log: slog.Default(), history: history, compiler: NewContextCompiler(history, budget), memory: memory.NewModel(), conversation: "default"}
}

// SetMemoryModel injects the explicit working/long-term memory used by the
// production prompt compiler. Short-term conversation remains in the event log.
func (a *Agent) SetMemoryModel(model *memory.Model) {
	if model != nil {
		a.memory = model
	}
}

func (a *Agent) MemoryModel() *memory.Model { return a.memory }

// SetConversation switches the active user's conversation. Calls are
// serialized with turns so a profile switch cannot change an in-flight ask.
func (a *Agent) SetConversation(conversationID string) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		conversationID = "default"
	}
	a.turnMu.Lock()
	a.conversation = conversationID
	a.turnMu.Unlock()
}

func (a *Agent) ConversationID() string {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	return a.conversation
}

func (a *Agent) SetLogger(logger *slog.Logger) {
	if logger != nil {
		a.log = logger
	}
}

type llmCallEvent struct {
	requestID, conversationID, turnID, snapshotID, userEventID, assistantEventID, persistence, model, responseID, configVersion, finishReason, outcome string
	promptTokens, completionTokens                                                                                                                     any
	latency                                                                                                                                            time.Duration
	usageAvailable                                                                                                                                     bool
	contextBudget, estimatedInputTokens, selectedEventCount, validatedObservationCount                                                                 int
	contextBlockKinds                                                                                                                                  []string
	selectedFirstEventID, selectedLastEventID                                                                                                          int64
}

var requestSequence atomic.Uint64

func completionOutcome(err error) string {
	switch {
	case errors.Is(err, ErrTruncated):
		return "truncated"
	case errors.Is(err, ErrContentFiltered):
		return "content_filtered"
	case errors.Is(err, ErrToolCalls):
		return "tool_calls"
	case errors.Is(err, ErrTimeout):
		return "timeout"
	case errors.Is(err, ErrUpstream):
		return "upstream_error"
	case errors.Is(err, ErrAbnormalFinish):
		return "abnormal_finish"
	default:
		return "completion_error"
	}
}

func (a *Agent) Ask(ctx context.Context, question, contextText string) (*domain.AnalysisResponse, error) {
	return nil, ErrSnapshotUnavailable
}

func (a *Agent) ask(ctx context.Context, question, contextText, snapshotID string, snapshot *collector.SystemSnapshot) (*domain.AnalysisResponse, error) {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.log == nil {
		a.log = slog.Default()
	}
	event := llmCallEvent{requestID: fmt.Sprintf("llm-%d", requestSequence.Add(1)), conversationID: a.conversation, snapshotID: snapshotID, promptTokens: "unavailable", completionTokens: "unavailable", persistence: "disabled", outcome: "request_rejected"}
	event.contextBudget = a.compiler.budget
	defer func() {
		a.log.Info("llm call", "request_id", event.requestID, "conversation_id", event.conversationID, "turn_id", event.turnID, "snapshot_id", event.snapshotID, "user_event_id", event.userEventID, "assistant_event_id", event.assistantEventID, "persistence", event.persistence, "model", event.model, "response_id", event.responseID, "prompt_version", PromptVersion, "schema_version", domain.AssistantSchemaVersion, "config_version", event.configVersion, "finish_reason", event.finishReason, "prompt_tokens", event.promptTokens, "completion_tokens", event.completionTokens, "usage_available", event.usageAvailable, "latency", event.latency, "context_budget", event.contextBudget, "estimated_input_tokens", event.estimatedInputTokens, "context_block_kinds", event.contextBlockKinds, "selected_event_count", event.selectedEventCount, "validated_observation_count", event.validatedObservationCount, "selected_first_event_id", event.selectedFirstEventID, "selected_last_event_id", event.selectedLastEventID, "outcome", event.outcome)
	}()
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return nil, errors.New("question exceeds 4000 characters")
	}
	var currentUserEventID int64
	if a.history != nil && snapshot != nil {
		event.turnID = "turn-" + uuid.NewV4().String()
		event.persistence = "pending"
		userEvent, err := a.history.AppendUser(ctx, a.conversation, event.turnID, question, snapshotID)
		if err != nil {
			event.persistence = "user_failed"
			return nil, fmt.Errorf("%w: append user event: %v", ErrConversationPersistence, err)
		}
		event.userEventID = fmt.Sprint(userEvent.ID)
		currentUserEventID = userEvent.ID
		event.persistence = "user_committed"
	}

	beforeEventID := int64(^uint64(0) >> 1)
	if currentUserEventID != 0 {
		beforeEventID = currentUserEventID
	}
	if a.memory != nil {
		profile := a.memory.Profile()
		if profileContext := profile.Render(); profileContext != "" {
			contextText += "\n\nUSER_PERSONALIZATION (PREFERENCES DATA):\n" + profileContext
		}
		longTermScope := a.conversation
		if longTermScope == "" {
			longTermScope = "default"
		}
		memoryContext := a.memory.BuildContext(memory.MemorySelection{TaskID: a.conversation, LongTermScope: longTermScope, IncludeWorking: true, IncludeLong: true}).Render()
		if memoryContext != "" {
			contextText += "\n\nEXPLICIT_MEMORY_LAYERS (DATA ONLY):\n" + memoryContext
		}
	}
	compiled, err := a.compiler.Compile(ctx, ContextRequest{ConversationID: a.conversation, BeforeEventID: beforeEventID, SystemInstructions: a.systemPrompt, Question: question, CurrentContext: contextText, CurrentSnapshot: snapshotValue(snapshot)})
	if err != nil {
		if errors.Is(err, ErrHistoryUnavailable) {
			event.outcome = "history_unavailable"
		} else if errors.Is(err, ErrRequiredContextTooLarge) {
			event.outcome = "required_context_too_large"
		}
		return nil, err
	}
	event.contextBudget = compiled.Trace.Budget
	event.estimatedInputTokens = compiled.Trace.EstimatedInputTokens
	event.selectedEventCount = compiled.Trace.SelectedEventCount
	event.selectedFirstEventID = compiled.Trace.SelectedFirstEventID
	event.selectedLastEventID = compiled.Trace.SelectedLastEventID
	for _, block := range compiled.Blocks {
		event.contextBlockKinds = append(event.contextBlockKinds, string(block.Kind))
	}
	event.validatedObservationCount = compiled.Trace.ValidatedObservationCount
	completionResult, err := a.completion.Complete(ctx, CompletionRequest{Messages: compiled.Messages, MaxTokens: DefaultMaxTokens})
	event.model, event.responseID, event.configVersion, event.finishReason = completionResult.Model, completionResult.ResponseID, completionResult.ConfigVersion, completionResult.FinishReason
	event.latency = completionResult.Latency
	event.usageAvailable = completionResult.Usage.Available
	if completionResult.Usage.Available {
		event.promptTokens, event.completionTokens = completionResult.Usage.PromptTokens, completionResult.Usage.CompletionTokens
	}
	if err != nil {
		event.outcome = completionOutcome(err)
		return nil, err
	}
	content := completionResult.Content
	result, err := parseAssistantResponse(content)
	if err != nil {
		event.outcome = "parse_error"
		return nil, fmt.Errorf("%w: %w: %v", ErrInvalidResponse, ErrResponseParse, err)
	}
	if err := validateAssistantResponseStructure(result, len([]byte(content))); err != nil {
		event.outcome = "structural_validation_error"
		return nil, err
	}
	if err := ValidateAssistantResponse(result, compiled.Evidence); err != nil {
		event.outcome = "semantic_validation_error"
		return nil, err
	}
	if a.history != nil && snapshot != nil {
		assistantEvent, err := a.history.AppendAssistant(ctx, a.conversation, event.turnID, content, snapshotID)
		if err != nil {
			event.persistence = "assistant_failed"
			return nil, fmt.Errorf("%w: append assistant event: %v", ErrConversationPersistence, err)
		}
		event.assistantEventID = fmt.Sprint(assistantEvent.ID)
		event.persistence = "complete"
	}
	event.outcome = "success"
	return &result, nil
}

func parseAssistantResponse(content string) (domain.AssistantResponseV2, error) {
	var envelopeFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &envelopeFields); err == nil {
		if _, isV2 := envelopeFields["assistant_schema_version"]; isV2 {
			var result domain.AssistantResponseV2
			if err := decodeStrict(content, &result); err != nil {
				return domain.AssistantResponseV2{}, err
			}
			if result.SchemaVersion != domain.AssistantSchemaVersion {
				return domain.AssistantResponseV2{}, fmt.Errorf("unsupported assistant schema version %q", result.SchemaVersion)
			}
			if result.Diagnosis != nil {
				result.Status, result.Summary, result.Observations = result.Diagnosis.Status, result.Diagnosis.Summary, result.Diagnosis.Observations
				result.Hypotheses, result.MissingData, result.Recommendations = result.Diagnosis.Hypotheses, result.Diagnosis.MissingData, result.Diagnosis.Recommendations
			}
			return result, nil
		}
	}
	var result domain.AssistantResponseV2
	if err := decodeStrict(content, &result); err == nil && result.SchemaVersion == domain.AssistantSchemaVersion {
		if result.Diagnosis != nil {
			result.Status, result.Summary, result.Observations = result.Diagnosis.Status, result.Diagnosis.Summary, result.Diagnosis.Observations
			result.Hypotheses, result.MissingData, result.Recommendations = result.Diagnosis.Hypotheses, result.Diagnosis.MissingData, result.Diagnosis.Recommendations
		}
		return result, nil
	}
	return domain.AssistantResponseV2{}, errors.New("assistant.v2 envelope is required")
}

func snapshotValue(snapshot *collector.SystemSnapshot) collector.SystemSnapshot {
	if snapshot == nil {
		return collector.SystemSnapshot{}
	}
	return *snapshot
}

func (a *Agent) AskWithSnapshot(ctx context.Context, question string, snapshot collector.SystemSnapshot) (*domain.AnalysisResponse, error) {
	result, err := a.ask(ctx, question, BuildContext(snapshot), snapshot.SnapshotID, &snapshot)
	if err != nil {
		return nil, err
	}
	return result, nil
}
