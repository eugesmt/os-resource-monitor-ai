package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
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

func structureError(message string) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidResponse, ErrResponseStructure, message)
}

type completion interface {
	Complete(ctx context.Context, systemPrompt, userContent string) (CompletionResult, error)
}

type Agent struct {
	systemPrompt string
	completion   completion
	log          *slog.Logger
}

func NewAgent(client *Client, prompt string) *Agent {
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultSystemPrompt
	}
	return &Agent{completion: client, systemPrompt: strings.TrimSpace(prompt), log: slog.Default()}
}

func newAgent(completion completion, prompt string) *Agent {
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultSystemPrompt
	}
	return &Agent{completion: completion, systemPrompt: strings.TrimSpace(prompt), log: slog.Default()}
}

func (a *Agent) SetLogger(logger *slog.Logger) {
	if logger != nil {
		a.log = logger
	}
}

type llmCallEvent struct {
	requestID, snapshotID, model, responseID, configVersion, finishReason, outcome string
	promptTokens, completionTokens                                                 any
	latency                                                                        time.Duration
	usageAvailable                                                                 bool
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
	return a.ask(ctx, question, contextText, "", nil)
}

func (a *Agent) ask(ctx context.Context, question, contextText, snapshotID string, snapshot *collector.SystemSnapshot) (*domain.AnalysisResponse, error) {
	if a.log == nil {
		a.log = slog.Default()
	}
	event := llmCallEvent{requestID: fmt.Sprintf("llm-%d", requestSequence.Add(1)), snapshotID: snapshotID, promptTokens: "unavailable", completionTokens: "unavailable", outcome: "request_rejected"}
	defer func() {
		a.log.Info("llm call", "request_id", event.requestID, "snapshot_id", event.snapshotID, "model", event.model, "response_id", event.responseID, "prompt_version", PromptVersion, "schema_version", DiagnosisSchemaVersion, "config_version", event.configVersion, "finish_reason", event.finishReason, "prompt_tokens", event.promptTokens, "completion_tokens", event.completionTokens, "usage_available", event.usageAvailable, "latency", event.latency, "outcome", event.outcome)
	}()
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return nil, errors.New("question exceeds 4000 characters")
	}

	userContent := fmt.Sprintf("USER QUESTION:\n%s\n\nSNAPSHOT:\n%s", question, contextText)
	completionResult, err := a.completion.Complete(ctx, a.systemPrompt, userContent)
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
	var result domain.AnalysisResponse
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		event.outcome = "parse_error"
		return nil, fmt.Errorf("%w: %w: %v", ErrInvalidResponse, ErrResponseParse, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		event.outcome = "parse_error"
		if err == nil {
			return nil, fmt.Errorf("%w: %w: trailing JSON value", ErrInvalidResponse, ErrResponseParse)
		}
		return nil, fmt.Errorf("%w: %w: trailing data: %v", ErrInvalidResponse, ErrResponseParse, err)
	}
	if err := validateAnalysisResponse(result, len([]byte(content))); err != nil {
		event.outcome = "structural_validation_error"
		return nil, err
	}
	if snapshot != nil {
		if err := ValidateDiagnosis(result, *snapshot); err != nil {
			event.outcome = "semantic_validation_error"
			return nil, err
		}
	}
	event.outcome = "success"
	return &result, nil
}

func (a *Agent) AskWithSnapshot(ctx context.Context, question string, snapshot collector.SystemSnapshot) (*domain.AnalysisResponse, error) {
	result, err := a.ask(ctx, question, BuildContext(snapshot), snapshot.SnapshotID, &snapshot)
	if err != nil {
		return nil, err
	}
	return result, nil
}
