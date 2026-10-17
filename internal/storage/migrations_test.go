package storage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestMigrateConversationDBFreshAndRepeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "conversation.db")
	ctx := context.Background()
	if err := MigrateConversationDB(ctx, path); err != nil {
		t.Fatalf("fresh migration: %v", err)
	}
	store, err := NewConversationStore(path)
	if err != nil {
		t.Fatalf("open freshly migrated store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close freshly migrated store: %v", err)
	}
	if err := MigrateConversationDB(ctx, path); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	store, err = NewConversationStore(path)
	if err != nil {
		t.Fatalf("open repeatedly migrated store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close repeatedly migrated store: %v", err)
	}
}

func TestMigrateConversationDBSecuresDatabaseAndParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes are not enforced on Windows")
	}
	parent := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(parent, "conversation.db")
	if err := MigrateConversationDB(context.Background(), path); err != nil {
		t.Fatalf("migration: %v", err)
	}
	directoryInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("parent permissions = %04o, want 0700", got)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("database permissions = %04o, want 0600", got)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("relax database permissions: %v", err)
	}
	if err := MigrateConversationDB(context.Background(), path); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	fileInfo, err = os.Stat(path)
	if err != nil {
		t.Fatalf("stat tightened database: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("tightened database permissions = %04o, want 0600", got)
	}
}

func TestMigrateConversationDBConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent", "conversation.db")
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	for range 2 {
		go func() {
			defer workers.Done()
			<-start
			errs <- MigrateConversationDB(ctx, path)
		}()
	}
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}
	store, err := NewConversationStore(path)
	if err != nil {
		t.Fatalf("open concurrently migrated store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close concurrently migrated store: %v", err)
	}
}
