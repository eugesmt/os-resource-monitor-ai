package storage

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestChatHistoryAppendLoadAndReopen(t *testing.T) {
	path := migratedDB(t)
	store, err := OpenChatHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.ChatMessage{{ID: 1, Role: domain.ChatRoleUser, Content: "question"}, {ID: 2, Role: domain.ChatRoleAssistant, Content: `{"summary":"answer","facts":[],"recommendations":[]}`}}
	if err := store.AppendTurn(context.Background(), want[0].Content, want[1].Content); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
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
	if err := store.AppendTurn(context.Background(), "question", " "); err == nil {
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
