package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

func clickOn(linkID string, daysAgo int, referrerHost string, isBot bool) store.Click {
	click := store.Click{
		LinkID:    linkID,
		ClickedAt: time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -daysAgo).Add(12 * time.Hour),
		IsBot:     isBot,
	}
	if referrerHost != "" {
		click.ReferrerHost = &referrerHost
	}
	return click
}

func TestLinkDetailShowsTotalsDailyTableAndReferrers(t *testing.T) {
	app := newTestApp(t)
	link := app.seedLink(t, "hello", "https://example.com/target", "Demo note")
	if err := app.store.InsertClicks(context.Background(), []store.Click{
		clickOn(link.ID, 0, "news.example", false),
		clickOn(link.ID, 0, "news.example", false),
		clickOn(link.ID, 0, "blog.example", false),
		clickOn(link.ID, 3, "", false),
		clickOn(link.ID, 0, "bot.example", true),
		clickOn(link.ID, 45, "old.example", false),
	}); err != nil {
		t.Fatalf("insert clicks: %v", err)
	}

	cookie, _ := app.signIn(t)
	response := app.send(http.MethodGet, "/admin/links/"+link.ID, nil, cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()

	for _, expected := range []string{
		"Stats for /hello",
		`<a href="https://go.example.com/hello">https://go.example.com/hello</a>`,
		"https://example.com/target",
		"Demo note",
		`<td class="number">5</td>`, // all time: 6 clicks minus the bot
		`<td class="number">4</td>`, // last 7 and 30 days
		`<td class="break">news.example</td>`,
		`<td class="break">old.example</td>`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("page is missing %q", expected)
		}
	}
	if strings.Contains(body, "bot.example") {
		t.Error("referrers of bot clicks must not be listed")
	}

	today := time.Now().UTC().Format("2006-01-02")
	threeDaysAgo := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	if got := strings.Count(body, `<td>20`); got != 30 {
		t.Errorf("daily table has %d rows, want 30", got)
	}
	todayIndex, olderIndex := strings.Index(body, "<td>"+today+"</td>"), strings.Index(body, "<td>"+threeDaysAgo+"</td>")
	if todayIndex < 0 || olderIndex < 0 || todayIndex > olderIndex {
		t.Errorf("the daily table should list today before older days (today at %d, 3 days ago at %d)", todayIndex, olderIndex)
	}
	if !strings.Contains(body, "<td>"+today+"</td>\n        <td class=\"number\">3</td>") {
		t.Error("today's row should show 3 clicks")
	}
	// news.example (2 clicks) is listed before blog.example (1).
	if strings.Index(body, "news.example") > strings.Index(body, "blog.example") {
		t.Error("referrers should be ordered by clicks, highest first")
	}
}

func TestLinkDetailWithoutClicks(t *testing.T) {
	app := newTestApp(t)
	link := app.seedLink(t, "quiet", "https://example.com/quiet", "")
	cookie, _ := app.signIn(t)

	body := app.send(http.MethodGet, "/admin/links/"+link.ID, nil, cookie).Body.String()
	if !strings.Contains(body, "No referrer data yet.") {
		t.Error("an empty referrer list should say so")
	}
	if strings.Count(body, `<td class="number">0</td>`) < 33 { // 3 totals + 30 days
		t.Errorf("a link without clicks should show zeros everywhere")
	}
	if strings.Contains(body, "<dt>Note</dt>") {
		t.Error("the note row should be hidden when there is no note")
	}
}

func TestLinkDetailUnknownIDIs404(t *testing.T) {
	app := newTestApp(t)
	cookie, _ := app.signIn(t)
	response := app.send(http.MethodGet, "/admin/links/does-not-exist", nil, cookie)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "Link not found") {
		t.Errorf("status %d body %q", response.Code, response.Body.String())
	}
}

func TestLinkDetailRequiresSignIn(t *testing.T) {
	app := newTestApp(t)
	link := app.seedLink(t, "hello", "https://example.com/target", "")
	response := app.send(http.MethodGet, "/admin/links/"+link.ID, nil, nil)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/login" {
		t.Errorf("status %d location %q, want redirect to /admin/login", response.Code, response.Header().Get("Location"))
	}
	if strings.Contains(response.Body.String(), "hello") {
		t.Error("an unauthenticated response must not reveal link data")
	}
}

func TestLinkDetailEscapesUserContent(t *testing.T) {
	app := newTestApp(t)
	link := app.seedLink(t, "xss", "https://example.com/ok", `<img src=x onerror=alert(1)>`)
	cookie, _ := app.signIn(t)
	body := app.send(http.MethodGet, "/admin/links/"+link.ID, nil, cookie).Body.String()
	if strings.Contains(body, "<img src=x") {
		t.Errorf("the note must be escaped: %s", body)
	}
}
