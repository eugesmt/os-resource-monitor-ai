package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"taskmanager-ai/internal/domain"

	_ "modernc.org/sqlite"
)

const DefaultConversationID = "default"

var ErrInvalidEventRole = errors.New("invalid conversation event role")

type ConversationStore struct{ db *sql.DB }

func NewConversationStore(path string) (*ConversationStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("conversation database path is empty")
	}
	dsn := path
	if strings.Contains(dsn, "?") {
		dsn += "&_pragma=foreign_keys(1)"
	} else {
		dsn += "?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open conversation database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &ConversationStore{db: db}
	if err := store.verify(ctxBackground()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func OpenConversationStore(path string) (*ConversationStore, error) {
	return NewConversationStore(path)
}

func ctxBackground() context.Context { return context.Background() }

func (s *ConversationStore) verify(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping conversation database: %w", err)
	}
	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify conversation foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		return errors.New("conversation database foreign keys are disabled")
	}
	wantColumns := map[string][]string{
		"conversations":       {"id", "created_at"},
		"conversation_events": {"id", "conversation_id", "turn_id", "role", "content", "snapshot_id", "created_at"},
	}
	for table, columns := range wantColumns {
		found, err := tableColumns(ctx, s.db, table)
		if err != nil {
			return fmt.Errorf("verify conversation schema: %w", err)
		}
		for _, column := range columns {
			if !found[column] {
				return fmt.Errorf("conversation schema is missing %s.%s; apply migrations with goose before starting", table, column)
			}
		}
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversations WHERE id=?`, DefaultConversationID).Scan(&exists); err != nil {
		return fmt.Errorf("verify default conversation: %w", err)
	}
	if exists != 1 {
		return errors.New("default conversation is missing; apply migrations with goose before starting")
	}
	return nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		found[name] = true
	}
	return found, rows.Err()
}

func (s *ConversationStore) AppendUser(ctx context.Context, conversationID, turnID, content, snapshotID string) (domain.ConversationEvent, error) {
	return s.append(ctx, conversationID, turnID, domain.EventRoleUser, content, snapshotID)
}

func (s *ConversationStore) AppendAssistant(ctx context.Context, conversationID, turnID, content, snapshotID string) (domain.ConversationEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ConversationEvent{}, err
	}
	defer tx.Rollback()
	var count int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversation_events WHERE conversation_id=? AND turn_id=? AND role='user' AND snapshot_id=?`, conversationID, turnID, snapshotID).Scan(&count)
	if err != nil {
		return domain.ConversationEvent{}, err
	}
	if count != 1 {
		return domain.ConversationEvent{}, errors.New("assistant event requires matching user event")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversation_events WHERE conversation_id=? AND turn_id=? AND role='assistant'`, conversationID, turnID).Scan(&count); err != nil {
		return domain.ConversationEvent{}, err
	}
	if count != 0 {
		return domain.ConversationEvent{}, errors.New("assistant event already exists")
	}
	event, err := insertEvent(ctx, tx, conversationID, turnID, domain.EventRoleAssistant, content, snapshotID)
	if err != nil {
		return domain.ConversationEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ConversationEvent{}, err
	}
	return event, nil
}

func (s *ConversationStore) append(ctx context.Context, conversationID, turnID string, role domain.EventRole, content, snapshotID string) (domain.ConversationEvent, error) {
	if role != domain.EventRoleUser && role != domain.EventRoleAssistant {
		return domain.ConversationEvent{}, ErrInvalidEventRole
	}
	event, err := insertEvent(ctx, s.db, conversationID, turnID, role, content, snapshotID)
	return event, err
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertEvent(ctx context.Context, db execer, conversationID, turnID string, role domain.EventRole, content, snapshotID string) (domain.ConversationEvent, error) {
	result, err := db.ExecContext(ctx, `INSERT INTO conversation_events (conversation_id, turn_id, role, content, snapshot_id) VALUES (?, ?, ?, ?, ?)`, conversationID, turnID, role, content, snapshotID)
	if err != nil {
		return domain.ConversationEvent{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.ConversationEvent{}, err
	}
	var event domain.ConversationEvent
	err = scanConversationEvent(db.QueryRowContext(ctx, `SELECT id, conversation_id, turn_id, role, content, snapshot_id, created_at FROM conversation_events WHERE id=?`, id), &event)
	return event, err
}

type rowScanner interface {
	Scan(...any) error
}

func scanConversationEvent(row rowScanner, event *domain.ConversationEvent) error {
	var createdAt string
	if err := row.Scan(&event.ID, &event.ConversationID, &event.TurnID, &event.Role, &event.Content, &event.SnapshotID, &createdAt); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return fmt.Errorf("parse conversation event created_at: %w", err)
	}
	event.CreatedAt = parsed
	return nil
}

func (s *ConversationStore) ListEvents(ctx context.Context, conversationID string) ([]domain.ConversationEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, conversation_id, turn_id, role, content, snapshot_id, created_at FROM conversation_events WHERE conversation_id=? ORDER BY id ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.ConversationEvent, 0)
	for rows.Next() {
		var event domain.ConversationEvent
		if err := scanConversationEvent(rows, &event); err != nil {
			return nil, err
		}
		items = append(items, event)
	}
	return items, rows.Err()
}

func (s *ConversationStore) ListRecentEvents(ctx context.Context, conversationID string, beforeEventID int64, limit int) ([]domain.ConversationEvent, error) {
	if limit <= 0 || beforeEventID <= 0 {
		return []domain.ConversationEvent{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, turn_id, role, content, snapshot_id, created_at
		FROM (
			SELECT id, conversation_id, turn_id, role, content, snapshot_id, created_at
			FROM conversation_events
			WHERE conversation_id=? AND id<?
			ORDER BY id DESC
			LIMIT ?
		)
		ORDER BY id ASC`, conversationID, beforeEventID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.ConversationEvent, 0, limit)
	for rows.Next() {
		var event domain.ConversationEvent
		if err := scanConversationEvent(rows, &event); err != nil {
			return nil, err
		}
		items = append(items, event)
	}
	return items, rows.Err()
}

func (s *ConversationStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
