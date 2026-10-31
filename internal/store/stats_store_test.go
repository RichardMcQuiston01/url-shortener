package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func createTestLink(t *testing.T, sqlite *SQLite, slug string) Link {
	t.Helper()
	link, err := sqlite.Create(context.Background(), NewLink{Slug: slug, TargetURL: "https://example.com/" + slug})
	if err != nil {
		t.Fatalf("Create %q: %v", slug, err)
	}
	return link
}

func clickAt(linkID string, clickedAt time.Time, referrerHost string, isBot bool) Click {
	click := Click{LinkID: linkID, ClickedAt: clickedAt, IsBot: isBot}
	if referrerHost != "" {
		click.ReferrerHost = &referrerHost
	}
	return click
}

func daysAgo(days int) time.Time {
	return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -days).Add(12 * time.Hour)
}

func insert(t *testing.T, sqlite *SQLite, clicks ...Click) {
	t.Helper()
	if err := sqlite.InsertClicks(context.Background(), clicks); err != nil {
		t.Fatalf("InsertClicks: %v", err)
	}
}

func TestInsertClicksEmptyBatchIsNoOp(t *testing.T) {
	sqlite, _ := openTestStore(t)
	if err := sqlite.InsertClicks(context.Background(), nil); err != nil {
		t.Errorf("InsertClicks(nil) = %v, want nil", err)
	}
}

func TestInsertClicksIsAllOrNothing(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	link := createTestLink(t, sqlite, "a")

	err := sqlite.InsertClicks(ctx, []Click{
		clickAt(link.ID, time.Now(), "", false),
		clickAt("no-such-link", time.Now(), "", false),
	})
	if err == nil {
		t.Fatal("expected a foreign key error for an unknown link_id")
	}

	summary, err := sqlite.GetLinkSummaryBySlug(ctx, "a")
	if err != nil {
		t.Fatalf("GetLinkSummaryBySlug: %v", err)
	}
	if summary.TotalClicks != 0 {
		t.Errorf("total clicks = %d, want 0: the failed batch must be rolled back", summary.TotalClicks)
	}
}

func TestSummaryCountsExcludeBotsAndRespectWindows(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	link := createTestLink(t, sqlite, "a")
	createTestLink(t, sqlite, "quiet")

	insert(t, sqlite,
		clickAt(link.ID, daysAgo(0), "", false),   // today: in all windows
		clickAt(link.ID, daysAgo(6), "", false),   // oldest day of the 7-day window
		clickAt(link.ID, daysAgo(7), "", false),   // in the 30-day window only
		clickAt(link.ID, daysAgo(29), "", false),  // oldest day of the 30-day window
		clickAt(link.ID, daysAgo(30), "", false),  // outside both windows
		clickAt(link.ID, daysAgo(0), "", true),    // bot: never counted
		clickAt(link.ID, daysAgo(100), "", false), // old: total only
	)

	summary, err := sqlite.GetLinkSummaryBySlug(ctx, "a")
	if err != nil {
		t.Fatalf("GetLinkSummaryBySlug: %v", err)
	}
	if summary.TotalClicks != 6 || summary.Clicks7Days != 2 || summary.Clicks30Days != 4 {
		t.Errorf("counts total/7d/30d = %d/%d/%d, want 6/2/4",
			summary.TotalClicks, summary.Clicks7Days, summary.Clicks30Days)
	}

	quiet, err := sqlite.GetLinkSummaryBySlug(ctx, "quiet")
	if err != nil {
		t.Fatalf("GetLinkSummaryBySlug quiet: %v", err)
	}
	if quiet.TotalClicks != 0 || quiet.Clicks7Days != 0 || quiet.Clicks30Days != 0 {
		t.Errorf("a link with no clicks should have zero counts, got %+v", quiet)
	}
}

func TestSummaryNotFound(t *testing.T) {
	sqlite, _ := openTestStore(t)
	if _, err := sqlite.GetLinkSummaryBySlug(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLinkSummaryBySlug error = %v, want ErrNotFound", err)
	}
	if _, err := sqlite.GetLinkDetail(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLinkDetail error = %v, want ErrNotFound", err)
	}
}

func TestListLinkSummariesOrderPagingAndCounts(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	for index, slug := range []string{"a", "b", "c"} {
		link := createTestLink(t, sqlite, slug)
		stamp := time.Date(2026, 1, index+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
		if _, err := sqlite.db.ExecContext(ctx, "UPDATE link SET created_at = ? WHERE id = ?", stamp, link.ID); err != nil {
			t.Fatalf("set created_at: %v", err)
		}
		for count := 0; count <= index; count++ { // a:1 click, b:2, c:3
			insert(t, sqlite, clickAt(link.ID, daysAgo(0), "", false))
		}
	}

	all, err := sqlite.ListLinkSummaries(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListLinkSummaries: %v", err)
	}
	if len(all) != 3 || all[0].Slug != "c" || all[1].Slug != "b" || all[2].Slug != "a" {
		t.Fatalf("order = %+v, want c, b, a", all)
	}
	if all[0].TotalClicks != 3 || all[1].TotalClicks != 2 || all[2].TotalClicks != 1 {
		t.Errorf("click counts are attached to the wrong links: %+v", all)
	}

	page, err := sqlite.ListLinkSummaries(ctx, 1, 1)
	if err != nil || len(page) != 1 || page[0].Slug != "b" || page[0].TotalClicks != 2 {
		t.Errorf("page = %+v, %v; want only b with 2 clicks", page, err)
	}
	empty, err := sqlite.ListLinkSummaries(ctx, 5, 10)
	if err != nil || len(empty) != 0 {
		t.Errorf("past the end = %+v, %v; want empty", empty, err)
	}
}

func TestGetLinkDetailDailyTableAndReferrers(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	link := createTestLink(t, sqlite, "a")
	other := createTestLink(t, sqlite, "other")

	insert(t, sqlite,
		clickAt(link.ID, daysAgo(0), "news.example", false),
		clickAt(link.ID, daysAgo(0), "news.example", false),
		clickAt(link.ID, daysAgo(0), "blog.example", false),
		clickAt(link.ID, daysAgo(2), "blog.example", false),
		clickAt(link.ID, daysAgo(2), "", false),           // direct visit: no referrer row
		clickAt(link.ID, daysAgo(0), "bot.example", true), // bot: ignored everywhere
		clickAt(link.ID, daysAgo(40), "old.example", false),
		clickAt(other.ID, daysAgo(0), "news.example", false), // another link: ignored
	)

	detail, err := sqlite.GetLinkDetail(ctx, link.ID)
	if err != nil {
		t.Fatalf("GetLinkDetail: %v", err)
	}
	if detail.Slug != "a" || detail.TotalClicks != 6 || detail.Clicks30Days != 5 {
		t.Errorf("summary part = %+v", detail.LinkSummary)
	}

	if len(detail.DailyLast30Days) != 30 {
		t.Fatalf("daily table has %d rows, want 30 (zero-filled)", len(detail.DailyLast30Days))
	}
	first, last := detail.DailyLast30Days[0], detail.DailyLast30Days[29]
	if first.Day != daysAgo(29).Format("2006-01-02") || last.Day != daysAgo(0).Format("2006-01-02") {
		t.Errorf("daily range %s..%s is not oldest..today", first.Day, last.Day)
	}
	var sum int64
	for _, day := range detail.DailyLast30Days {
		sum += day.Clicks
	}
	if last.Clicks != 3 || detail.DailyLast30Days[27].Clicks != 2 || sum != detail.Clicks30Days {
		t.Errorf("today=%d two-days-ago=%d sum=%d; want 3, 2 and the 30-day count %d",
			last.Clicks, detail.DailyLast30Days[27].Clicks, sum, detail.Clicks30Days)
	}

	if len(detail.TopReferrers) != 3 {
		t.Fatalf("referrers = %+v", detail.TopReferrers)
	}
	// blog.example (2) ties news.example (2): ties sort by host, then old.example (1).
	want := []ReferrerCount{{"blog.example", 2}, {"news.example", 2}, {"old.example", 1}}
	for index, expected := range want {
		if detail.TopReferrers[index] != expected {
			t.Errorf("referrer[%d] = %+v, want %+v", index, detail.TopReferrers[index], expected)
		}
	}
}

func TestTopReferrersAreLimitedToTen(t *testing.T) {
	sqlite, _ := openTestStore(t)
	link := createTestLink(t, sqlite, "a")
	for index := 0; index < 12; index++ {
		insert(t, sqlite, clickAt(link.ID, daysAgo(0), "host"+string(rune('a'+index))+".example", false))
	}
	detail, err := sqlite.GetLinkDetail(context.Background(), link.ID)
	if err != nil {
		t.Fatalf("GetLinkDetail: %v", err)
	}
	if len(detail.TopReferrers) != 10 {
		t.Errorf("got %d referrers, want the top 10", len(detail.TopReferrers))
	}
}
