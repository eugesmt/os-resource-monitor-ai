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
	systemPrompt string
	completion   completion
	history      HistoryStore
	manager      ContextManager
	mu           sync.Mutex
}

func NewAgent(client *Client, prompt string, history HistoryStore) *Agent {
	return newAgent(client, prompt, history)
}

func NewConfiguredAgent(client *Client, prompt string, history HistoryStore, recentLimit, batchSize, maxSummaryTokens int, summaryModel string) *Agent {
	return newAgent(client, prompt, history)
}

func newAgent(completion completion, prompt string, history ...HistoryStore) *Agent {
	var store HistoryStore
	if len(history) > 0 {
		store = history[0]
	}
	a := &Agent{completion: completion, systemPrompt: strings.TrimSpace(prompt), history: store}
	if store != nil {
		a.manager = &legacyManager{history: store, limit: 20}
	}
	return a
}

func NewAgentWithContextManager(completion completion, prompt string, history HistoryStore, manager ContextManager) *Agent {
	return &Agent{completion: completion, systemPrompt: strings.TrimSpace(prompt), history: history, manager: manager}
}

func (a *Agent) Ask(ctx context.Context, question, contextText string) (*domain.AnalysisResponse, domain.AICallUsage, error) {
	return a.ask(ctx, question, contextText, 0)
}

func (a *Agent) AskOnBranch(ctx context.Context, question, contextText string, branchID int64) (*domain.AnalysisResponse, domain.AICallUsage, error) {
	return a.ask(ctx, question, contextText, branchID)
}

func (a *Agent) ask(ctx context.Context, question, contextText string, branchID int64) (*domain.AnalysisResponse, domain.AICallUsage, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, domain.AICallUsage{}, errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return nil, domain.AICallUsage{}, errors.New("question exceeds 4000 characters")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var prepared PreparedContext
	if a.manager != nil {
		scope := domain.ContextScope{}
		if bs, ok := a.history.(interface {
			GetActiveScope(context.Context) (domain.ContextScope, error)
		}); ok {
			var err error
			scope, err = bs.GetActiveScope(ctx)
			if err != nil {
				return nil, domain.AICallUsage{}, err
			}
			if branchID != 0 {
				resolver, ok := a.history.(interface {
					ResolveScope(context.Context, int64) (domain.ContextScope, error)
				})
				if !ok {
					return nil, domain.AICallUsage{}, errors.New("branch selection unavailable")
				}
				scope, err = resolver.ResolveScope(ctx, branchID)
				if err != nil {
					return nil, domain.AICallUsage{}, err
				}
			}
		}
		var err error
		prepared, err = a.manager.Prepare(ctx, scope, question)
		if err != nil {
			return nil, domain.AICallUsage{}, err
		}
	}
	messages := prepared.Messages

	userContent := fmt.Sprintf("CURRENT REQUEST\n\nSYSTEM CONTEXT (authoritative source for metrics):\n%s\n\nUSER QUESTION:\n%s\n\nUse previous messages only as DIALOGUE HISTORY for understanding the question. Do not use numbers from previous AI answers as system metrics.", contextText, question)
	messages = append(messages, domain.ChatMessage{Role: domain.ChatRoleUser, Content: userContent})
	completion, err := a.completion.Complete(ctx, a.systemPrompt, messages)
	completion.Usage.ContextStrategy = a.managerName()
	completion.Usage.HistoryMessageCount = prepared.Metrics.HistoryMessageCount
	completion.Usage.ProviderMessageCount = len(messages) + 2
	completion.Usage.ServiceContextTokens = prepared.Metrics.ServiceContextTokens
	completion.Usage.FactsCount = prepared.Metrics.FactsCount
	completion.Usage.FactsTokens = prepared.Metrics.FactsTokens
	completion.Usage.BranchMetadataTokens = prepared.Metrics.BranchMetadataTokens
	completion.Usage.HistoryTruncated = prepared.Metrics.HistoryTruncated
	completion.Usage.BranchID = prepared.Scope.BranchID
	if prepared.AuxiliaryUsage.InputTokens != nil {
		completion.Usage.AuxiliaryInputTokens = intPtr(*prepared.AuxiliaryUsage.InputTokens)
	}
	if prepared.AuxiliaryUsage.OutputTokens != nil {
		completion.Usage.AuxiliaryOutputTokens = intPtr(*prepared.AuxiliaryUsage.OutputTokens)
	}
	if prepared.AuxiliaryUsage.TotalTokens != nil {
		completion.Usage.AuxiliaryTotalTokens = intPtr(*prepared.AuxiliaryUsage.TotalTokens)
	}
	if prepared.AuxiliaryUsage.EstimatedCostUSD != nil {
		completion.Usage.AuxiliaryCostUSD = floatPtr(*prepared.AuxiliaryUsage.EstimatedCostUSD)
	}
	if completion.Usage.InputTokens != nil && completion.Usage.AuxiliaryInputTokens != nil {
		v := *completion.Usage.InputTokens + *completion.Usage.AuxiliaryInputTokens
		completion.Usage.AggregateInputTokens = intPtr(v)
	}
	if completion.Usage.OutputTokens != nil && completion.Usage.AuxiliaryOutputTokens != nil {
		v := *completion.Usage.OutputTokens + *completion.Usage.AuxiliaryOutputTokens
		completion.Usage.AggregateOutputTokens = intPtr(v)
	}
	if completion.Usage.TotalTokens != nil && completion.Usage.AuxiliaryTotalTokens != nil {
		v := *completion.Usage.TotalTokens + *completion.Usage.AuxiliaryTotalTokens
		completion.Usage.AggregateTotalTokens = intPtr(v)
	}
	if completion.Usage.EstimatedCostUSD != nil && completion.Usage.AuxiliaryCostUSD != nil {
		v := *completion.Usage.EstimatedCostUSD + *completion.Usage.AuxiliaryCostUSD
		completion.Usage.AggregateCostUSD = floatPtr(v)
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
	if a.manager != nil {
		if err := a.manager.Commit(ctx, prepared, question, completion.Content, completion.Usage); err != nil {
			return nil, completion.Usage, fmt.Errorf("persist chat history: %w", err)
		}
	} else if a.history != nil {
		if err := a.history.AppendTurn(ctx, question, completion.Content, completion.Usage); err != nil {
			return nil, completion.Usage, fmt.Errorf("persist chat history: %w", err)
		}
	}
	return &result, completion.Usage, nil
}

func (a *Agent) managerName() domain.ContextStrategyName {
	if a.manager == nil {
		return ""
	}
	return a.manager.Name()
}
func (a *Agent) ContextStrategy() domain.ContextStrategyName { return a.managerName() }

func (a *Agent) History(ctx context.Context) ([]domain.ChatMessage, error) {
	if a.history == nil {
		return []domain.ChatMessage{}, nil
	}
	return a.history.Load(ctx)
}
