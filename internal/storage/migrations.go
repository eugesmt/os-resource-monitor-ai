package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"system-monitor-ai/migrations"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

var migrationMu sync.Mutex

// MigrateConversationDB applies all embedded schema migrations to path. It is
// safe to call for both a fresh and an already migrated database.
func MigrateConversationDB(ctx context.Context, path string) error {
	migrationMu.Lock()
	defer migrationMu.Unlock()

	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("conversation database path is empty")
	}
	if parent := filepath.Dir(path); parent != "." {
		info, err := os.Stat(parent)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(parent, 0o700); err != nil {
				return fmt.Errorf("create conversation database directory %q: %w", parent, err)
			}
			if runtime.GOOS != "windows" {
				if err := os.Chmod(parent, 0o700); err != nil {
					return fmt.Errorf("secure conversation database directory %q: %w", parent, err)
				}
			}
		} else if err != nil {
			return fmt.Errorf("stat conversation database directory %q: %w", parent, err)
		} else if !info.IsDir() {
			return fmt.Errorf("conversation database parent %q is not a directory", parent)
		}
	}
	if runtime.GOOS != "windows" {
		if err := secureDatabaseFile(path); err != nil {
			return err
		}
	}

	dsn := path
	if strings.Contains(dsn, "?") {
		dsn += "&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	} else {
		dsn += "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open conversation database for migration: %w", err)
	}
	defer db.Close()
	if runtime.GOOS != "windows" {
		if err := secureDatabaseFile(path); err != nil {
			return err
		}
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS, goose.WithVerbose(false))
	if err != nil {
		return fmt.Errorf("create conversation migration provider: %w", err)
	}
	if err := provider.Ping(ctx); err != nil {
		return fmt.Errorf("ping conversation database for migration: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := secureDatabaseFile(path); err != nil {
			return err
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply conversation migrations: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := secureDatabaseFile(path); err != nil {
			return err
		}
	}
	return nil
}

func secureDatabaseFile(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat conversation database %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("conversation database %q is not a regular file", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure conversation database %q: %w", path, err)
	}
	return nil
}
