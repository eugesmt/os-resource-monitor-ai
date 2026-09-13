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
			u.usage_inconsistent, u.context_status, u.context_window_tokens, u.history_truncated
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
		var model, component, contextStatus sql.NullString
		var current, history, systemPrompt, estimatedInput, input, output, total, contextWindow, available, inconsistent, truncated sql.NullInt64
		var cost sql.NullFloat64
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &assistantID, &model, &current, &history, &systemPrompt, &estimatedInput, &input, &output, &total, &cost, &component, &available, &inconsistent, &contextStatus, &contextWindow, &truncated); err != nil {
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
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_call_usage (
		assistant_message_id, user_message_id, model, current_request_tokens, history_tokens,
		system_prompt_tokens, estimated_input_tokens, input_tokens, output_tokens, total_tokens,
		estimated_cost_usd, component_token_count, usage_available, usage_inconsistent,
		context_status, context_window_tokens, history_truncated
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		assistantID, userID, usage.Model, usage.CurrentRequestTokens, usage.HistoryTokens,
		usage.SystemPromptTokens, usage.EstimatedInputTokens, nullableInt(usage.InputTokens), nullableInt(usage.OutputTokens), nullableInt(usage.TotalTokens),
		nullableFloat(usage.EstimatedCostUSD), usage.ComponentTokenCount, boolInt(usage.UsageAvailable), boolInt(usage.UsageInconsistent),
		usage.ContextStatus, nullableInt(usage.ContextWindowTokens), boolInt(usage.HistoryTruncated)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *ChatHistory) Close() error { return s.db.Close() }

func intPtr(v int) *int           { return &v }
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
