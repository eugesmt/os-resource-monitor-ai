package ai

import (
	"context"
	"fmt"

	"taskmanager-ai/internal/domain"
)

type PreparedContext struct {
	Messages       []domain.ChatMessage
	Scope          domain.ContextScope
	Metrics        ContextMetrics
	Mutations      []domain.FactMutation
	AuxiliaryUsage domain.AICallUsage
}

type ContextMetrics struct {
	HistoryMessageCount  int
	ProviderMessageCount int
	ServiceContextTokens int
	FactsCount           int
	FactsTokens          int
	BranchMetadataTokens int
	HistoryTruncated     bool
}

type ContextManager interface {
	Name() domain.ContextStrategyName
	Prepare(context.Context, domain.ContextScope, string) (PreparedContext, error)
	Commit(context.Context, PreparedContext, string, string, domain.AICallUsage) error
}

type branchStore interface {
	GetActiveScope(context.Context) (domain.ContextScope, error)
	LoadRecentLineage(context.Context, domain.ContextScope, int) ([]domain.ChatMessage, error)
	LoadLineage(context.Context, domain.ContextScope) ([]domain.ChatMessage, error)
	AppendTurnInBranch(context.Context, domain.ContextScope, string, string, domain.AICallUsage) error
}

type legacyManager struct {
	history HistoryStore
	limit   int
}

func (m *legacyManager) Name() domain.ContextStrategyName { return domain.ContextStrategySlidingWindow }
func (m *legacyManager) Prepare(ctx context.Context, scope domain.ContextScope, _ string) (PreparedContext, error) {
	items, err := m.history.Load(ctx)
	if err != nil {
		return PreparedContext{}, fmt.Errorf("load chat history: %w", err)
	}
	if len(items) > m.limit {
		items = items[len(items)-m.limit:]
	}
	return PreparedContext{Messages: items, Scope: scope, Metrics: ContextMetrics{HistoryMessageCount: len(items), HistoryTruncated: len(items) == m.limit}}, nil
}
func (m *legacyManager) Commit(ctx context.Context, p PreparedContext, user, assistant string, usage domain.AICallUsage) error {
	return m.history.AppendTurn(ctx, user, assistant, usage)
}
