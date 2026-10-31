package redirect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

type fakeFinder struct {
	links map[string]store.Link
	err   error
}

func (finder fakeFinder) GetBySlug(_ context.Context, slug string) (store.Link, error) {
	if finder.err != nil {
		return store.Link{}, finder.err
	}
	link, found := finder.links[slug]
	if !found {
		return store.Link{}, store.ErrNotFound
	}
	return link, nil
}

// fakeRecorder collects clicks; accept=false simulates a full queue.
type fakeRecorder struct {
	mutex  sync.Mutex
	clicks []store.Click
	reject bool
}

func (recorder *fakeRecorder) Record(click store.Click) bool {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.clicks = append(recorder.clicks, click)
	return !recorder.reject
}

func (recorder *fakeRecorder) Close(context.Context) error { return nil }

func (recorder *fakeRecorder) recorded() []store.Click {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]store.Click(nil), recorder.clicks...)
}

func newTestMux(finder LinkFinder, homeRedirectURL string) *http.ServeMux {
	return newTestMuxWithRecorder(finder, &fakeRecorder{}, homeRedirectURL)
}

func newTestMuxWithRecorder(finder LinkFinder, recorder ClickRecorder, homeRedirectURL string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(responseWriter http.ResponseWriter, _ *http.Request) {
		_, _ = responseWriter.Write([]byte("ok\n"))
	})
	NewHandler(finder, recorder, homeRedirectURL, discardLogger()).Register(mux)
	return mux
}

func serve(mux *http.ServeMux, method string, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

func TestSlugRoutes(t *testing.T) {
	finder := fakeFinder{links: map[string]store.Link{
		"hello": {Slug: "hello", TargetURL: "https://example.com/some/long/path?q=1"},
		"Hello": {Slug: "Hello", TargetURL: "https://example.com/capital"},
	}}
	mux := newTestMux(finder, "")

	testCases := []struct {
		name             string
		method           string
		path             string
		expectedStatus   int
		expectedLocation string
	}{
		{"known slug redirects with 302", http.MethodGet, "/hello", http.StatusFound, "https://example.com/some/long/path?q=1"},
		{"slugs are case-sensitive", http.MethodGet, "/Hello", http.StatusFound, "https://example.com/capital"},
		{"HEAD redirects too", http.MethodHead, "/hello", http.StatusFound, "https://example.com/some/long/path?q=1"},
		{"unknown slug is 404", http.MethodGet, "/nope", http.StatusNotFound, ""},
		{"POST is not allowed", http.MethodPost, "/hello", http.StatusMethodNotAllowed, ""},
		{"nested path is 404", http.MethodGet, "/hello/extra", http.StatusNotFound, ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := serve(mux, testCase.method, testCase.path)
			if recorder.Code != testCase.expectedStatus {
				t.Errorf("status = %d, want %d", recorder.Code, testCase.expectedStatus)
			}
			if location := recorder.Header().Get("Location"); location != testCase.expectedLocation {
				t.Errorf("Location = %q, want %q", location, testCase.expectedLocation)
			}
		})
	}
}

func TestUnknownSlugBodyIsShortAndPlain(t *testing.T) {
	recorder := serve(newTestMux(fakeFinder{}, ""), http.MethodGet, "/nope")
	if !strings.Contains(recorder.Body.String(), "link not found") {
		t.Errorf("body = %q, want a short not-found message", recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", contentType)
	}
}

func TestLookupFailureIs500WithoutLeakingDetail(t *testing.T) {
	mux := newTestMux(fakeFinder{err: errors.New("disk on fire")}, "")
	recorder := serve(mux, http.MethodGet, "/hello")
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "disk on fire") {
		t.Error("response leaks internal error detail")
	}
}

func TestHomeRoute(t *testing.T) {
	withHome := serve(newTestMux(fakeFinder{}, "https://richardmcquiston.com"), http.MethodGet, "/")
	if withHome.Code != http.StatusFound || withHome.Header().Get("Location") != "https://richardmcquiston.com" {
		t.Errorf("with HOME_REDIRECT_URL: %d %q", withHome.Code, withHome.Header().Get("Location"))
	}
	withoutHome := serve(newTestMux(fakeFinder{}, ""), http.MethodGet, "/")
	if withoutHome.Code != http.StatusNotFound {
		t.Errorf("without HOME_REDIRECT_URL: status = %d, want 404", withoutHome.Code)
	}
}

func TestRobotsTxt(t *testing.T) {
	recorder := serve(newTestMux(fakeFinder{}, ""), http.MethodGet, "/robots.txt")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	for _, expected := range []string{"Disallow: /admin", "Disallow: /api"} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("robots.txt missing %q: %q", expected, recorder.Body.String())
		}
	}
}

func TestSlugRouteDoesNotShadowHealthz(t *testing.T) {
	recorder := serve(newTestMux(fakeFinder{}, ""), http.MethodGet, "/healthz")
	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 from the healthz route", recorder.Code)
	}
}

func TestRedirectWithRealStore(t *testing.T) {
	ctx := context.Background()
	sqlite, err := store.Open(ctx, filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlite.Close()
	if _, err := sqlite.Create(ctx, store.NewLink{Slug: "ext", TargetURL: "https://example.com/extension"}); err != nil {
		t.Fatalf("create link: %v", err)
	}

	mux := newTestMux(sqlite, "")
	found := serve(mux, http.MethodGet, "/ext")
	if found.Code != http.StatusFound || found.Header().Get("Location") != "https://example.com/extension" {
		t.Errorf("known slug: %d %q", found.Code, found.Header().Get("Location"))
	}
	if missing := serve(mux, http.MethodGet, "/missing"); missing.Code != http.StatusNotFound {
		t.Errorf("unknown slug: status = %d, want 404", missing.Code)
	}
}

func TestRedirectRecordsClickDetails(t *testing.T) {
	recorder := &fakeRecorder{}
	finder := fakeFinder{links: map[string]store.Link{"hello": {ID: "link-1", Slug: "hello", TargetURL: "https://example.com/x"}}}
	mux := newTestMuxWithRecorder(finder, recorder, "")

	request := httptest.NewRequest(http.MethodGet, "/hello", nil)
	request.Header.Set("Referer", "https://News.Example.com:8443/story?id=7")
	request.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0")
	before := time.Now().UTC()
	mux.ServeHTTP(httptest.NewRecorder(), request)

	clicks := recorder.recorded()
	if len(clicks) != 1 {
		t.Fatalf("recorded %d clicks, want 1", len(clicks))
	}
	click := clicks[0]
	if click.LinkID != "link-1" || click.IsBot {
		t.Errorf("click = %+v, want link-1 and not a bot", click)
	}
	if click.ReferrerHost == nil || *click.ReferrerHost != "news.example.com" {
		t.Errorf("referrer host = %v, want news.example.com (host only)", click.ReferrerHost)
	}
	if click.ClickedAt.Before(before.Add(-time.Second)) || click.ClickedAt.Location() != time.UTC {
		t.Errorf("clicked_at = %v, want a recent UTC time", click.ClickedAt)
	}
}

func TestOnlyCountedRequestsAreRecorded(t *testing.T) {
	finder := fakeFinder{links: map[string]store.Link{"hello": {ID: "link-1", Slug: "hello", TargetURL: "https://example.com/x"}}}
	testCases := []struct {
		name          string
		method        string
		path          string
		expectedCount int
	}{
		{"GET is counted", http.MethodGet, "/hello", 1},
		{"HEAD is not counted", http.MethodHead, "/hello", 0},
		{"unknown slug is not counted", http.MethodGet, "/nope", 0},
		{"robots.txt is not counted", http.MethodGet, "/robots.txt", 0},
		{"home is not counted", http.MethodGet, "/", 0},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := &fakeRecorder{}
			serve(newTestMuxWithRecorder(finder, recorder, "https://richardmcquiston.com"), testCase.method, testCase.path)
			if count := len(recorder.recorded()); count != testCase.expectedCount {
				t.Errorf("recorded %d clicks, want %d", count, testCase.expectedCount)
			}
		})
	}
}

func TestRedirectStillWorksWhenRecorderRejectsClicks(t *testing.T) {
	finder := fakeFinder{links: map[string]store.Link{"hello": {ID: "link-1", Slug: "hello", TargetURL: "https://example.com/x"}}}
	mux := newTestMuxWithRecorder(finder, &fakeRecorder{reject: true}, "")
	recorder := serve(mux, http.MethodGet, "/hello")
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "https://example.com/x" {
		t.Errorf("a full click queue must not affect the redirect: %d %q", recorder.Code, recorder.Header().Get("Location"))
	}
}
