package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, keeps CGO_ENABLED=0 builds working

	"github.com/richardmcquiston01/url-shortener/internal/migrations"
)

// SQLite is the SQLite-backed store. It is safe for concurrent use; a single
// connection serializes access, which is plenty at this scale.
type SQLite struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at databasePath, applies the
// connection pragmas from CLAUDE.md and runs pending migrations.
func Open(ctx context.Context, databasePath string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o750); err != nil {
		return nil, fmt.Errorf("create directory for database %q: %w", databasePath, err)
	}

	query := url.Values{}
	for _, pragma := range []string{
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"foreign_keys(1)",
		"synchronous(NORMAL)",
	} {
		query.Add("_pragma", pragma)
	}
	dataSourceName := (&url.URL{Scheme: "file", Opaque: databasePath, RawQuery: query.Encode()}).String()

	db, err := sql.Open("sqlite", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("open database %q: %w", databasePath, err)
	}
	db.SetMaxOpenConns(1)

	sqlite := &SQLite{db: db}
	if err := sqlite.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database %q: %w", databasePath, err)
	}
	if err := sqlite.migrate(ctx, migrations.Files); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database %q: %w", databasePath, err)
	}
	return sqlite, nil
}

// Ping checks that the database answers a trivial query.
func (sqlite *SQLite) Ping(ctx context.Context) error {
	var one int
	if err := sqlite.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close closes the database.
func (sqlite *SQLite) Close() error {
	if err := sqlite.db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}

// migrate applies, in lexical order and one transaction each, every .sql file
// in migrationFiles not yet recorded in schema_migration.
func (sqlite *SQLite) migrate(ctx context.Context, migrationFiles fs.FS) error {
	const createTable string = `CREATE TABLE IF NOT EXISTS schema_migration (
  version    TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL
)`
	if _, err := sqlite.db.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("create schema_migration table: %w", err)
	}

	fileNames, err := fs.Glob(migrationFiles, "*.sql")
	if err != nil {
		return fmt.Errorf("list migration files: %w", err)
	}
	sort.Strings(fileNames)

	for _, fileName := range fileNames {
		version := strings.TrimSuffix(fileName, ".sql")

		var alreadyApplied int
		if err := sqlite.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migration WHERE version = ?", version).Scan(&alreadyApplied); err != nil {
			return fmt.Errorf("check migration %q: %w", version, err)
		}
		if alreadyApplied > 0 {
			continue
		}

		statements, err := fs.ReadFile(migrationFiles, fileName)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", fileName, err)
		}
		if err := sqlite.applyMigration(ctx, version, string(statements)); err != nil {
			return err
		}
	}
	return nil
}

func (sqlite *SQLite) applyMigration(ctx context.Context, version string, statements string) error {
	transaction, err := sqlite.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %q: %w", version, err)
	}
	defer transaction.Rollback() //nolint:errcheck // no-op after a successful commit

	if _, err := transaction.ExecContext(ctx, statements); err != nil {
		return fmt.Errorf("apply migration %q: %w", version, err)
	}
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO schema_migration (version, applied_at) VALUES (?, ?)",
		version, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record migration %q: %w", version, err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit migration %q: %w", version, err)
	}
	return nil
}

// BackupTo writes a consistent, compacted copy of the database to
// destinationPath using VACUUM INTO. The destination file must not exist.
func (sqlite *SQLite) BackupTo(ctx context.Context, destinationPath string) error {
	if _, err := sqlite.db.ExecContext(ctx, "VACUUM INTO ?", destinationPath); err != nil {
		return fmt.Errorf("back up database to %q: %w", destinationPath, err)
	}
	return nil
}
