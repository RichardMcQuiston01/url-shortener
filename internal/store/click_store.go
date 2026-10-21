package store

import (
	"context"
	"fmt"
	"time"
)

var _ ClickStore = (*SQLite)(nil)

// InsertClicks stores a batch of clicks in one transaction, so either every
// click is saved or none is. A click without an ID gets a new UUID.
func (sqlite *SQLite) InsertClicks(ctx context.Context, clicks []Click) error {
	if len(clicks) == 0 {
		return nil
	}

	transaction, err := sqlite.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin insert of %d clicks: %w", len(clicks), err)
	}
	defer transaction.Rollback() //nolint:errcheck // no-op after a successful commit

	statement, err := transaction.PrepareContext(ctx,
		"INSERT INTO click (id, link_id, clicked_at, referrer_host, is_bot) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare click insert: %w", err)
	}
	defer statement.Close()

	for _, click := range clicks {
		if click.ID == "" {
			if click.ID, err = NewUUID(); err != nil {
				return fmt.Errorf("insert click for link %q: %w", click.LinkID, err)
			}
		}
		isBot := 0
		if click.IsBot {
			isBot = 1
		}
		if _, err := statement.ExecContext(ctx, click.ID, click.LinkID,
			click.ClickedAt.UTC().Format(time.RFC3339), click.ReferrerHost, isBot); err != nil {
			return fmt.Errorf("insert click for link %q: %w", click.LinkID, err)
		}
	}

	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit insert of %d clicks: %w", len(clicks), err)
	}
	return nil
}
