package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) (*SQLite, string) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "nested", "links.db")
	sqlite, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return sqlite, databasePath
}

func TestOpenAppliesMigrationsAndPragmas(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()

	for _, table := range []string{"link", "click", "schema_migration"} {
		var count int
		if err := sqlite.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %q missing after migration", table)
		}
	}

	expectedPragmas := map[string]string{
		"journal_mode": "wal",
		"foreign_keys": "1",
		"busy_timeout": "5000",
		"synchronous":  "1",
	}
	for pragma, expected := range expectedPragmas {
		var actual string
		if err := sqlite.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&actual); err != nil {
			t.Fatalf("read pragma %s: %v", pragma, err)
		}
		if actual != expected {
			t.Errorf("pragma %s = %q, want %q", pragma, actual, expected)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	sqlite, databasePath := openTestStore(t)
	if err := sqlite.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer reopened.Close()

	var appliedCount int
	if err := reopened.db.QueryRow("SELECT COUNT(*) FROM schema_migration").Scan(&appliedCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if appliedCount != 1 {
		t.Errorf("schema_migration rows = %d, want 1 (migrations must not be re-applied)", appliedCount)
	}
}

func TestForeignKeysCascadeDelete(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	statements := []string{
		`INSERT INTO link (id, slug, target_url, created_at, updated_at) VALUES ('l1', 'a', 'https://example.com', 't', 't')`,
		`INSERT INTO click (id, link_id, clicked_at) VALUES ('c1', 'l1', 't')`,
		`DELETE FROM link WHERE id = 'l1'`,
	}
	for _, statement := range statements {
		if _, err := sqlite.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	var remaining int
	if err := sqlite.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM click").Scan(&remaining); err != nil {
		t.Fatalf("count clicks: %v", err)
	}
	if remaining != 0 {
		t.Errorf("clicks remaining = %d, want 0 after cascade", remaining)
	}
}

func TestPing(t *testing.T) {
	sqlite, _ := openTestStore(t)
	if err := sqlite.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

var _ Pinger = (*SQLite)(nil)
