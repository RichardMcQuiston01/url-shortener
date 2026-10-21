package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var _ LinkStore = (*SQLite)(nil)

// sqliteConstraintUnique is SQLITE_CONSTRAINT_UNIQUE, the extended result code
// SQLite reports when a UNIQUE index rejects an insert.
const sqliteConstraintUnique int = 2067

const linkColumns string = "id, slug, target_url, note, created_at, updated_at"

// Create inserts a link with a new UUID and the current UTC time. It returns
// ErrSlugTaken (wrapped) if another link already uses the slug.
func (sqlite *SQLite) Create(ctx context.Context, newLink NewLink) (Link, error) {
	linkID, err := NewUUID()
	if err != nil {
		return Link{}, fmt.Errorf("create link %q: %w", newLink.Slug, err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	_, err = sqlite.db.ExecContext(ctx,
		"INSERT INTO link ("+linkColumns+") VALUES (?, ?, ?, ?, ?, ?)",
		linkID, newLink.Slug, newLink.TargetURL, newLink.Note,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		if isUniqueViolation(err) {
			return Link{}, fmt.Errorf("insert link %q: %w", newLink.Slug, ErrSlugTaken)
		}
		return Link{}, fmt.Errorf("insert link %q: %w", newLink.Slug, err)
	}

	return Link{
		ID:        linkID,
		Slug:      newLink.Slug,
		TargetURL: newLink.TargetURL,
		Note:      newLink.Note,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetBySlug returns the link with the slug, or ErrNotFound (wrapped).
func (sqlite *SQLite) GetBySlug(ctx context.Context, slug string) (Link, error) {
	link, err := sqlite.queryLink(ctx, "WHERE slug = ?", slug)
	if err != nil {
		return Link{}, fmt.Errorf("get link by slug %q: %w", slug, err)
	}
	return link, nil
}

// GetByID returns the link with the id, or ErrNotFound (wrapped).
func (sqlite *SQLite) GetByID(ctx context.Context, id string) (Link, error) {
	link, err := sqlite.queryLink(ctx, "WHERE id = ?", id)
	if err != nil {
		return Link{}, fmt.Errorf("get link by id %q: %w", id, err)
	}
	return link, nil
}

// GetByTargetURL returns the oldest link pointing at targetURL, or
// ErrNotFound (wrapped).
func (sqlite *SQLite) GetByTargetURL(ctx context.Context, targetURL string) (Link, error) {
	link, err := sqlite.queryLink(ctx, "WHERE target_url = ? ORDER BY created_at ASC, id ASC", targetURL)
	if err != nil {
		return Link{}, fmt.Errorf("get link by target url: %w", err)
	}
	return link, nil
}

// List returns links newest first, skipping offset and returning at most limit.
func (sqlite *SQLite) List(ctx context.Context, limit int, offset int) ([]Link, error) {
	rows, err := sqlite.db.QueryContext(ctx,
		"SELECT "+linkColumns+" FROM link ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list links (limit %d, offset %d): %w", limit, offset, err)
	}
	defer rows.Close()

	links := make([]Link, 0, limit)
	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("list links: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list links: read rows: %w", err)
	}
	return links, nil
}

func (sqlite *SQLite) queryLink(ctx context.Context, whereClause string, argument string) (Link, error) {
	row := sqlite.db.QueryRowContext(ctx, "SELECT "+linkColumns+" FROM link "+whereClause+" LIMIT 1", argument)
	link, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return link, err
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(destinations ...any) error
}

func scanLink(row rowScanner) (Link, error) {
	var (
		link                 Link
		note                 sql.NullString
		createdAt, updatedAt string
	)
	if err := row.Scan(&link.ID, &link.Slug, &link.TargetURL, &note, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Link{}, err
		}
		return Link{}, fmt.Errorf("scan link: %w", err)
	}
	if err := fillLinkFields(&link, note, createdAt, updatedAt); err != nil {
		return Link{}, err
	}
	return link, nil
}

// fillLinkFields converts the nullable note and the stored RFC 3339 timestamps
// into the Link's typed fields.
func fillLinkFields(link *Link, note sql.NullString, createdAt string, updatedAt string) error {
	if note.Valid {
		link.Note = &note.String
	}
	var err error
	if link.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return fmt.Errorf("parse created_at %q of link %q: %w", createdAt, link.Slug, err)
	}
	if link.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
		return fmt.Errorf("parse updated_at %q of link %q: %w", updatedAt, link.Slug, err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var codedError interface{ Code() int }
	if errors.As(err, &codedError) && codedError.Code() == sqliteConstraintUnique {
		return true
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
