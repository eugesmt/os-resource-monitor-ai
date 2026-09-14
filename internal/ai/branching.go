package ai

import (
	"context"
	"taskmanager-ai/internal/domain"
)

type Branching struct{ store branchStore }

func NewBranching(store branchStore) *Branching       { return &Branching{store: store} }
func (m *Branching) Name() domain.ContextStrategyName { return domain.ContextStrategyBranching }
func (m *Branching) Prepare(ctx context.Context, scope domain.ContextScope, _ string) (PreparedContext, error) {
	items, err := m.store.LoadLineage(ctx, scope)
	if err != nil {
		return PreparedContext{}, err
	}
	return PreparedContext{Messages: items, Scope: scope, Metrics: ContextMetrics{HistoryMessageCount: len(items)}}, nil
}
func (m *Branching) Commit(ctx context.Context, p PreparedContext, user, assistant string, usage domain.AICallUsage) error {
	return m.store.AppendTurnInBranch(ctx, p.Scope, user, assistant, usage)
}
