package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"taskmanager-ai/internal/domain"
)

const (
	maxAIResponseBytes     = 32 << 10
	maxSummaryRunes        = 2000
	maxFactFieldRunes      = 200
	maxRecommendationRunes = 500
	maxFacts               = 5
	maxRecommendations     = 5
)

func validateAnalysisResponse(result domain.AnalysisResponse, responseBytes int) error {
	if responseBytes > maxAIResponseBytes || strings.TrimSpace(result.Summary) == "" || result.Facts == nil || result.Recommendations == nil {
		return fmt.Errorf("%w: missing or oversized required fields", ErrInvalidResponse)
	}
	if len(result.Facts) > maxFacts || len(result.Recommendations) > maxRecommendations {
		return fmt.Errorf("%w: too many facts or recommendations", ErrInvalidResponse)
	}
	if len([]rune(result.Summary)) > maxSummaryRunes {
		return fmt.Errorf("%w: summary is too long", ErrInvalidResponse)
	}
	for _, fact := range result.Facts {
		if strings.TrimSpace(fact.Metric) == "" || len([]rune(fact.Metric)) > maxFactFieldRunes || len([]rune(fact.Unit)) > maxFactFieldRunes || math.IsNaN(fact.Value) || math.IsInf(fact.Value, 0) {
			return fmt.Errorf("%w: invalid fact", ErrInvalidResponse)
		}
	}
	for _, recommendation := range result.Recommendations {
		if strings.TrimSpace(recommendation) == "" || len([]rune(recommendation)) > maxRecommendationRunes {
			return fmt.Errorf("%w: invalid recommendation", ErrInvalidResponse)
		}
	}
	return nil
}

type completion interface {
	Complete(ctx context.Context, systemPrompt string, messages []domain.ChatMessage) (CompletionResult, error)
}

type HistoryStore interface {
	Load(context.Context) ([]domain.ChatMessage, error)
	AppendTurn(context.Context, string, string, domain.AICallUsage) error
}

type ContextStore interface {
	LoadSummaryState(context.Context) (*domain.DialogueSummary, error)
	LoadSummaryBatch(context.Context, int64, int64, int) ([]domain.ChatMessage, error)
	LoadRecentMessages(context.Context, int) ([]domain.ChatMessage, error)
	SaveSummary(context.Context, *domain.DialogueSummary, domain.DialogueSummary, domain.SummaryCallUsage) error
	RecordSummaryAttempt(context.Context, domain.SummaryCallUsage) error
}

type Agent struct {
	systemPrompt     string
	completion       completion
	history          HistoryStore
	summarizer       Summarizer
	recentLimit      int
	batchSize        int
	maxSummaryTokens int
	mu               sync.Mutex
}

type contextMetrics struct {
	summary, recent, uncompressed int
	compacted                     bool
}

func NewAgent(client *Client, prompt string, history HistoryStore) *Agent {
	return NewConfiguredAgent(client, prompt, history, 20, 10, 800, "")
}

func NewConfiguredAgent(client *Client, prompt string, history HistoryStore, recentLimit, batchSize, maxSummaryTokens int, summaryModel string) *Agent {
	a := &Agent{completion: client, systemPrompt: strings.TrimSpace(prompt), history: history, recentLimit: recentLimit, batchSize: batchSize, maxSummaryTokens: maxSummaryTokens}
	if client != nil {
		a.summarizer = NewSummarizer(client, summaryModel, maxSummaryTokens)
	}
	return a
}

func newAgent(completion completion, prompt string, history ...HistoryStore) *Agent {
	var store HistoryStore
	if len(history) > 0 {
		store = history[0]
	}
	return &Agent{completion: completion, systemPrompt: strings.TrimSpace(prompt), history: store, recentLimit: 20, batchSize: 10, maxSummaryTokens: 800}
}

func (a *Agent) Ask(ctx context.Context, question, contextText string) (*domain.AnalysisResponse, domain.AICallUsage, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, domain.AICallUsage{}, errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return nil, domain.AICallUsage{}, errors.New("question exceeds 4000 characters")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var messages []domain.ChatMessage
	metrics := contextMetrics{}
	if a.history != nil {
		if cs, ok := a.history.(ContextStore); ok && a.summarizer != nil {
			var err error
			messages, metrics, err = a.prepareContext(ctx, cs)
			if err != nil {
				return nil, domain.AICallUsage{}, err
			}
		} else {
			var err error
			messages, err = a.history.Load(ctx)
			if err != nil {
				return nil, domain.AICallUsage{}, fmt.Errorf("load chat history: %w", err)
			}
		}
	}

	userContent := fmt.Sprintf("CURRENT REQUEST\n\nSYSTEM CONTEXT (authoritative source for metrics):\n%s\n\nUSER QUESTION:\n%s\n\nUse previous messages only as DIALOGUE HISTORY for understanding the question. Do not use numbers from previous AI answers as system metrics.", contextText, question)
	messages = append(messages, domain.ChatMessage{Role: domain.ChatRoleUser, Content: userContent})
	completion, err := a.completion.Complete(ctx, a.systemPrompt, messages)
	if metrics.compacted {
		completion.Usage.SummaryTokens = metrics.summary
		completion.Usage.RecentHistoryTokens = metrics.recent
		completion.Usage.UncompressedHistoryTokens = metrics.uncompressed
		completion.Usage.EstimatedUncompressedInputTokens = completion.Usage.SystemPromptTokens + metrics.uncompressed + completion.Usage.CurrentRequestTokens
		completion.Usage.EstimatedTokensSaved = completion.Usage.EstimatedUncompressedInputTokens - completion.Usage.EstimatedInputTokens
		completion.Usage.HistoryCompacted = true
	}
	if err != nil {
		return nil, completion.Usage, err
	}
	var result domain.AnalysisResponse
	if err := json.Unmarshal([]byte(completion.Content), &result); err != nil {
		return nil, completion.Usage, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if err := validateAnalysisResponse(result, len([]byte(completion.Content))); err != nil {
		return nil, completion.Usage, err
	}
	if a.history != nil {
		if err := a.history.AppendTurn(ctx, question, completion.Content, completion.Usage); err != nil {
			return nil, completion.Usage, fmt.Errorf("persist chat history: %w", err)
		}
	}
	return &result, completion.Usage, nil
}

func (a *Agent) prepareContext(ctx context.Context, store ContextStore) ([]domain.ChatMessage, contextMetrics, error) {
	allRecent, err := store.LoadRecentMessages(ctx, a.recentLimit+2)
	if err != nil {
		return nil, contextMetrics{}, fmt.Errorf("load recent chat messages: %w", err)
	}
	if len(allRecent) <= a.recentLimit {
		return allRecent, contextMetrics{}, nil
	}
	recent := allRecent[len(allRecent)-a.recentLimit:]
	cutoff := recent[0].ID
	state, err := store.LoadSummaryState(ctx)
	if err != nil {
		return nil, contextMetrics{}, fmt.Errorf("load summary state: %w", err)
	}
	if state != nil && state.RecentMessagesLimit != a.recentLimit {
		return nil, contextMetrics{}, fmt.Errorf("summary recent limit incompatible with configuration")
	}
	if state != nil && state.SummaryTokensEstimate > a.maxSummaryTokens {
		return nil, contextMetrics{}, fmt.Errorf("summary token limit incompatible with configuration")
	}
	for {
		cursor := int64(0)
		var previous *domain.DialogueSummary
		previousContent := ""
		sourceCount := 0
		sourceTokens := 0
		version := int64(1)
		if state != nil {
			cursor = state.LastMessageID
			previous = state
			previousContent = state.Content
			sourceCount = state.SourceMessageCount
			sourceTokens = state.SourceTokensEstimate
			version = state.Version + 1
		}
		batch, err := store.LoadSummaryBatch(ctx, cursor, cutoff, a.batchSize)
		if err != nil {
			return nil, contextMetrics{}, fmt.Errorf("load summary batch: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		res, err := a.summarizer.Update(ctx, previousContent, batch)
		res.Usage.PreviousLastMessageID = cursor
		if err != nil {
			_ = store.RecordSummaryAttempt(ctx, res.Usage)
			return nil, contextMetrics{}, fmt.Errorf("update dialogue summary: %w", err)
		}
		for _, m := range batch {
			sourceTokens += estimateTokens(m.Role, m.Content)
		}
		sourceCount += len(batch)
		next := domain.DialogueSummary{Content: res.Content, LastMessageID: batch[len(batch)-1].ID, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), SourceMessageCount: sourceCount, SourceTokensEstimate: sourceTokens, SummaryTokensEstimate: res.Usage.ResultSummaryTokens, RecentMessagesLimit: a.recentLimit, Version: version}
		res.Usage.ThroughMessageID = next.LastMessageID
		res.Usage.SummarySaved = true
		if err := store.SaveSummary(ctx, previous, next, res.Usage); err != nil {
			return nil, contextMetrics{}, fmt.Errorf("save dialogue summary: %w", err)
		}
		state = &next
	}
	state, err = store.LoadSummaryState(ctx)
	if err != nil {
		return nil, contextMetrics{}, err
	}
	result := make([]domain.ChatMessage, 0, a.recentLimit+2)
	metrics := contextMetrics{compacted: state != nil}
	if state != nil {
		metrics.summary = state.SummaryTokensEstimate
		metrics.uncompressed = state.SourceTokensEstimate
	}
	for _, m := range recent {
		metrics.recent += estimateTokens(m.Role, m.Content)
	}
	if state != nil {
		metrics.uncompressed += metrics.recent
	}
	if state != nil {
		result = append(result, domain.ChatMessage{Role: "system", Content: "DIALOGUE SUMMARY\n\n" + state.Content})
	}
	result = append(result, recent...)
	return result, metrics, nil
}

func (a *Agent) History(ctx context.Context) ([]domain.ChatMessage, error) {
	if a.history == nil {
		return []domain.ChatMessage{}, nil
	}
	return a.history.Load(ctx)
}
