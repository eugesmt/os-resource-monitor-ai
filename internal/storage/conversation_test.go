package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"taskmanager-ai/internal/ai"
	"taskmanager-ai/internal/domain"

	_ "modernc.org/sqlite"
)

func migratedConversationDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conversation.db")
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920120000_conversation_event_log.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	up, _, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("migration has no Down marker")
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	if _, err := db.Exec(up); err != nil {
		_ = db.Close()
		t.Fatalf("apply migration: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close migration database: %v", err)
	}
	return path
}

func TestConversationStoreListRecentEventsBoundedBeforeCursorAndReopen(t *testing.T) {
	store, path := openConversationTestStore(t)
	ctx := context.Background()
	for _, turn := range []string{"turn-1", "turn-2"} {
		if _, err := store.AppendUser(ctx, DefaultConversationID, turn, turn+" question", turn+" snapshot"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendAssistant(ctx, DefaultConversationID, turn, turn+" answer", turn+" snapshot"); err != nil {
			t.Fatal(err)
		}
	}
	current, err := store.AppendUser(ctx, DefaultConversationID, "turn-3", "current question", "snap-3")
	if err != nil {
		t.Fatal(err)
	}

	recent, err := store.ListRecentEvents(ctx, DefaultConversationID, current.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].ID != 3 || recent[1].ID != 4 {
		t.Fatalf("recent events = %#v, want IDs 3,4 in ascending order", recent)
	}
	beforeAssistant, err := store.ListRecentEvents(ctx, DefaultConversationID, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeAssistant) != 3 || beforeAssistant[0].ID != 1 || beforeAssistant[2].ID != 3 {
		t.Fatalf("before cursor events = %#v, want IDs 1,2,3", beforeAssistant)
	}
	if empty, err := store.ListRecentEvents(ctx, DefaultConversationID, current.ID, 0); err != nil || len(empty) != 0 {
		t.Fatalf("zero-limit events = %#v, %v", empty, err)
	}

	compiledBefore, err := ai.NewContextCompiler(store, 8000).Compile(ctx, DefaultConversationID, current.ID, "system", "snapshot", "question")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewConversationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	compiledAfter, err := ai.NewContextCompiler(reopened, 8000).Compile(ctx, DefaultConversationID, current.ID, "system", "snapshot", "question")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compiledAfter, compiledBefore) {
		t.Fatalf("compiled context changed after reopen:\nbefore=%#v\nafter=%#v", compiledBefore, compiledAfter)
	}
}

func openConversationTestStore(t *testing.T) (*ConversationStore, string) {
	t.Helper()
	path := migratedConversationDB(t)
	store, err := NewConversationStore(path)
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func TestConversationStoreRequiresExpectedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := NewConversationStore(path); err == nil || !strings.Contains(err.Error(), "apply migrations") {
		t.Fatalf("missing schema error = %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE conversations (id TEXT PRIMARY KEY); CREATE TABLE conversation_events (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := NewConversationStore(path); err == nil || !strings.Contains(err.Error(), "created_at") {
		t.Fatalf("incompatible schema error = %v", err)
	}
}

func TestConversationStoreAppendListConstraintsAndReopen(t *testing.T) {
	store, path := openConversationTestStore(t)
	ctx := context.Background()

	items, err := store.ListEvents(ctx, DefaultConversationID)
	if err != nil || len(items) != 0 {
		t.Fatalf("initial ListEvents() = %#v, %v", items, err)
	}
	user, err := store.AppendUser(ctx, DefaultConversationID, "turn-1", "question", "snap-1")
	if err != nil {
		t.Fatalf("AppendUser() error = %v", err)
	}
	assistant, err := store.AppendAssistant(ctx, DefaultConversationID, "turn-1", `{"status":"healthy"}`, "snap-1")
	if err != nil {
		t.Fatalf("AppendAssistant() error = %v", err)
	}
	if user.ID >= assistant.ID || user.Role != domain.EventRoleUser || assistant.Role != domain.EventRoleAssistant || user.TurnID != assistant.TurnID || user.SnapshotID != assistant.SnapshotID || user.CreatedAt.IsZero() || assistant.CreatedAt.IsZero() {
		t.Fatalf("events not linked or ordered: user=%#v assistant=%#v", user, assistant)
	}
	if _, err := store.AppendUser(ctx, DefaultConversationID, "turn-1", "duplicate", "snap-1"); err == nil {
		t.Fatal("duplicate user event unexpectedly succeeded")
	}
	if _, err := store.AppendAssistant(ctx, DefaultConversationID, "turn-missing", "answer", "snap-1"); err == nil {
		t.Fatal("assistant without user unexpectedly succeeded")
	}
	if _, err := store.AppendAssistant(ctx, DefaultConversationID, "turn-1", "answer", "snap-other"); err == nil {
		t.Fatal("assistant with mismatched snapshot unexpectedly succeeded")
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO conversation_events (conversation_id, turn_id, role, content, snapshot_id) VALUES (?, ?, ?, ?, ?)`, DefaultConversationID, "turn-invalid", "system", "content", "snap"); err == nil {
		t.Fatal("invalid role unexpectedly succeeded")
	}
	if _, err := store.AppendUser(ctx, DefaultConversationID, "turn-empty", "", "snap"); err == nil {
		t.Fatal("empty content unexpectedly succeeded")
	}
	if _, err := store.AppendUser(ctx, "missing", "turn-fk", "content", "snap"); err == nil {
		t.Fatal("foreign-key violation unexpectedly succeeded")
	}

	want, err := store.ListEvents(ctx, DefaultConversationID)
	if err != nil || len(want) != 2 {
		t.Fatalf("ListEvents() = %#v, %v", want, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := NewConversationStore(path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer reopened.Close()
	got, err := reopened.ListEvents(ctx, DefaultConversationID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("events after reopen = %#v, %v; want %#v", got, err, want)
	}
}

func TestConversationStoreCancellationAndFailedAssistantAreAtomic(t *testing.T) {
	store, _ := openConversationTestStore(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.AppendUser(canceled, DefaultConversationID, "turn-canceled", "question", "snap"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled AppendUser() error = %v", err)
	}

	ctx := context.Background()
	if _, err := store.AppendUser(ctx, DefaultConversationID, "turn-atomic", "question", "snap"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendAssistant(ctx, DefaultConversationID, "turn-atomic", "", "snap"); err == nil {
		t.Fatal("empty assistant unexpectedly succeeded")
	}
	items, err := store.ListEvents(ctx, DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Role != domain.EventRoleUser || items[0].TurnID != "turn-atomic" {
		t.Fatalf("failed transaction left partial event: %#v", items)
	}
}
