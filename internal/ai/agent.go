package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

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
	Complete(ctx context.Context, systemPrompt string, messages []domain.ChatMessage) (string, error)
}

type HistoryStore interface {
	Load(context.Context) ([]domain.ChatMessage, error)
	AppendTurn(context.Context, string, string) error
}

type Agent struct {
	systemPrompt string
	completion   completion
	history      HistoryStore
	mu           sync.Mutex
}

func NewAgent(client *Client, prompt string, history HistoryStore) *Agent {
	return &Agent{completion: client, systemPrompt: strings.TrimSpace(prompt), history: history}
}

func newAgent(completion completion, prompt string, history ...HistoryStore) *Agent {
	var store HistoryStore
	if len(history) > 0 {
		store = history[0]
	}
	return &Agent{completion: completion, systemPrompt: strings.TrimSpace(prompt), history: store}
}

func (a *Agent) Ask(ctx context.Context, question, contextText string) (*domain.AnalysisResponse, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return nil, errors.New("question exceeds 4000 characters")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var messages []domain.ChatMessage
	if a.history != nil {
		var err error
		messages, err = a.history.Load(ctx)
		if err != nil {
			return nil, fmt.Errorf("load chat history: %w", err)
		}
	}

	userContent := fmt.Sprintf("CURRENT REQUEST\n\nSYSTEM CONTEXT (authoritative source for metrics):\n%s\n\nUSER QUESTION:\n%s\n\nUse previous messages only as DIALOGUE HISTORY for understanding the question. Do not use numbers from previous AI answers as system metrics.", contextText, question)
	messages = append(messages, domain.ChatMessage{Role: domain.ChatRoleUser, Content: userContent})
	content, err := a.completion.Complete(ctx, a.systemPrompt, messages)
	if err != nil {
		return nil, err
	}
	var result domain.AnalysisResponse
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if err := validateAnalysisResponse(result, len([]byte(content))); err != nil {
		return nil, err
	}
	if a.history != nil {
		if err := a.history.AppendTurn(ctx, question, content); err != nil {
			return nil, fmt.Errorf("persist chat history: %w", err)
		}
	}
	return &result, nil
}

func (a *Agent) History(ctx context.Context) ([]domain.ChatMessage, error) {
	if a.history == nil {
		return []domain.ChatMessage{}, nil
	}
	return a.history.Load(ctx)
}
