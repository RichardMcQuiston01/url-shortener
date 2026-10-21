// Package store provides SQLite access for links, clicks and statistics.
package store

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors returned by store implementations. Callers match them with
// errors.Is.
var (
	ErrNotFound  error = errors.New("record not found")
	ErrSlugTaken error = errors.New("slug is already used by another link")
)

// Link is a stored short link. Only the slug is stored, never the full short
// URL, so the public base URL stays configuration.
type Link struct {
	ID        string
	Slug      string
	TargetURL string
	Note      *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewLink is the data needed to create a Link. The store assigns the ID and
// timestamps.
type NewLink struct {
	Slug      string
	TargetURL string
	Note      *string
}

// Click is one recorded redirect. Bot clicks are stored but excluded from
// headline counts. No IP address or user agent is stored.
type Click struct {
	ID           string
	LinkID       string
	ClickedAt    time.Time
	ReferrerHost *string
	IsBot        bool
}

// LinkSummary is a Link with headline click counts (bots excluded).
type LinkSummary struct {
	Link
	TotalClicks  int64
	Clicks7Days  int64
	Clicks30Days int64
}

// DailyClicks is the click count for one UTC day (YYYY-MM-DD).
type DailyClicks struct {
	Day    string
	Clicks int64
}

// ReferrerCount is the click count for one referrer host.
type ReferrerCount struct {
	Host   string
	Clicks int64
}

// LinkDetail is the stats view for a single link.
type LinkDetail struct {
	LinkSummary
	DailyLast30Days []DailyClicks
	TopReferrers    []ReferrerCount
}

// LinkStore creates and looks up links.
type LinkStore interface {
	// Create inserts a link. It returns ErrSlugTaken if the slug exists.
	Create(ctx context.Context, newLink NewLink) (Link, error)
	// GetBySlug returns ErrNotFound if no link has the slug.
	GetBySlug(ctx context.Context, slug string) (Link, error)
	// GetByID returns ErrNotFound if no link has the id.
	GetByID(ctx context.Context, id string) (Link, error)
	// GetByTargetURL returns the oldest link with the target, or ErrNotFound.
	GetByTargetURL(ctx context.Context, targetURL string) (Link, error)
	// List returns links newest first, skipping offset and returning at most
	// limit of them.
	List(ctx context.Context, limit int, offset int) ([]Link, error)
}

// ClickStore records clicks.
type ClickStore interface {
	// InsertClicks stores a batch of clicks in one transaction.
	InsertClicks(ctx context.Context, clicks []Click) error
}

// StatsStore answers read-only statistics queries.
type StatsStore interface {
	// ListLinkSummaries returns links newest first with click counts.
	ListLinkSummaries(ctx context.Context, limit int, offset int) ([]LinkSummary, error)
	// GetLinkSummaryBySlug returns ErrNotFound if no link has the slug.
	GetLinkSummaryBySlug(ctx context.Context, slug string) (LinkSummary, error)
	// GetLinkDetail returns ErrNotFound if no link has the id.
	GetLinkDetail(ctx context.Context, id string) (LinkDetail, error)
}

// Pinger checks database connectivity for /healthz.
type Pinger interface {
	Ping(ctx context.Context) error
}
