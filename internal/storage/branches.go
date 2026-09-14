package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"taskmanager-ai/internal/domain"
)

var ErrBranchNotFound = errors.New("branch not found")
var ErrStaleBranchVersion = errors.New("active branch version is stale")

func (s *ChatHistory) GetActiveScope(ctx context.Context) (domain.ContextScope, error) {
	var scope domain.ContextScope
	err := s.db.QueryRowContext(ctx, `SELECT conversation_id, active_branch_id, version FROM conversation_state LIMIT 1`).Scan(&scope.ConversationID, &scope.BranchID, &scope.Version)
	if err != nil {
		return scope, fmt.Errorf("load active dialogue scope: %w", err)
	}
	return scope, nil
}

func (s *ChatHistory) ResolveScope(ctx context.Context, branchID int64) (domain.ContextScope, error) {
	if branchID == 0 {
		return s.GetActiveScope(ctx)
	}
	var scope domain.ContextScope
	err := s.db.QueryRowContext(ctx, `SELECT conversation_id, id, (SELECT version FROM conversation_state WHERE conversation_id=b.conversation_id) FROM dialogue_branches b WHERE id=?`, branchID).Scan(&scope.ConversationID, &scope.BranchID, &scope.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, ErrBranchNotFound
	}
	return scope, err
}

func (s *ChatHistory) LoadLineage(ctx context.Context, scope domain.ContextScope) ([]domain.ChatMessage, error) {
	type segment struct{ branch, cutoff int64 }
	segments := []segment{{branch: scope.BranchID}}
	seen := map[int64]bool{scope.BranchID: true}
	for {
		var parent, checkpoint, message int64
		err := s.db.QueryRowContext(ctx, `SELECT o.parent_branch_id, o.checkpoint_id, c.message_id FROM dialogue_branch_origins o JOIN dialogue_checkpoints c ON c.id=o.checkpoint_id WHERE o.child_branch_id=?`, segments[len(segments)-1].branch).Scan(&parent, &checkpoint, &message)
		_ = checkpoint
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
		if seen[parent] {
			return nil, errors.New("branch lineage cycle")
		}
		seen[parent] = true
		segments[len(segments)-1].cutoff = message
		segments = append(segments, segment{branch: parent})
		if len(segments) > 64 {
			return nil, errors.New("branch lineage exceeds maximum depth")
		}
	}
	result := make([]domain.ChatMessage, 0)
	for i := len(segments) - 1; i >= 0; i-- {
		seg := segments[i]
		query := `SELECT m.id,m.role,m.content FROM chat_messages m JOIN chat_message_branches b ON b.message_id=m.id WHERE b.conversation_id=? AND b.branch_id=?`
		args := []any{scope.ConversationID, seg.branch}
		if seg.cutoff != 0 {
			query += ` AND m.id <= ?`
			args = append(args, seg.cutoff)
		}
		query += ` ORDER BY m.id`
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m domain.ChatMessage
			if err := rows.Scan(&m.ID, &m.Role, &m.Content); err != nil {
				rows.Close()
				return nil, err
			}
			m.ConversationID = scope.ConversationID
			m.BranchID = seg.branch
			result = append(result, m)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}

func (s *ChatHistory) LoadRecentLineage(ctx context.Context, scope domain.ContextScope, limit int) ([]domain.ChatMessage, error) {
	items, err := s.LoadLineage(ctx, scope)
	if err != nil {
		return nil, err
	}
	if limit < 0 {
		limit = 0
	}
	if len(items) <= limit {
		return items, nil
	}
	return items[len(items)-limit:], nil
}

func (s *ChatHistory) ListBranches(ctx context.Context, conversationID int64) ([]domain.DialogueBranch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.id,b.conversation_id,COALESCE(b.name,''),b.created_at,COALESCE(o.parent_branch_id,0),COALESCE(o.checkpoint_id,0) FROM dialogue_branches b LEFT JOIN dialogue_branch_origins o ON o.child_branch_id=b.id WHERE b.conversation_id=? ORDER BY b.id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.DialogueBranch
	for rows.Next() {
		var b domain.DialogueBranch
		if err := rows.Scan(&b.ID, &b.ConversationID, &b.Name, &b.CreatedAt, &b.ParentBranchID, &b.CheckpointID); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

func (s *ChatHistory) CreateCheckpoint(ctx context.Context, conversationID, branchID, messageID int64, label string) (domain.DialogueCheckpoint, error) {
	var role string
	if err := s.db.QueryRowContext(ctx, `SELECT m.role FROM chat_messages m JOIN chat_message_branches b ON b.message_id=m.id WHERE m.id=? AND b.conversation_id=? AND b.branch_id=?`, messageID, conversationID, branchID).Scan(&role); err != nil {
		return domain.DialogueCheckpoint{}, err
	}
	if role != domain.ChatRoleAssistant {
		return domain.DialogueCheckpoint{}, errors.New("checkpoint must reference assistant message")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `INSERT INTO dialogue_checkpoints(conversation_id,branch_id,message_id,label,created_at) VALUES(?,?,?,?,?)`, conversationID, branchID, messageID, label, now)
	if err != nil {
		return domain.DialogueCheckpoint{}, err
	}
	id, _ := res.LastInsertId()
	return domain.DialogueCheckpoint{ID: id, ConversationID: conversationID, BranchID: branchID, MessageID: messageID, Label: label, CreatedAt: now}, nil
}

func (s *ChatHistory) CreateBranch(ctx context.Context, conversationID, checkpointID int64, name string, activate bool) (domain.DialogueBranch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.DialogueBranch{}, err
	}
	defer tx.Rollback()
	var parent int64
	if err = tx.QueryRowContext(ctx, `SELECT branch_id FROM dialogue_checkpoints WHERE id=? AND conversation_id=?`, checkpointID, conversationID).Scan(&parent); err != nil {
		return domain.DialogueBranch{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `INSERT INTO dialogue_branches(conversation_id,name,created_at) VALUES(?,?,?)`, conversationID, name, now)
	if err != nil {
		return domain.DialogueBranch{}, err
	}
	id, _ := res.LastInsertId()
	if _, err = tx.ExecContext(ctx, `INSERT INTO dialogue_branch_origins(child_branch_id,conversation_id,parent_branch_id,checkpoint_id) VALUES(?,?,?,?)`, id, conversationID, parent, checkpointID); err != nil {
		return domain.DialogueBranch{}, err
	}
	if activate {
		if _, err = tx.ExecContext(ctx, `UPDATE conversation_state SET active_branch_id=?,version=version+1 WHERE conversation_id=?`, id, conversationID); err != nil {
			return domain.DialogueBranch{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.DialogueBranch{}, err
	}
	return domain.DialogueBranch{ID: id, ConversationID: conversationID, Name: name, ParentBranchID: parent, CheckpointID: checkpointID, CreatedAt: now}, nil
}

func (s *ChatHistory) SetActiveBranch(ctx context.Context, conversationID, branchID, expectedVersion int64) (domain.ContextScope, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE conversation_state SET active_branch_id=?,version=version+1 WHERE conversation_id=? AND version=? AND EXISTS(SELECT 1 FROM dialogue_branches WHERE id=? AND conversation_id=?)`, branchID, conversationID, expectedVersion, branchID, conversationID)
	if err != nil {
		return domain.ContextScope{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.ContextScope{}, ErrStaleBranchVersion
	}
	return s.GetActiveScope(ctx)
}

func (s *ChatHistory) ListFacts(ctx context.Context, conversationID, branchID int64) ([]domain.DialogueFact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,conversation_id,branch_id,fact_key,category,COALESCE(value_text,''),state,source_message_id,version,updated_at,COALESCE(expires_at,''),inherited_from_fact_id FROM dialogue_facts WHERE conversation_id=? AND branch_id=? ORDER BY fact_key`, conversationID, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.DialogueFact
	for rows.Next() {
		var f domain.DialogueFact
		if err := rows.Scan(&f.ID, &f.ConversationID, &f.BranchID, &f.FactKey, &f.Category, &f.ValueText, &f.State, &f.SourceMessageID, &f.Version, &f.UpdatedAt, &f.ExpiresAt, &f.InheritedFromFactID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func normalizeFactKey(key string) string { return strings.ToLower(strings.TrimSpace(key)) }
