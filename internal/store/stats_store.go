package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

var _ StatsStore = (*SQLite)(nil)

const (
	topReferrerLimit int           = 10
	dailyWindowDays  int           = 30
	weekWindowDays   int           = 7
	hoursPerDay      time.Duration = 24 * time.Hour
)

// The "last N days" windows are calendar windows in UTC: today plus the
// previous N-1 days. That keeps the per-day table and the 30-day headline
// count consistent with each other. Bot clicks are never counted.
func windowStart(now time.Time, days int) time.Time {
	return now.UTC().Truncate(hoursPerDay).AddDate(0, 0, -(days - 1))
}

// ListLinkSummaries returns links newest first with click counts.
func (sqlite *SQLite) ListLinkSummaries(ctx context.Context, limit int, offset int) ([]LinkSummary, error) {
	summaries, err := sqlite.querySummaries(ctx, "", nil, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list link summaries (limit %d, offset %d): %w", limit, offset, err)
	}
	return summaries, nil
}

// GetLinkSummaryBySlug returns the link with its click counts, or ErrNotFound
// (wrapped).
func (sqlite *SQLite) GetLinkSummaryBySlug(ctx context.Context, slug string) (LinkSummary, error) {
	summary, err := sqlite.querySingleSummary(ctx, "WHERE slug = ?", slug)
	if err != nil {
		return LinkSummary{}, fmt.Errorf("get link summary by slug %q: %w", slug, err)
	}
	return summary, nil
}

// GetLinkDetail returns the summary plus a zero-filled per-day table for the
// last 30 days (oldest first) and the top referrer hosts, or ErrNotFound
// (wrapped).
func (sqlite *SQLite) GetLinkDetail(ctx context.Context, id string) (LinkDetail, error) {
	summary, err := sqlite.querySingleSummary(ctx, "WHERE id = ?", id)
	if err != nil {
		return LinkDetail{}, fmt.Errorf("get link detail by id %q: %w", id, err)
	}

	daily, err := sqlite.queryDailyClicks(ctx, id, time.Now())
	if err != nil {
		return LinkDetail{}, fmt.Errorf("get link detail by id %q: %w", id, err)
	}
	referrers, err := sqlite.queryTopReferrers(ctx, id)
	if err != nil {
		return LinkDetail{}, fmt.Errorf("get link detail by id %q: %w", id, err)
	}
	return LinkDetail{LinkSummary: summary, DailyLast30Days: daily, TopReferrers: referrers}, nil
}

func (sqlite *SQLite) querySingleSummary(ctx context.Context, whereClause string, argument string) (LinkSummary, error) {
	summaries, err := sqlite.querySummaries(ctx, whereClause, []any{argument}, 1, 0)
	if err != nil {
		return LinkSummary{}, err
	}
	if len(summaries) == 0 {
		return LinkSummary{}, ErrNotFound
	}
	return summaries[0], nil
}

// querySummaries picks the page of links first and only then joins clicks, so
// a page never aggregates the click table for links it does not return.
func (sqlite *SQLite) querySummaries(ctx context.Context, whereClause string, whereArguments []any, limit int, offset int) ([]LinkSummary, error) {
	now := time.Now()
	const nonBotInWindow string = "COALESCE(SUM(CASE WHEN c.is_bot = 0 AND c.clicked_at >= ? THEN 1 ELSE 0 END), 0)"

	query := `SELECT l.id, l.slug, l.target_url, l.note, l.created_at, l.updated_at,
  COALESCE(SUM(CASE WHEN c.is_bot = 0 THEN 1 ELSE 0 END), 0),
  ` + nonBotInWindow + `,
  ` + nonBotInWindow + `
FROM (SELECT * FROM link ` + whereClause + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?) l
LEFT JOIN click c ON c.link_id = l.id
GROUP BY l.id
ORDER BY l.created_at DESC, l.id DESC`

	arguments := []any{
		windowStart(now, weekWindowDays).Format(time.RFC3339),
		windowStart(now, dailyWindowDays).Format(time.RFC3339),
	}
	arguments = append(arguments, whereArguments...)
	arguments = append(arguments, limit, offset)

	rows, err := sqlite.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query link summaries: %w", err)
	}
	defer rows.Close()

	summaries := make([]LinkSummary, 0, limit)
	for rows.Next() {
		var (
			summary              LinkSummary
			note                 sql.NullString
			createdAt, updatedAt string
		)
		if err := rows.Scan(&summary.ID, &summary.Slug, &summary.TargetURL, &note, &createdAt, &updatedAt,
			&summary.TotalClicks, &summary.Clicks7Days, &summary.Clicks30Days); err != nil {
			return nil, fmt.Errorf("scan link summary: %w", err)
		}
		if err := fillLinkFields(&summary.Link, note, createdAt, updatedAt); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read link summaries: %w", err)
	}
	return summaries, nil
}

func (sqlite *SQLite) queryDailyClicks(ctx context.Context, linkID string, now time.Time) ([]DailyClicks, error) {
	start := windowStart(now, dailyWindowDays)

	rows, err := sqlite.db.QueryContext(ctx,
		`SELECT substr(clicked_at, 1, 10), COUNT(*) FROM click
WHERE link_id = ? AND is_bot = 0 AND clicked_at >= ?
GROUP BY 1`, linkID, start.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("query daily clicks: %w", err)
	}
	defer rows.Close()

	clicksByDay := make(map[string]int64, dailyWindowDays)
	for rows.Next() {
		var day string
		var clicks int64
		if err := rows.Scan(&day, &clicks); err != nil {
			return nil, fmt.Errorf("scan daily clicks: %w", err)
		}
		clicksByDay[day] = clicks
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read daily clicks: %w", err)
	}

	daily := make([]DailyClicks, 0, dailyWindowDays)
	for offset := 0; offset < dailyWindowDays; offset++ {
		day := start.AddDate(0, 0, offset).Format("2006-01-02")
		daily = append(daily, DailyClicks{Day: day, Clicks: clicksByDay[day]})
	}
	return daily, nil
}

func (sqlite *SQLite) queryTopReferrers(ctx context.Context, linkID string) ([]ReferrerCount, error) {
	rows, err := sqlite.db.QueryContext(ctx,
		`SELECT referrer_host, COUNT(*) AS clicks FROM click
WHERE link_id = ? AND is_bot = 0 AND referrer_host IS NOT NULL AND referrer_host != ''
GROUP BY referrer_host
ORDER BY clicks DESC, referrer_host ASC
LIMIT ?`, linkID, topReferrerLimit)
	if err != nil {
		return nil, fmt.Errorf("query top referrers: %w", err)
	}
	defer rows.Close()

	referrers := make([]ReferrerCount, 0, topReferrerLimit)
	for rows.Next() {
		var referrer ReferrerCount
		if err := rows.Scan(&referrer.Host, &referrer.Clicks); err != nil {
			return nil, fmt.Errorf("scan top referrers: %w", err)
		}
		referrers = append(referrers, referrer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read top referrers: %w", err)
	}
	return referrers, nil
}
