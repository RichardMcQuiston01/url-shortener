package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

var fixedNow time.Time = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newScheduler(source Source, dir string, options Options) *Scheduler {
	if options.Now == nil {
		options.Now = func() time.Time { return fixedNow }
	}
	return NewScheduler(source, dir, discardLogger(), options)
}

type failingSource struct{ calls int }

func (source *failingSource) BackupTo(_ context.Context, destinationPath string) error {
	source.calls++
	// Leave a partial file behind, as a crashed copy would.
	_ = os.WriteFile(destinationPath, []byte("half"), 0o600)
	return errors.New("disk full")
}

func TestRunOnceWritesARestorableBackup(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	created, err := database.Create(ctx, store.NewLink{Slug: "hello", TargetURL: "https://example.com/a"})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}

	backupDir := filepath.Join(t.TempDir(), "backups")
	path, err := newScheduler(database, backupDir, Options{}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if want := filepath.Join(backupDir, "links-2026-10-21.db"); path != want {
		t.Errorf("backup path = %q, want %q", path, want)
	}

	// The restore test: the backup opens as a normal database with the data in it.
	restored, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("open the backup as a database: %v", err)
	}
	defer restored.Close()
	link, err := restored.GetBySlug(ctx, "hello")
	if err != nil || link.ID != created.ID || link.TargetURL != "https://example.com/a" {
		t.Errorf("restored link = %+v, %v; want the original", link, err)
	}
}

func TestRunOnceTwiceInADayKeepsOneFile(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()

	backupDir := t.TempDir()
	scheduler := newScheduler(database, backupDir, Options{})
	for run := 1; run <= 2; run++ {
		if _, err := scheduler.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce %d: %v", run, err)
		}
	}
	entries, _ := os.ReadDir(backupDir)
	if len(entries) != 1 {
		t.Errorf("found %d files, want 1 (the second run replaces the first)", len(entries))
	}
}

func TestRunOnceKeepsOnlyTheNewestFourteen(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()

	backupDir := t.TempDir()
	for day := 1; day <= 20; day++ {
		name := fmt.Sprintf("links-2026-09-%02d.db", day)
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("old"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	unrelated := filepath.Join(backupDir, "notes.txt")
	if err := os.WriteFile(unrelated, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("seed unrelated file: %v", err)
	}

	if _, err := newScheduler(database, backupDir, Options{}).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	matches, _ := filepath.Glob(filepath.Join(backupDir, "links-*.db"))
	if len(matches) != KeepBackups {
		t.Fatalf("kept %d backups, want %d", len(matches), KeepBackups)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "links-2026-10-21.db")); err != nil {
		t.Error("today's backup must be kept")
	}
	if _, err := os.Stat(filepath.Join(backupDir, "links-2026-09-20.db")); err != nil {
		t.Error("the newest old backups must be kept")
	}
	if _, err := os.Stat(filepath.Join(backupDir, "links-2026-09-01.db")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the oldest backups must be removed")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Error("files that are not backups must never be deleted")
	}
}

func TestFailedBackupLeavesNoPartialFile(t *testing.T) {
	backupDir := t.TempDir()
	if _, err := newScheduler(&failingSource{}, backupDir, Options{}).RunOnce(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	entries, _ := os.ReadDir(backupDir)
	if len(entries) != 0 {
		t.Errorf("a failed backup left %d files behind", len(entries))
	}
}

func TestStalePartialFromACrashIsReplaced(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()

	backupDir := t.TempDir()
	stale := filepath.Join(backupDir, "links-2026-10-21.db.partial")
	if err := os.WriteFile(stale, []byte("crashed"), 0o600); err != nil {
		t.Fatalf("seed partial: %v", err)
	}
	if _, err := newScheduler(database, backupDir, Options{}).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Error("the stale partial file should be gone")
	}
}

func TestUntilNextRun(t *testing.T) {
	scheduler := newScheduler(&failingSource{}, t.TempDir(), Options{})
	testCases := []struct {
		name     string
		now      time.Time
		expected time.Duration
	}{
		{"before the run hour", time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC), 2 * time.Hour},
		{"after the run hour", time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), 15 * time.Hour},
		{"exactly at the run hour", time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC), 24 * time.Hour},
		{"a non-UTC clock is converted", time.Date(2026, 10, 21, 5, 0, 0, 0, time.FixedZone("EEST", 3*3600)), time.Hour},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := scheduler.untilNextRun(testCase.now); actual != testCase.expected {
				t.Errorf("untilNextRun = %v, want %v", actual, testCase.expected)
			}
		})
	}
}

func TestDueNow(t *testing.T) {
	backupDir := t.TempDir()
	scheduler := newScheduler(&failingSource{}, backupDir, Options{})
	if !scheduler.dueNow() {
		t.Error("a missing backup directory means a backup is due")
	}

	path := filepath.Join(backupDir, "links-2026-10-21.db")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed backup: %v", err)
	}
	fresh := fixedNow.Add(-2 * time.Hour)
	if err := os.Chtimes(path, fresh, fresh); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if scheduler.dueNow() {
		t.Error("a backup from 2 hours ago means nothing is due")
	}

	stale := fixedNow.Add(-25 * time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if !scheduler.dueNow() {
		t.Error("a backup from 25 hours ago means a backup is due")
	}
}

func TestRunBacksUpAtStartupThenNightlyAndSurvivesFailures(t *testing.T) {
	source := &failingSource{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var waits []time.Duration
	scheduler := newScheduler(source, t.TempDir(), Options{
		Wait: func(_ context.Context, duration time.Duration) error {
			waits = append(waits, duration)
			if len(waits) == 3 {
				cancel()
				return ctx.Err()
			}
			return nil
		},
	})
	scheduler.Run(ctx)

	// One attempt at startup (no backup exists) plus one after each of the two
	// completed waits; every attempt failed and the loop kept going regardless.
	if source.calls != 3 {
		t.Errorf("backup attempted %d times, want 3", source.calls)
	}
	if len(waits) != 3 || waits[0] != 15*time.Hour {
		t.Errorf("waits = %v, want three waits starting with 15h", waits)
	}
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		newScheduler(&failingSource{}, t.TempDir(), Options{}).Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}
