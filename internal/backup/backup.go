// Package backup writes a nightly copy of the database from inside the app, so
// the single container needs no cron job or sidecar.
package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// KeepBackups is how many daily backups are retained.
	KeepBackups int = 14

	// RunHourUTC is the hour of day (UTC) the nightly backup runs.
	RunHourUTC int = 3

	filePrefix    string = "links-"
	fileSuffix    string = ".db"
	partialSuffix string = ".partial"
	dateLayout    string = "2006-01-02"

	// A backup older than this is taken immediately at startup, so a server that
	// restarts often (or was down at night) still gets backed up.
	maxBackupAge time.Duration = 24 * time.Hour
)

// Source can write a copy of the database to a new file.
type Source interface {
	BackupTo(ctx context.Context, destinationPath string) error
}

// Options tunes a Scheduler. Zero values use the package defaults; Now and Wait
// exist so tests can control time.
type Options struct {
	Keep    int
	HourUTC int
	Now     func() time.Time
	Wait    func(ctx context.Context, duration time.Duration) error
}

// Scheduler takes a backup at startup when the newest one is stale, then once
// every night.
type Scheduler struct {
	source  Source
	dir     string
	keep    int
	hourUTC int
	logger  *slog.Logger
	now     func() time.Time
	wait    func(ctx context.Context, duration time.Duration) error
}

// NewScheduler builds a Scheduler that stores backups in dir.
func NewScheduler(source Source, dir string, logger *slog.Logger, options Options) *Scheduler {
	scheduler := &Scheduler{
		source:  source,
		dir:     dir,
		keep:    options.Keep,
		hourUTC: options.HourUTC,
		logger:  logger,
		now:     options.Now,
		wait:    options.Wait,
	}
	if scheduler.keep <= 0 {
		scheduler.keep = KeepBackups
	}
	if scheduler.hourUTC <= 0 || scheduler.hourUTC > 23 {
		scheduler.hourUTC = RunHourUTC
	}
	if scheduler.now == nil {
		scheduler.now = time.Now
	}
	if scheduler.wait == nil {
		scheduler.wait = sleepContext
	}
	return scheduler
}

// Run blocks until ctx is cancelled. A failed backup is logged and retried at
// the next scheduled time; it never stops the app.
func (scheduler *Scheduler) Run(ctx context.Context) {
	if scheduler.dueNow() {
		scheduler.runOnce(ctx)
	}
	for {
		if err := scheduler.wait(ctx, scheduler.untilNextRun(scheduler.now())); err != nil {
			return
		}
		scheduler.runOnce(ctx)
	}
}

// RunOnce takes one backup now and prunes old ones.
func (scheduler *Scheduler) RunOnce(ctx context.Context) (string, error) {
	if err := os.MkdirAll(scheduler.dir, 0o750); err != nil {
		return "", fmt.Errorf("create backup directory %q: %w", scheduler.dir, err)
	}

	finalPath := filepath.Join(scheduler.dir, filePrefix+scheduler.now().UTC().Format(dateLayout)+fileSuffix)
	partialPath := finalPath + partialSuffix
	// VACUUM INTO refuses to overwrite, and a crashed run may have left a partial.
	if err := os.Remove(partialPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("remove stale partial backup %q: %w", partialPath, err)
	}

	if err := scheduler.source.BackupTo(ctx, partialPath); err != nil {
		_ = os.Remove(partialPath)
		return "", err
	}
	// Rename last, so a file with the final name is always a complete backup.
	if err := os.Rename(partialPath, finalPath); err != nil {
		_ = os.Remove(partialPath)
		return "", fmt.Errorf("finish backup %q: %w", finalPath, err)
	}

	if err := scheduler.prune(); err != nil {
		scheduler.logger.Error("prune old backups failed", "dir", scheduler.dir, "error", err)
	}
	return finalPath, nil
}

func (scheduler *Scheduler) runOnce(ctx context.Context) {
	path, err := scheduler.RunOnce(ctx)
	if err != nil {
		scheduler.logger.Error("database backup failed; will retry at the next scheduled time", "error", err)
		return
	}
	scheduler.logger.Info("database backup written", "path", path)
}

// dueNow reports whether there is no backup, or the newest one is over a day old.
func (scheduler *Scheduler) dueNow() bool {
	names, err := scheduler.list()
	if err != nil || len(names) == 0 {
		return true
	}
	info, err := os.Stat(filepath.Join(scheduler.dir, names[0]))
	if err != nil {
		return true
	}
	return scheduler.now().Sub(info.ModTime()) > maxBackupAge
}

func (scheduler *Scheduler) untilNextRun(now time.Time) time.Duration {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), scheduler.hourUTC, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(now)
}

// list returns complete backup file names, newest first. Names embed the date,
// so a reverse sort is chronological.
func (scheduler *Scheduler) list() ([]string, error) {
	pattern := filepath.Join(scheduler.dir, filePrefix+"????-??-??"+fileSuffix)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("list backups in %q: %w", scheduler.dir, err)
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, filepath.Base(match))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

func (scheduler *Scheduler) prune() error {
	names, err := scheduler.list()
	if err != nil {
		return err
	}
	var failures []error
	for _, name := range names[min(scheduler.keep, len(names)):] {
		if err := os.Remove(filepath.Join(scheduler.dir, name)); err != nil {
			failures = append(failures, fmt.Errorf("remove old backup %q: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
