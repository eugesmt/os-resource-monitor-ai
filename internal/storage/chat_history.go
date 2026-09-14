package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"taskmanager-ai/internal/domain"

	_ "modernc.org/sqlite"
)

type ChatHistory struct{ db *sql.DB }

func OpenChatHistory(path string) (*ChatHistory, error) {
	dsn := sqliteDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open chat database: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping chat database: %w", err)
	}
	var exists int
	if err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='chat_messages'`).Scan(&exists); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("chat schema is not migrated: %w", err)
	}
	var missing []string
	for _, table := range []string{"conversation_state", "dialogue_branches", "chat_message_branches"} {
		if err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			missing = append(missing, table)
		}
	}
	if len(missing) > 0 {
		_ = db.Close()
		return nil, fmt.Errorf("chat schema is missing context strategy tables: %s; apply Goose migrations with 'goose -dir migrations sqlite3 <db> up'", strings.Join(missing, ", "))
	}
	return &ChatHistory{db: db}, nil
}

func sqliteDSN(path string) string {
	if strings.Contains(path, "_pragma=foreign_keys") {
		return path
	}
	if path == ":memory:" {
		return "file::memory:?cache=shared&_pragma=foreign_keys(1)"
	}
	prefix := path
	if !strings.HasPrefix(prefix, "file:") {
		prefix = "file:" + prefix
	}
	separator := "?"
	if strings.Contains(prefix, "?") {
		separator = "&"
	}
	return prefix + separator + "_pragma=foreign_keys(1)"
}

func (s *ChatHistory) Load(ctx context.Context) ([]domain.ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.role, m.content,
			u.assistant_message_id, u.model, u.current_request_tokens, u.history_tokens,
			u.system_prompt_tokens, u.estimated_input_tokens, u.input_tokens, u.output_tokens,
			u.total_tokens, u.estimated_cost_usd, u.component_token_count, u.usage_available,
			u.usage_inconsistent, u.context_status, u.context_window_tokens, u.history_truncated,
			u.summary_tokens, u.recent_history_tokens, u.uncompressed_history_tokens,
			u.estimated_uncompressed_input_tokens, u.estimated_tokens_saved, u.history_compacted,
			u.context_strategy, u.branch_id, u.provider_message_count, u.history_message_count,
			u.service_context_tokens, u.facts_count, u.facts_tokens, u.branch_metadata_tokens,
			u.auxiliary_input_tokens, u.auxiliary_output_tokens, u.auxiliary_total_tokens, u.auxiliary_cost_usd,
			u.aggregate_input_tokens, u.aggregate_output_tokens, u.aggregate_total_tokens, u.aggregate_cost_usd
		FROM chat_messages AS m
		LEFT JOIN ai_call_usage AS u ON u.assistant_message_id = m.id
		ORDER BY m.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []domain.ChatMessage
	for rows.Next() {
		var message domain.ChatMessage
		var assistantID sql.NullInt64
		var model, component, contextStatus, strategy sql.NullString
		var current, history, systemPrompt, estimatedInput, input, output, total, contextWindow, available, inconsistent, truncated, summaryTokens, recentTokens, uncompressedTokens, estimatedUncompressed, saved, compacted sql.NullInt64
		var branch, providerMessages, historyMessages, serviceTokens, factsCount, factsTokens, metadataTokens, auxInput, auxOutput, auxTotal, aggregateInput, aggregateOutput, aggregateTotal sql.NullInt64
		var cost, auxCost, aggregateCost sql.NullFloat64
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &assistantID, &model, &current, &history, &systemPrompt, &estimatedInput, &input, &output, &total, &cost, &component, &available, &inconsistent, &contextStatus, &contextWindow, &truncated, &summaryTokens, &recentTokens, &uncompressedTokens, &estimatedUncompressed, &saved, &compacted, &strategy, &branch, &providerMessages, &historyMessages, &serviceTokens, &factsCount, &factsTokens, &metadataTokens, &auxInput, &auxOutput, &auxTotal, &auxCost, &aggregateInput, &aggregateOutput, &aggregateTotal, &aggregateCost); err != nil {
			return nil, err
		}
		if assistantID.Valid {
			usage := &domain.AICallUsage{
				Model:                model.String,
				CurrentRequestTokens: int(current.Int64),
				HistoryTokens:        int(history.Int64),
				SystemPromptTokens:   int(systemPrompt.Int64),
				EstimatedInputTokens: int(estimatedInput.Int64),
				ComponentTokenCount:  component.String,
				UsageAvailable:       available.Int64 != 0,
				UsageInconsistent:    inconsistent.Int64 != 0,
				ContextStatus:        contextStatus.String,
				HistoryTruncated:     truncated.Int64 != 0,
				ContextStrategy:      domain.ContextStrategyName(strategy.String), BranchID: branch.Int64,
				ProviderMessageCount: int(providerMessages.Int64), HistoryMessageCount: int(historyMessages.Int64), ServiceContextTokens: int(serviceTokens.Int64), FactsCount: int(factsCount.Int64), FactsTokens: int(factsTokens.Int64), BranchMetadataTokens: int(metadataTokens.Int64),
				SummaryTokens: int(summaryTokens.Int64), RecentHistoryTokens: int(recentTokens.Int64), UncompressedHistoryTokens: int(uncompressedTokens.Int64), EstimatedUncompressedInputTokens: int(estimatedUncompressed.Int64), EstimatedTokensSaved: int(saved.Int64), HistoryCompacted: compacted.Int64 != 0,
			}
			if input.Valid {
				usage.InputTokens = intPtr(int(input.Int64))
			}
			if output.Valid {
				usage.OutputTokens = intPtr(int(output.Int64))
			}
			if total.Valid {
				usage.TotalTokens = intPtr(int(total.Int64))
			}
			if cost.Valid {
				usage.EstimatedCostUSD = floatPtr(cost.Float64)
			}
			if contextWindow.Valid {
				usage.ContextWindowTokens = intPtr(int(contextWindow.Int64))
			}
			if auxInput.Valid {
				usage.AuxiliaryInputTokens = intPtr(int(auxInput.Int64))
			}
			if auxOutput.Valid {
				usage.AuxiliaryOutputTokens = intPtr(int(auxOutput.Int64))
			}
			if auxTotal.Valid {
				usage.AuxiliaryTotalTokens = intPtr(int(auxTotal.Int64))
			}
			if auxCost.Valid {
				usage.AuxiliaryCostUSD = floatPtr(auxCost.Float64)
			}
			if aggregateInput.Valid {
				usage.AggregateInputTokens = intPtr(int(aggregateInput.Int64))
			}
			if aggregateOutput.Valid {
				usage.AggregateOutputTokens = intPtr(int(aggregateOutput.Int64))
			}
			if aggregateTotal.Valid {
				usage.AggregateTotalTokens = intPtr(int(aggregateTotal.Int64))
			}
			if aggregateCost.Valid {
				usage.AggregateCostUSD = floatPtr(aggregateCost.Float64)
			}
			message.Usage = usage
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *ChatHistory) AppendTurn(ctx context.Context, userContent, assistantContent string, usage domain.AICallUsage) error {
	scope, err := s.GetActiveScope(ctx)
	if err != nil {
		return err
	}
	return s.AppendTurnInBranch(ctx, scope, userContent, assistantContent, usage)
}

func (s *ChatHistory) AppendTurnInBranch(ctx context.Context, scope domain.ContextScope, userContent, assistantContent string, usage domain.AICallUsage) error {
	return s.appendTurnInBranch(ctx, scope, userContent, assistantContent, usage, nil)
}

func (s *ChatHistory) AppendTurnInBranchWithFacts(ctx context.Context, scope domain.ContextScope, userContent, assistantContent string, usage domain.AICallUsage, mutations []domain.FactMutation) error {
	return s.appendTurnInBranch(ctx, scope, userContent, assistantContent, usage, mutations)
}

func (s *ChatHistory) appendTurnInBranch(ctx context.Context, scope domain.ContextScope, userContent, assistantContent string, usage domain.AICallUsage, mutations []domain.FactMutation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	userResult, err := tx.ExecContext(ctx, `INSERT INTO chat_messages (role, content) VALUES (?, ?)`, domain.ChatRoleUser, userContent)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	userID, err := userResult.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	assistantResult, err := tx.ExecContext(ctx, `INSERT INTO chat_messages (role, content) VALUES (?, ?)`, domain.ChatRoleAssistant, assistantContent)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	assistantID, err := assistantResult.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO chat_message_branches(message_id, conversation_id, branch_id) VALUES (?, ?, ?), (?, ?, ?)`, userID, scope.ConversationID, scope.BranchID, assistantID, scope.ConversationID, scope.BranchID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_call_usage (
		assistant_message_id, user_message_id, model, current_request_tokens, history_tokens,
		system_prompt_tokens, estimated_input_tokens, input_tokens, output_tokens, total_tokens,
		estimated_cost_usd, component_token_count, usage_available, usage_inconsistent,
			context_status, context_window_tokens, history_truncated, summary_tokens, recent_history_tokens, uncompressed_history_tokens, estimated_uncompressed_input_tokens, estimated_tokens_saved, history_compacted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		assistantID, userID, usage.Model, usage.CurrentRequestTokens, usage.HistoryTokens,
		usage.SystemPromptTokens, usage.EstimatedInputTokens, nullableInt(usage.InputTokens), nullableInt(usage.OutputTokens), nullableInt(usage.TotalTokens),
		nullableFloat(usage.EstimatedCostUSD), usage.ComponentTokenCount, boolInt(usage.UsageAvailable), boolInt(usage.UsageInconsistent),
		usage.ContextStatus, nullableInt(usage.ContextWindowTokens), boolInt(usage.HistoryTruncated), nullableInt(&usage.SummaryTokens), nullableInt(&usage.RecentHistoryTokens), nullableInt(&usage.UncompressedHistoryTokens), nullableInt(&usage.EstimatedUncompressedInputTokens), nullableInt(&usage.EstimatedTokensSaved), boolInt(usage.HistoryCompacted)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_call_usage SET context_strategy=?, branch_id=?, provider_message_count=?, history_message_count=?, service_context_tokens=?, facts_count=?, facts_tokens=?, branch_metadata_tokens=?, auxiliary_input_tokens=?, auxiliary_output_tokens=?, auxiliary_total_tokens=?, auxiliary_cost_usd=?, aggregate_input_tokens=?, aggregate_output_tokens=?, aggregate_total_tokens=?, aggregate_cost_usd=? WHERE assistant_message_id=?`,
		usage.ContextStrategy, nullableInt(nonZeroPtr(usage.BranchID)), usage.ProviderMessageCount, usage.HistoryMessageCount, usage.ServiceContextTokens, usage.FactsCount, usage.FactsTokens, usage.BranchMetadataTokens,
		nullableInt(usage.AuxiliaryInputTokens), nullableInt(usage.AuxiliaryOutputTokens), nullableInt(usage.AuxiliaryTotalTokens), nullableFloat(usage.AuxiliaryCostUSD), nullableInt(usage.AggregateInputTokens), nullableInt(usage.AggregateOutputTokens), nullableInt(usage.AggregateTotalTokens), nullableFloat(usage.AggregateCostUSD), assistantID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if len(mutations) > 0 {
		if err := applyFactMutationsTx(ctx, tx, scope, userID, mutations); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *ChatHistory) Close() error { return s.db.Close() }

func intPtr(v int) *int { return &v }
func nonZeroPtr(v int64) *int {
	if v == 0 {
		return nil
	}
	n := int(v)
	return &n
}
func floatPtr(v float64) *float64 { return &v }
func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}
func nullableFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
