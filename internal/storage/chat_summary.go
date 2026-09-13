package storage

import (
	"context"
	"database/sql"
	"fmt"

	"taskmanager-ai/internal/domain"
)

func (s *ChatHistory) LoadSummaryState(ctx context.Context) (*domain.DialogueSummary, error) {
	var v domain.DialogueSummary
	err := s.db.QueryRowContext(ctx, `SELECT content,last_message_id,updated_at,source_message_count,source_tokens_estimate,summary_tokens_estimate,recent_messages_limit,version FROM conversation_summary WHERE id=1`).Scan(&v.Content, &v.LastMessageID, &v.UpdatedAt, &v.SourceMessageCount, &v.SourceTokensEstimate, &v.SummaryTokensEstimate, &v.RecentMessagesLimit, &v.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *ChatHistory) LoadSummaryBatch(ctx context.Context, after, before int64, limit int) ([]domain.ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,role,content FROM chat_messages WHERE id>? AND id<? ORDER BY id ASC LIMIT ?`, after, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ChatMessage
	for rows.Next() {
		var m domain.ChatMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result)%2 != 0 {
		return nil, fmt.Errorf("summary batch splits a turn")
	}
	for i := 0; i < len(result); i += 2 {
		if result[i].Role != domain.ChatRoleUser || result[i+1].Role != domain.ChatRoleAssistant {
			return nil, fmt.Errorf("invalid chat role sequence")
		}
	}
	return result, nil
}

func (s *ChatHistory) LoadRecentMessages(ctx context.Context, limit int) ([]domain.ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,role,content FROM chat_messages ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reverse []domain.ChatMessage
	for rows.Next() {
		var m domain.ChatMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content); err != nil {
			return nil, err
		}
		reverse = append(reverse, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]domain.ChatMessage, len(reverse))
	for i := range reverse {
		result[len(reverse)-1-i] = reverse[i]
	}
	if len(result)%2 != 0 {
		return nil, fmt.Errorf("recent window splits a turn")
	}
	for i := 0; i < len(result); i += 2 {
		if result[i].Role != domain.ChatRoleUser || result[i+1].Role != domain.ChatRoleAssistant {
			return nil, fmt.Errorf("invalid chat role sequence")
		}
	}
	return result, nil
}

func (s *ChatHistory) SaveSummary(ctx context.Context, previous *domain.DialogueSummary, summary domain.DialogueSummary, usage domain.SummaryCallUsage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if previous == nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO conversation_summary (id,content,last_message_id,updated_at,source_message_count,source_tokens_estimate,summary_tokens_estimate,recent_messages_limit,version) VALUES (1,?,?,?,?,?,?,?,?)`, summary.Content, summary.LastMessageID, summary.UpdatedAt, summary.SourceMessageCount, summary.SourceTokensEstimate, summary.SummaryTokensEstimate, summary.RecentMessagesLimit, summary.Version); err != nil {
			return err
		}
	} else {
		res, e := tx.ExecContext(ctx, `UPDATE conversation_summary SET content=?,last_message_id=?,updated_at=?,source_message_count=?,source_tokens_estimate=?,summary_tokens_estimate=?,recent_messages_limit=?,version=? WHERE id=1 AND version=? AND last_message_id=?`, summary.Content, summary.LastMessageID, summary.UpdatedAt, summary.SourceMessageCount, summary.SourceTokensEstimate, summary.SummaryTokensEstimate, summary.RecentMessagesLimit, summary.Version, previous.Version, previous.LastMessageID)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return fmt.Errorf("summary CAS conflict")
		}
	}
	if err = insertSummaryUsage(ctx, tx, usage); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ChatHistory) RecordSummaryAttempt(ctx context.Context, usage domain.SummaryCallUsage) error {
	return s.recordSummaryUsage(ctx, usage)
}
func insertSummaryUsage(ctx context.Context, tx *sql.Tx, usage domain.SummaryCallUsage) error {
	q := `INSERT INTO ai_summary_usage (started_at,finished_at,previous_last_message_id,through_message_id,message_count,model,outcome,previous_summary_tokens,new_messages_tokens,estimated_input_tokens,input_tokens,output_tokens,total_tokens,result_summary_tokens,summary_saved) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	args := []any{usage.StartedAt, usage.FinishedAt, usage.PreviousLastMessageID, usage.ThroughMessageID, usage.MessageCount, usage.Model, usage.Outcome, usage.PreviousSummaryTokens, usage.NewMessagesTokens, usage.EstimatedInputTokens, nullableInt(usage.InputTokens), nullableInt(usage.OutputTokens), nullableInt(usage.TotalTokens), usage.ResultSummaryTokens, boolInt(usage.SummarySaved)}
	if tx != nil {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}
	return fmt.Errorf("summary usage transaction is nil")
}

// execDB is supplied by the receiver through this small helper-free variant.
func (s *ChatHistory) recordSummaryUsage(ctx context.Context, usage domain.SummaryCallUsage) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_summary_usage (started_at,finished_at,previous_last_message_id,through_message_id,message_count,model,outcome,previous_summary_tokens,new_messages_tokens,estimated_input_tokens,input_tokens,output_tokens,total_tokens,result_summary_tokens,summary_saved) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, usage.StartedAt, usage.FinishedAt, usage.PreviousLastMessageID, usage.ThroughMessageID, usage.MessageCount, usage.Model, usage.Outcome, usage.PreviousSummaryTokens, usage.NewMessagesTokens, usage.EstimatedInputTokens, nullableInt(usage.InputTokens), nullableInt(usage.OutputTokens), nullableInt(usage.TotalTokens), usage.ResultSummaryTokens, boolInt(usage.SummarySaved))
	return err
}
