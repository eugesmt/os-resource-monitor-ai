package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/memory"
	"uuid"

	_ "modernc.org/sqlite"
)

const DefaultConversationID = "default"

var ErrInvalidEventRole = errors.New("invalid conversation event role")
var ErrTaskStateVersionConflict = errors.New("task state version conflict")

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
	wantColumns := []struct {
		table   string
		columns []string
	}{
		{table: "conversations", columns: []string{"id", "created_at"}},
		{table: "conversation_events", columns: []string{"id", "conversation_id", "turn_id", "role", "content", "snapshot_id", "created_at"}},
		{table: "memory_records", columns: []string{"id", "layer", "scope", "kind", "content", "created_at"}},
		{table: "profiles", columns: []string{"user_id", "style", "format", "constraints_json"}},
		{table: "tasks", columns: []string{"task_id", "conversation_id", "goal", "phase", "step", "expected_action_kind", "expected_action", "status", "outcome", "last_error", "response_type", "version", "created_at", "updated_at"}},
	}
	for _, expected := range wantColumns {
		table, columns := expected.table, expected.columns
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

var ErrTaskNotFound = errors.New("task not found")
var ErrNoOpenTask = errors.New("no open task")

func (s *ConversationStore) CreateTask(ctx context.Context, conversationID string) (domain.TaskState, error) {
	if strings.TrimSpace(conversationID) == "" {
		return domain.TaskState{}, errors.New("conversation id is required")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO conversations (id) VALUES (?)`, conversationID); err != nil {
		return domain.TaskState{}, err
	}
	state := domain.NewTaskState("task-" + uuid.NewV4().String())
	state.ExpectedActionKind = domain.TaskActionKindAgent
	_, err := s.db.ExecContext(ctx, `INSERT INTO tasks (task_id, conversation_id, phase, step, expected_action_kind, expected_action, status, outcome, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)`, state.TaskID, conversationID, state.Phase, state.Step, state.ExpectedActionKind, state.ExpectedAction, state.Status, state.Outcome)
	if err != nil {
		return domain.TaskState{}, err
	}
	return s.GetTask(ctx, state.TaskID)
}

func (s *ConversationStore) GetOpenTask(ctx context.Context, conversationID string) (domain.TaskState, error) {
	return s.getTask(ctx, `SELECT task_id, goal, phase, step, expected_action_kind, expected_action, status, outcome, last_error, response_type, version, updated_at FROM tasks WHERE conversation_id=? AND phase<>? ORDER BY updated_at DESC LIMIT 1`, conversationID, domain.TaskPhaseDone)
}

func (s *ConversationStore) GetLatestTask(ctx context.Context, conversationID string) (domain.TaskState, error) {
	return s.getTask(ctx, `SELECT task_id, goal, phase, step, expected_action_kind, expected_action, status, outcome, last_error, response_type, version, updated_at FROM tasks WHERE conversation_id=? ORDER BY updated_at DESC LIMIT 1`, conversationID)
}

func (s *ConversationStore) GetTask(ctx context.Context, taskID string) (domain.TaskState, error) {
	return s.getTask(ctx, `SELECT task_id, goal, phase, step, expected_action_kind, expected_action, status, outcome, last_error, response_type, version, updated_at FROM tasks WHERE task_id=?`, taskID)
}

func (s *ConversationStore) getTask(ctx context.Context, query string, args ...any) (domain.TaskState, error) {
	var state domain.TaskState
	var updated string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&state.TaskID, &state.Goal, &state.Phase, &state.Step, &state.ExpectedActionKind, &state.ExpectedAction, &state.Status, &state.Outcome, &state.LastError, &state.ResponseType, &state.Version, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TaskState{}, ErrTaskNotFound
	}
	if err != nil {
		return domain.TaskState{}, err
	}
	state.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return domain.TaskState{}, err
	}
	return state, state.Validate()
}

func (s *ConversationStore) SaveTaskTransition(ctx context.Context, taskID, action string, from domain.TaskState, next domain.TaskState, expectedVersion int64) (domain.TaskState, error) {
	if err := next.Validate(); err != nil {
		return domain.TaskState{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TaskState{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET goal=?, phase=?, step=?, expected_action_kind=?, expected_action=?, status=?, outcome=?, last_error=?, response_type=?, version=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE task_id=? AND version=?`, next.Goal, next.Phase, next.Step, next.ExpectedActionKind, next.ExpectedAction, next.Status, next.Outcome, next.LastError, next.ResponseType, expectedVersion+1, taskID, expectedVersion)
	if err != nil {
		return domain.TaskState{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.TaskState{}, err
	}
	if affected != 1 {
		return domain.TaskState{}, ErrTaskStateVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_transition_events (task_id, action, from_phase, to_phase, from_status, to_status, step, expected_action) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, taskID, action, from.Phase, next.Phase, from.Status, next.Status, next.Step, next.ExpectedAction); err != nil {
		return domain.TaskState{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.TaskState{}, err
	}
	return s.GetTask(ctx, taskID)
}

func (s *ConversationStore) ListTaskTransitions(ctx context.Context, taskID string) ([]domain.TaskTransition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, action, from_phase, to_phase, from_status, to_status, step, expected_action, created_at FROM task_transition_events WHERE task_id=? ORDER BY id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.TaskTransition, 0)
	for rows.Next() {
		var item domain.TaskTransition
		var created string
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Action, &item.FromPhase, &item.ToPhase, &item.FromStatus, &item.ToStatus, &item.Step, &item.ExpectedAction, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ConversationStore) SaveMemory(ctx context.Context, record memory.Record) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO memory_records (id, layer, scope, kind, content, created_at) VALUES (?, ?, ?, ?, ?, ?)`, record.ID, record.Layer, record.Scope, record.Kind, record.Content, record.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *ConversationStore) ListMemory(ctx context.Context) ([]memory.Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, layer, scope, kind, content, created_at FROM memory_records ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]memory.Record, 0)
	for rows.Next() {
		var item memory.Record
		var created string
		if err := rows.Scan(&item.ID, &item.Layer, &item.Scope, &item.Kind, &item.Content, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ConversationStore) DeleteMemory(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM memory_records WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return errors.New("memory record not found")
	}
	return nil
}

func (s *ConversationStore) SaveProfile(ctx context.Context, profile memory.Profile) error {
	constraints, err := json.Marshal(profile.Constraints)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO profiles (user_id, style, format, constraints_json)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET style=excluded.style, format=excluded.format, constraints_json=excluded.constraints_json
	`, profile.UserID, profile.Style, profile.Format, string(constraints))
	return err
}

func (s *ConversationStore) ListProfiles(ctx context.Context) ([]memory.Profile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, style, format, constraints_json FROM profiles ORDER BY user_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := make([]memory.Profile, 0)
	for rows.Next() {
		var profile memory.Profile
		var constraints string
		if err := rows.Scan(&profile.UserID, &profile.Style, &profile.Format, &constraints); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(constraints), &profile.Constraints); err != nil {
			return nil, fmt.Errorf("decode profile %q constraints: %w", profile.UserID, err)
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
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

func (s *ConversationStore) CommitAssistantAndTask(ctx context.Context, conversationID, turnID, content, snapshotID, taskID, action string, from, to domain.TaskState) (domain.ConversationEvent, domain.TaskState, error) {
	if err := to.Validate(); err != nil {
		return domain.ConversationEvent{}, domain.TaskState{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ConversationEvent{}, domain.TaskState{}, err
	}
	defer tx.Rollback()
	event, err := insertEvent(ctx, tx, conversationID, turnID, domain.EventRoleAssistant, content, snapshotID)
	if err != nil {
		return domain.ConversationEvent{}, domain.TaskState{}, err
	}
	to.Version = from.Version + 1
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET goal=?, phase=?, step=?, expected_action_kind=?, expected_action=?, status=?, outcome=?, last_error=?, response_type=?, version=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE task_id=? AND version=?`, to.Goal, to.Phase, to.Step, to.ExpectedActionKind, to.ExpectedAction, to.Status, to.Outcome, to.LastError, to.ResponseType, to.Version, taskID, from.Version)
	if err != nil {
		return domain.ConversationEvent{}, domain.TaskState{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		if err != nil {
			return domain.ConversationEvent{}, domain.TaskState{}, err
		}
		return domain.ConversationEvent{}, domain.TaskState{}, ErrTaskStateVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_transition_events (task_id, action, from_phase, to_phase, from_status, to_status, step, expected_action) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, taskID, action, from.Phase, to.Phase, from.Status, to.Status, to.Step, to.ExpectedAction); err != nil {
		return domain.ConversationEvent{}, domain.TaskState{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ConversationEvent{}, domain.TaskState{}, err
	}
	saved, err := s.GetTask(ctx, taskID)
	if err != nil {
		return domain.ConversationEvent{}, domain.TaskState{}, err
	}
	return event, saved, nil
}

func (s *ConversationStore) append(ctx context.Context, conversationID, turnID string, role domain.EventRole, content, snapshotID string) (domain.ConversationEvent, error) {
	if role != domain.EventRoleUser && role != domain.EventRoleAssistant {
		return domain.ConversationEvent{}, ErrInvalidEventRole
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO conversations (id) VALUES (?)`, conversationID); err != nil {
		return domain.ConversationEvent{}, err
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
