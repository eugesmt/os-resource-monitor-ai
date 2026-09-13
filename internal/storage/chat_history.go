package storage

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
	"taskmanager-ai/internal/domain"
)

type ChatHistory struct { db *sql.DB }

func OpenChatHistory(path string) (*ChatHistory, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil { return nil, fmt.Errorf("open chat database: %w", err) }
	if err := db.Ping(); err != nil { _ = db.Close(); return nil, fmt.Errorf("ping chat database: %w", err) }
	var exists int
	if err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='chat_messages'`).Scan(&exists); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("chat schema is not migrated: %w", err)
	}
	return &ChatHistory{db: db}, nil
}

func (s *ChatHistory) Load(ctx context.Context) ([]domain.ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, role, content FROM chat_messages ORDER BY id ASC`)
	if err != nil { return nil, err }
	defer rows.Close()
	var messages []domain.ChatMessage
	for rows.Next() {
		var message domain.ChatMessage
		if err := rows.Scan(&message.ID, &message.Role, &message.Content); err != nil { return nil, err }
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil { return nil, err }
	return messages, nil
}

func (s *ChatHistory) AppendTurn(ctx context.Context, userContent, assistantContent string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	if _, err = tx.ExecContext(ctx, `INSERT INTO chat_messages (role, content) VALUES (?, ?)`, domain.ChatRoleUser, userContent); err != nil { _ = tx.Rollback(); return err }
	if _, err = tx.ExecContext(ctx, `INSERT INTO chat_messages (role, content) VALUES (?, ?)`, domain.ChatRoleAssistant, assistantContent); err != nil { _ = tx.Rollback(); return err }
	return tx.Commit()
}

func (s *ChatHistory) Close() error { return s.db.Close() }
