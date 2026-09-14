package ai

import (
	"context"
	"taskmanager-ai/internal/domain"
)

type SlidingWindow struct {
	store branchStore
	limit int
}

func NewSlidingWindow(store branchStore, limit int) *SlidingWindow {
	return &SlidingWindow{store: store, limit: limit}
}
func (m *SlidingWindow) Name() domain.ContextStrategyName { return domain.ContextStrategySlidingWindow }
func (m *SlidingWindow) Prepare(ctx context.Context, scope domain.ContextScope, _ string) (PreparedContext, error) {
	all, err := m.store.LoadLineage(ctx, scope)
	if err != nil {
		return PreparedContext{}, err
	}
	items, err := m.store.LoadRecentLineage(ctx, scope, m.limit)
	if err != nil {
		return PreparedContext{}, err
	}
	return PreparedContext{Messages: items, Scope: scope, Metrics: ContextMetrics{HistoryMessageCount: len(items), HistoryTruncated: len(all) > len(items)}}, nil
}
func (m *SlidingWindow) Commit(ctx context.Context, p PreparedContext, user, assistant string, usage domain.AICallUsage) error {
	return m.store.AppendTurnInBranch(ctx, p.Scope, user, assistant, usage)
}
