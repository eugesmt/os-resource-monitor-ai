package storage

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"taskmanager-ai/internal/domain"
)

func migratedDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chat.db")
	cmd := exec.Command("goose", "-dir", filepath.Join("..", "..", "migrations"), "sqlite3", path, "up")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("goose up: %v\n%s", err, output)
	}
	return path
}

func TestMigrationsApplyToFreshDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration-smoke.db")
	dir := filepath.Join("..", "..", "migrations")
	if output, err := exec.Command("goose", "-dir", dir, "sqlite3", path, "status").CombinedOutput(); err != nil {
		t.Fatalf("pre-up goose status: %v\n%s", err, output)
	}
	if output, err := exec.Command("goose", "-dir", dir, "sqlite3", path, "up").CombinedOutput(); err != nil {
		t.Fatalf("goose up: %v\n%s", err, output)
	}
	if output, err := exec.Command("goose", "-dir", dir, "sqlite3", path, "status").CombinedOutput(); err != nil {
		t.Fatalf("post-up goose status: %v\n%s", err, output)
	}
	store, err := OpenChatHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var tableSQL string
	if err := store.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'ai_call_usage'`).Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tableSQL, "assistant_message_id") || !strings.Contains(tableSQL, "component_token_count") {
		t.Fatalf("usage schema = %s", tableSQL)
	}
	var foreignKeyCount int
	if err := store.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('ai_call_usage')`).Scan(&foreignKeyCount); err != nil {
		t.Fatal(err)
	}
	if foreignKeyCount != 2 {
		t.Fatalf("foreign keys = %d, want 2", foreignKeyCount)
	}
}

func TestChatHistoryAppendLoadAndReopen(t *testing.T) {
	path := migratedDB(t)
	store, err := OpenChatHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.ChatMessage{{ID: 1, Role: domain.ChatRoleUser, Content: "question"}, {ID: 2, Role: domain.ChatRoleAssistant, Content: `{"summary":"answer","facts":[],"recommendations":[]}`}}
	usage := domain.AICallUsage{Model: "test-model", CurrentRequestTokens: 4, HistoryTokens: 3, SystemPromptTokens: 2, EstimatedInputTokens: 9, ComponentTokenCount: "estimated", UsageAvailable: true, ContextStatus: "unknown"}
	if err := store.AppendTurn(context.Background(), want[0].Content, want[1].Content, usage); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want[1].Usage = &usage
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenChatHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err = store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened Load() = %#v, want %#v", got, want)
	}
}

func TestChatHistoryRejectsInvalidTurnWithoutPartialInsert(t *testing.T) {
	store, err := OpenChatHistory(migratedDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	usage := domain.AICallUsage{Model: "test-model", ComponentTokenCount: "estimated", ContextStatus: "unknown"}
	if err := store.AppendTurn(context.Background(), "question", " ", usage); err == nil {
		t.Fatal("AppendTurn() error = nil")
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Load() = %#v, want empty history", got)
	}
}

func TestChatHistoryUsageIsAtomicAndForeignKeysAreEnabled(t *testing.T) {
	store, err := OpenChatHistory(migratedDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var foreignKeys int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	usage := domain.AICallUsage{Model: "test-model", CurrentRequestTokens: 1, ComponentTokenCount: "estimated", ContextStatus: "unknown"}
	if err := store.AppendTurn(context.Background(), "question", "answer", usage); err != nil {
		t.Fatal(err)
	}
	var messages, usageRows int
	if err := store.db.QueryRow(`SELECT count(*) FROM chat_messages`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM ai_call_usage`).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if messages != 2 || usageRows != 1 {
		t.Fatalf("messages=%d usage=%d, want 2 and 1", messages, usageRows)
	}

	badUsage := usage
	badUsage.ComponentTokenCount = "provider"
	if err := store.AppendTurn(context.Background(), "second question", "second answer", badUsage); err == nil {
		t.Fatal("AppendTurn() error = nil for invalid usage")
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM chat_messages`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM ai_call_usage`).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if messages != 2 || usageRows != 1 {
		t.Fatalf("failed turn partially persisted: messages=%d usage=%d", messages, usageRows)
	}
}

func TestChatHistoryLoadsLegacyMessageWithoutUsage(t *testing.T) {
	store, err := OpenChatHistory(migratedDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`INSERT INTO chat_messages (role, content) VALUES ('assistant', 'legacy')`); err != nil {
		t.Fatal(err)
	}
	items, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Usage != nil {
		t.Fatalf("legacy items = %#v", items)
	}
}
