package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

func (app *testApp) seedLink(t *testing.T, slug string, targetURL string, note string) store.Link {
	t.Helper()
	newLink := store.NewLink{Slug: slug, TargetURL: targetURL}
	if note != "" {
		newLink.Note = &note
	}
	link, err := app.store.Create(context.Background(), newLink)
	if err != nil {
		t.Fatalf("seed link %q: %v", slug, err)
	}
	return link
}

func (app *testApp) addLink(t *testing.T, cookie *http.Cookie, token string, fields url.Values) *httptest.ResponseRecorder {
	t.Helper()
	fields.Set("csrf_token", token)
	return app.send(http.MethodPost, "/admin/links", fields, cookie)
}

func TestDashboardListsLinksWithClickCounts(t *testing.T) {
	app := newTestApp(t)
	link := app.seedLink(t, "hello", "https://example.com/some/long/path", "Chrome ext: Hired Hand")
	app.seedLink(t, "quiet", "https://example.com/quiet", "")
	today := time.Now().UTC()
	if err := app.store.InsertClicks(context.Background(), []store.Click{
		{LinkID: link.ID, ClickedAt: today},
		{LinkID: link.ID, ClickedAt: today},
		{LinkID: link.ID, ClickedAt: today.AddDate(0, 0, -60)},
		{LinkID: link.ID, ClickedAt: today, IsBot: true},
	}); err != nil {
		t.Fatalf("insert clicks: %v", err)
	}

	cookie, _ := app.signIn(t)
	body := app.send(http.MethodGet, "/admin", nil, cookie).Body.String()

	for _, expected := range []string{
		`<a href="/admin/links/` + link.ID + `">hello</a>`,
		`https://go.example.com/hello`,
		`https://example.com/some/long/path`,
		`Chrome ext: Hired Hand`,
		`<td class="number">3</td>`, // total: bots excluded
		`<td class="number">2</td>`, // last 7 and 30 days
		`https://go.example.com/quiet`,
		`<td class="number">0</td>`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("dashboard is missing %q", expected)
		}
	}
	if strings.Contains(body, "No links yet") {
		t.Error("the empty-state message must not appear when links exist")
	}
}

func TestDashboardEmptyState(t *testing.T) {
	app := newTestApp(t)
	cookie, _ := app.signIn(t)
	if body := app.send(http.MethodGet, "/admin", nil, cookie).Body.String(); !strings.Contains(body, "No links yet") {
		t.Error("an empty dashboard should say so")
	}
}

func TestDashboardEscapesUserContent(t *testing.T) {
	app := newTestApp(t)
	app.seedLink(t, "xss", `https://example.com/?q="><script>alert(1)</script>`, `<script>alert("note")</script>`)
	cookie, _ := app.signIn(t)

	body := app.send(http.MethodGet, "/admin", nil, cookie).Body.String()
	if strings.Contains(body, "<script>alert") {
		t.Errorf("user content must be HTML-escaped: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the escaped note to appear")
	}
}

func TestAddLinkShowsShortURLOnceAfterRedirect(t *testing.T) {
	app := newTestApp(t)
	cookie, token := app.signIn(t)

	response := app.addLink(t, cookie, token, url.Values{
		"url": {"https://example.org/a/long/path"}, "slug": {"hello"}, "note": {"demo"},
	})
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin" {
		t.Fatalf("status %d location %q, want redirect back to /admin", response.Code, response.Header().Get("Location"))
	}
	withFlash := sessionCookieFrom(t, response)

	first := app.send(http.MethodGet, "/admin", nil, withFlash)
	if !strings.Contains(first.Body.String(), "Short link created:") ||
		!strings.Contains(first.Body.String(), `<a href="https://go.example.com/hello">https://go.example.com/hello</a>`) {
		t.Errorf("the success message with the new short URL is missing: %s", first.Body.String())
	}

	afterFlash := sessionCookieFrom(t, first)
	second := app.send(http.MethodGet, "/admin", nil, afterFlash)
	if strings.Contains(second.Body.String(), "Short link created:") {
		t.Error("the message must be shown only once")
	}
	if !strings.Contains(second.Body.String(), "hello") {
		t.Error("the new link should be in the table")
	}
	if !afterFlash.Expires.Equal(withFlash.Expires) {
		t.Errorf("clearing the message must not extend the session: %v vs %v", afterFlash.Expires, withFlash.Expires)
	}
}

func TestAddLinkWithoutSlugGeneratesOne(t *testing.T) {
	app := newTestApp(t)
	cookie, token := app.signIn(t)
	response := app.addLink(t, cookie, token, url.Values{"url": {"https://example.org/random"}})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", response.Code)
	}
	links, err := app.store.ListLinkSummaries(context.Background(), 10, 0)
	if err != nil || len(links) != 1 || len(links[0].Slug) != 6 {
		t.Errorf("expected one link with a generated 6-character slug: %+v, %v", links, err)
	}
}

func TestAddLinkForAnExistingURLReusesIt(t *testing.T) {
	app := newTestApp(t)
	cookie, token := app.signIn(t)
	app.addLink(t, cookie, token, url.Values{"url": {"https://example.org/same"}})
	response := app.addLink(t, cookie, token, url.Values{"url": {"https://example.org/same"}})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", response.Code)
	}
	page := app.send(http.MethodGet, "/admin", nil, sessionCookieFrom(t, response))
	if !strings.Contains(page.Body.String(), "That URL already has a short link:") {
		t.Errorf("the page should explain the URL was already shortened: %s", page.Body.String())
	}
	if links, _ := app.store.ListLinkSummaries(context.Background(), 10, 0); len(links) != 1 {
		t.Errorf("no duplicate link should be created, got %d", len(links))
	}
}

func TestAddLinkRejectionsKeepTheFormAndExplainWhy(t *testing.T) {
	app := newTestApp(t)
	app.seedLink(t, "taken", "https://example.com/taken", "")
	cookie, token := app.signIn(t)

	testCases := []struct {
		name            string
		fields          url.Values
		expectedStatus  int
		expectedMessage string
	}{
		{"bad scheme", url.Values{"url": {"ftp://example.org/x"}}, http.StatusBadRequest, `url scheme &#34;ftp&#34; is not allowed`},
		{"own host", url.Values{"url": {"https://go.example.com/x"}}, http.StatusBadRequest, "redirect loop"},
		{"reserved slug", url.Values{"url": {"https://example.org/x"}, "slug": {"admin"}}, http.StatusBadRequest, `slug &#34;admin&#34; is reserved`},
		{"invalid slug", url.Values{"url": {"https://example.org/x"}, "slug": {"a b"}}, http.StatusBadRequest, "invalid character"},
		{"slug taken", url.Values{"url": {"https://example.org/x"}, "slug": {"taken"}}, http.StatusConflict, `is already used by another link`},
		{"note too long", url.Values{"url": {"https://example.org/x"}, "note": {strings.Repeat("n", 501)}}, http.StatusBadRequest, "note is too long"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response := app.addLink(t, cookie, token, testCase.fields)
			if response.Code != testCase.expectedStatus {
				t.Fatalf("status = %d, want %d", response.Code, testCase.expectedStatus)
			}
			body := response.Body.String()
			if !strings.Contains(body, testCase.expectedMessage) {
				t.Errorf("body should contain %q: %s", testCase.expectedMessage, body)
			}
			if !strings.Contains(body, `role="alert"`) {
				t.Error("the error should be announced with role=alert")
			}
			if wanted := testCase.fields.Get("url"); wanted != "" && !strings.Contains(body, `value="`+htmlEscaped(wanted)+`"`) {
				t.Errorf("the entered URL %q should be kept in the form", wanted)
			}
		})
	}
	if links, _ := app.store.ListLinkSummaries(context.Background(), 10, 0); len(links) != 1 {
		t.Errorf("rejected requests must not create links, have %d", len(links))
	}
}

// htmlEscaped mimics html/template's escaping of the characters used in test URLs.
func htmlEscaped(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;").Replace(value)
}

func TestAddLinkRequiresAValidCSRFTokenAndSession(t *testing.T) {
	app := newTestApp(t)
	cookie, token := app.signIn(t)
	fields := func(csrf string) url.Values {
		values := url.Values{"url": {"https://example.org/x"}, "slug": {"sneaky"}}
		if csrf != "" {
			values.Set("csrf_token", csrf)
		}
		return values
	}

	testCases := []struct {
		name   string
		form   url.Values
		cookie *http.Cookie
		status int
	}{
		{"missing token", fields(""), cookie, http.StatusForbidden},
		{"wrong token", fields("not-the-token"), cookie, http.StatusForbidden},
		{"token from another session", fields(token), func() *http.Cookie { other, _ := app.signIn(t); return other }(), http.StatusForbidden},
		{"no session cookie", fields(token), nil, http.StatusForbidden},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if response := app.send(http.MethodPost, "/admin/links", testCase.form, testCase.cookie); response.Code != testCase.status {
				t.Errorf("status = %d, want %d", response.Code, testCase.status)
			}
		})
	}
	if links, _ := app.store.ListLinkSummaries(context.Background(), 10, 0); len(links) != 0 {
		t.Errorf("a CSRF-rejected form created %d links", len(links))
	}
}

func TestAddLinkWithAnonymousSessionRedirectsToLogin(t *testing.T) {
	app := newTestApp(t)
	loginPage := app.send(http.MethodGet, "/admin/login", nil, nil)
	response := app.send(http.MethodPost, "/admin/links",
		url.Values{"url": {"https://example.org/x"}, "csrf_token": {csrfTokenFrom(t, loginPage)}},
		sessionCookieFrom(t, loginPage))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/login" {
		t.Errorf("status %d location %q, want redirect to /admin/login", response.Code, response.Header().Get("Location"))
	}
	if links, _ := app.store.ListLinkSummaries(context.Background(), 10, 0); len(links) != 0 {
		t.Error("an unauthenticated form created a link")
	}
}

func TestOversizedFormIsRejected(t *testing.T) {
	app := newTestApp(t)
	cookie, token := app.signIn(t)
	response := app.addLink(t, cookie, token, url.Values{"url": {"https://example.org/" + strings.Repeat("a", 20*1024)}})
	if response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a form over 16 KB", response.Code)
	}
}

func TestDashboardPagination(t *testing.T) {
	app := newTestApp(t)
	for index := 0; index < 55; index++ {
		app.seedLink(t, fmt.Sprintf("slug%02d", index), fmt.Sprintf("https://example.com/%d", index), "")
	}
	cookie, _ := app.signIn(t)

	first := app.send(http.MethodGet, "/admin", nil, cookie).Body.String()
	if strings.Count(first, `<td class="break"><a href="https://go.example.com/`) != 50 ||
		!strings.Contains(first, `rel="next"`) || strings.Contains(first, `rel="prev"`) {
		t.Errorf("page 1 should have 50 rows and only a Next link")
	}
	second := app.send(http.MethodGet, "/admin?page=2", nil, cookie).Body.String()
	if strings.Count(second, `<td class="break"><a href="https://go.example.com/`) != 5 ||
		strings.Contains(second, `rel="next"`) || !strings.Contains(second, `rel="prev"`) {
		t.Errorf("page 2 should have 5 rows and only a Previous link")
	}
	for _, bad := range []string{"0", "-3", "abc", "99999999999999999999"} {
		if body := app.send(http.MethodGet, "/admin?page="+bad, nil, cookie).Body.String(); !strings.Contains(body, "Page 1") {
			t.Errorf("page=%q should fall back to page 1", bad)
		}
	}
}
