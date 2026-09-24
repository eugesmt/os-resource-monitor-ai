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
	"time"

	"taskmanager-ai/internal/ai"
	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/invariants"
	"taskmanager-ai/internal/memory"

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
	memoryMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920200000_memory_records.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read memory migration: %v", err)
	}
	memoryUp, _, ok := strings.Cut(string(memoryMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("memory migration has no Down marker")
	}
	if _, err := db.Exec(memoryUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply memory migration: %v", err)
	}
	profilesMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920220000_profiles.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read profiles migration: %v", err)
	}
	profilesUp, _, ok := strings.Cut(string(profilesMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("profiles migration has no Down marker")
	}
	if _, err := db.Exec(profilesUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply profiles migration: %v", err)
	}
	invariantsMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920290000_assistant_invariants.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read invariants migration: %v", err)
	}
	invariantsUp, _, ok := strings.Cut(string(invariantsMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("invariants migration has no Down marker")
	}
	if _, err := db.Exec(invariantsUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply invariants migration: %v", err)
	}
	taskMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920230000_task_states.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read task state migration: %v", err)
	}
	taskUp, _, ok := strings.Cut(string(taskMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("task state migration has no Down marker")
	}
	if _, err := db.Exec(taskUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply task state migration: %v", err)
	}
	newTasksMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920240000_tasks.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read tasks migration: %v", err)
	}
	newTasksUp, _, ok := strings.Cut(string(newTasksMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("tasks migration has no Down marker")
	}
	if _, err := db.Exec(newTasksUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply tasks migration: %v", err)
	}
	taskOutcomeMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920250000_task_outcome.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read task outcome migration: %v", err)
	}
	taskOutcomeUp, _, ok := strings.Cut(string(taskOutcomeMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("task outcome migration has no Down marker")
	}
	if _, err := db.Exec(taskOutcomeUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply task outcome migration: %v", err)
	}
	taskErrorMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920260000_task_last_error.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read task error migration: %v", err)
	}
	taskErrorUp, _, ok := strings.Cut(string(taskErrorMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("task error migration has no Down marker")
	}
	if _, err := db.Exec(taskErrorUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply task error migration: %v", err)
	}
	taskResponseTypeMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920270000_task_response_type.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read task response type migration: %v", err)
	}
	taskResponseTypeUp, _, ok := strings.Cut(string(taskResponseTypeMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("task response type migration has no Down marker")
	}
	if _, err := db.Exec(taskResponseTypeUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply task response type migration: %v", err)
	}
	taskGoalMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920280000_task_goal.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read task goal migration: %v", err)
	}
	taskGoalUp, _, ok := strings.Cut(string(taskGoalMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("task goal migration has no Down marker")
	}
	if _, err := db.Exec(taskGoalUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply task goal migration: %v", err)
	}
	taskInvariantMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920300000_task_invariant_conflict_response.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read task invariant migration: %v", err)
	}
	taskInvariantUp, _, ok := strings.Cut(string(taskInvariantMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("task invariant migration has no Down marker")
	}
	if _, err := db.Exec(taskInvariantUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply task invariant migration: %v", err)
	}
	metricsMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20260920310000_metric_history.sql"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("read metrics migration: %v", err)
	}
	metricsUp, _, ok := strings.Cut(string(metricsMigration), "-- +goose Down")
	if !ok {
		_ = db.Close()
		t.Fatal("metrics migration has no Down marker")
	}
	if _, err := db.Exec(metricsUp); err != nil {
		_ = db.Close()
		t.Fatalf("apply metrics migration: %v", err)
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

func TestConversationStoreCreatesDistinctTasksAndReusesConversation(t *testing.T) {
	store, _ := openConversationTestStore(t)
	defer store.Close()
	ctx := context.Background()
	first, err := store.CreateTask(ctx, DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTask(ctx, DefaultConversationID); err == nil {
		t.Fatal("second open task was accepted")
	}
	done, err := first.Apply(domain.TaskActionAdvance, domain.TaskPhaseExecution, "execute", "validate")
	if err != nil {
		t.Fatal(err)
	}
	done, err = store.SaveTaskTransition(ctx, first.TaskID, "advance", first, done, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	done, err = done.ApplyDecision(domain.TaskDecision{Action: domain.TaskDecisionAdvance, Phase: domain.TaskPhaseValidation, Step: "validate", ExpectedActionKind: domain.TaskActionKindUser, ExpectedAction: "confirm"})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := store.SaveTaskTransition(ctx, first.TaskID, "advance", done, done, 2)
	if err != nil {
		t.Fatal(err)
	}
	finished, err := validated.ApplyDecision(domain.TaskDecision{Action: domain.TaskDecisionComplete})
	if err != nil {
		t.Fatal(err)
	}
	done, err = store.SaveTaskTransition(ctx, first.TaskID, "complete", validated, finished, validated.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTask(ctx, DefaultConversationID); err != nil {
		t.Fatal("new task after non-terminal task failed: ", err)
	}
	if done.TaskID != first.TaskID || done.Phase != domain.TaskPhaseDone {
		t.Fatalf("completed task = %#v", done)
	}
	latest, err := store.GetOpenTask(ctx, DefaultConversationID)
	if err != nil || latest.TaskID == first.TaskID {
		t.Fatalf("open task = %#v, err=%v", latest, err)
	}
}

func TestConversationStoreReopensPausedTaskCheckpoint(t *testing.T) {
	store, path := openConversationTestStore(t)
	ctx := context.Background()
	created, err := store.CreateTask(ctx, DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := created.ApplyDecision(domain.TaskDecision{Action: domain.TaskDecisionStay, Phase: domain.TaskPhasePlanning, Step: "собрать данные", ExpectedActionKind: domain.TaskActionKindTool, ExpectedAction: "дождаться результатов инструмента"})
	if err != nil {
		t.Fatal(err)
	}
	planned, err = store.SaveTaskTransition(ctx, created.TaskID, "stay", created, planned, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := planned.Apply(domain.TaskActionPause, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	paused, err = store.SaveTaskTransition(ctx, planned.TaskID, "pause", planned, paused, planned.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenConversationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetOpenTask(ctx, DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != paused.TaskID || got.Phase != paused.Phase || got.Status != domain.TaskStatusPaused || got.Step != paused.Step || got.ExpectedActionKind != paused.ExpectedActionKind || got.ExpectedAction != paused.ExpectedAction || got.Version != paused.Version {
		t.Fatalf("reopened checkpoint = %#v, want %#v", got, paused)
	}
}

func TestConversationStoreCommitsAssistantAndTaskAtomically(t *testing.T) {
	store, _ := openConversationTestStore(t)
	defer store.Close()
	ctx := context.Background()
	task, err := store.CreateTask(ctx, DefaultConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendUser(ctx, DefaultConversationID, "turn-task", "start task", "snap-task"); err != nil {
		t.Fatal(err)
	}
	next, err := task.ApplyDecision(domain.TaskDecision{Action: domain.TaskDecisionAdvance, Phase: domain.TaskPhaseExecution, Step: "execute", ExpectedActionKind: domain.TaskActionKindAgent, ExpectedAction: "run"})
	if err != nil {
		t.Fatal(err)
	}
	assistant, committed, err := store.CommitAssistantAndTask(ctx, DefaultConversationID, "turn-task", `{"assistant_schema_version":"assistant.v2"}`, "snap-task", task.TaskID, "advance", task, next)
	if err != nil {
		t.Fatal(err)
	}
	if assistant.Role != domain.EventRoleAssistant || committed.Phase != domain.TaskPhaseExecution || committed.Version != task.Version+1 {
		t.Fatalf("assistant=%#v committed=%#v", assistant, committed)
	}
	events, err := store.ListEvents(ctx, DefaultConversationID)
	if err != nil || len(events) != 2 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}

func TestConversationStorePersistsSeparateMemoryLayers(t *testing.T) {
	store, path := openConversationTestStore(t)
	defer store.Close()
	ctx := context.Background()
	record := memory.Record{ID: "memory-1", Layer: memory.LongTerm, Scope: "default", Kind: "preference", Content: "answer in Russian", CreatedAt: time.Now().UTC()}
	if err := store.SaveMemory(ctx, record); err != nil {
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
	items, err := reopened.ListMemory(ctx)
	if err != nil || len(items) != 1 || items[0].Layer != memory.LongTerm || items[0].Content != record.Content {
		t.Fatalf("memory records = %#v, err=%v", items, err)
	}
}

func TestConversationStorePersistsProfiles(t *testing.T) {
	store, path := openConversationTestStore(t)
	ctx := context.Background()
	want := memory.Profile{UserID: "user-1", Style: "concise", Format: "bullets", Constraints: []string{"answer in Russian", "do not invent metrics"}}
	if err := store.SaveProfile(ctx, want); err != nil {
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
	profiles, err := reopened.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || !reflect.DeepEqual(profiles[0], want) {
		t.Fatalf("profiles = %#v, want %#v", profiles, []memory.Profile{want})
	}
}

func TestConversationStorePersistsInvariantsSeparately(t *testing.T) {
	store, path := openConversationTestStore(t)
	ctx := context.Background()
	want := []invariants.Invariant{{ID: "architecture-monolith", Category: "architecture", Statement: "Keep the modular monolith", Enabled: true}}
	if err := store.ReplaceInvariants(ctx, want); err != nil {
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
	got, err := reopened.ListInvariants(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("invariants = %#v, err=%v", got, err)
	}
	if err := reopened.DeleteInvariant(ctx, want[0].ID); err != nil {
		t.Fatal(err)
	}
	if items, err := reopened.ListInvariants(ctx); err != nil || len(items) != 0 {
		t.Fatalf("invariants after delete = %#v, err=%v", items, err)
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
	profileUser, err := store.AppendUser(ctx, "profile-user", "turn-profile", "content", "snap")
	if err != nil {
		t.Fatalf("new profile conversation was not created: %v", err)
	}
	profileEvents, err := store.ListEvents(ctx, "profile-user")
	if err != nil || len(profileEvents) != 1 || profileEvents[0].ID != profileUser.ID {
		t.Fatalf("profile conversation events = %#v, %v", profileEvents, err)
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
