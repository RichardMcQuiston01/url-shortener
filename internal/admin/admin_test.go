package admin

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/links"
	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const (
	testPassword string = "correct-horse-battery"
	testSecret   string = "0123456789abcdef0123456789abcdef"
	testBaseURL  string = "https://go.example.com"
)

var csrfFieldPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// fakeClock is a manually advanced clock shared by the handler and limiter.
type fakeClock struct{ current time.Time }

func (clock *fakeClock) now() time.Time          { return clock.current }
func (clock *fakeClock) advance(d time.Duration) { clock.current = clock.current.Add(d) }

type testApp struct {
	mux     *http.ServeMux
	clock   *fakeClock
	store   *store.SQLite
	logs    *bytes.Buffer
	handler *Handler
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	return newTestAppWithOptions(t, false)
}

func newTestAppWithOptions(t *testing.T, trustProxy bool) *testApp {
	t.Helper()
	sqlite, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })

	validator, err := links.NewValidator(testBaseURL)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	clock := &fakeClock{current: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	logs := &bytes.Buffer{}

	handler, err := NewHandler(Options{
		AdminPassword: testPassword,
		SessionSecret: testSecret,
		BaseURL:       testBaseURL,
		TrustProxy:    trustProxy,
		Links:         links.NewService(sqlite, validator, links.RandomSlugGenerator{}),
		Stats:         sqlite,
		Logger:        slog.New(slog.NewTextHandler(logs, nil)),
		Now:           clock.now,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	if err := handler.Register(mux); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return &testApp{mux: mux, clock: clock, store: sqlite, logs: logs, handler: handler}
}

// send performs a request; cookie may be nil. Form values are sent as a POST body.
func (app *testApp) send(method string, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request := httptest.NewRequest(method, path, body)
	request.RemoteAddr = "203.0.113.7:51234"
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	app.mux.ServeHTTP(recorder, request)
	return recorder
}

func sessionCookieFrom(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie
		}
	}
	t.Fatalf("response has no %s cookie", sessionCookieName)
	return nil
}

func csrfTokenFrom(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	match := csrfFieldPattern.FindStringSubmatch(recorder.Body.String())
	if match == nil {
		t.Fatalf("page has no csrf_token field: %s", recorder.Body.String())
	}
	return match[1]
}

// signIn performs a real login and returns the authenticated cookie and its CSRF token.
func (app *testApp) signIn(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	loginPage := app.send(http.MethodGet, "/admin/login", nil, nil)
	anonymous := sessionCookieFrom(t, loginPage)
	response := app.send(http.MethodPost, "/admin/login",
		url.Values{"password": {testPassword}, "csrf_token": {csrfTokenFrom(t, loginPage)}}, anonymous)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin" {
		t.Fatalf("sign in: status %d, location %q", response.Code, response.Header().Get("Location"))
	}
	signedIn := sessionCookieFrom(t, response)
	dashboard := app.send(http.MethodGet, "/admin", nil, signedIn)
	return signedIn, csrfTokenFrom(t, dashboard)
}

func TestLoginPageSetsHardenedCookieAndCSRFField(t *testing.T) {
	app := newTestApp(t)
	recorder := app.send(http.MethodGet, "/admin/login", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	cookie := sessionCookieFrom(t, recorder)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/admin" {
		t.Errorf("cookie flags = %+v, want HttpOnly, Secure, SameSite=Lax, Path=/admin", cookie)
	}
	if !cookie.Expires.After(app.clock.now()) {
		t.Errorf("cookie must carry an expiry in the future, got %v", cookie.Expires)
	}
	csrfTokenFrom(t, recorder)
}

func TestSecurityHeadersOnEveryAdminPage(t *testing.T) {
	app := newTestApp(t)
	cookie, _ := app.signIn(t)
	for _, request := range []struct {
		path   string
		cookie *http.Cookie
	}{{"/admin/login", nil}, {"/admin", cookie}, {"/admin", nil}} {
		recorder := app.send(http.MethodGet, request.path, nil, request.cookie)
		headers := recorder.Header()
		if headers.Get("X-Robots-Tag") != "noindex" || headers.Get("X-Content-Type-Options") != "nosniff" ||
			headers.Get("X-Frame-Options") != "DENY" || headers.Get("Referrer-Policy") != "no-referrer" ||
			headers.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: missing security headers: %v", request.path, headers)
		}
		policy := headers.Get("Content-Security-Policy")
		if !strings.Contains(policy, "default-src 'none'") || strings.Contains(policy, "unsafe-inline") || strings.Contains(policy, "script-src") {
			t.Errorf("%s: CSP %q must forbid scripts and inline code", request.path, policy)
		}
		if strings.Contains(strings.ToLower(recorder.Body.String()), "<script") {
			t.Errorf("%s: page must not contain scripts", request.path)
		}
	}
}

func TestLoginSucceedsAndRedirectsToDashboard(t *testing.T) {
	app := newTestApp(t)
	cookie, _ := app.signIn(t)
	if app.send(http.MethodGet, "/admin", nil, cookie).Code != http.StatusOK {
		t.Error("dashboard should be reachable after signing in")
	}
	// An already signed-in user visiting the login page is sent to the dashboard.
	if response := app.send(http.MethodGet, "/admin/login", nil, cookie); response.Code != http.StatusSeeOther {
		t.Errorf("login page for a signed-in user: status %d, want redirect", response.Code)
	}
}

func TestLoginFailureIsRejectedLoggedAndNeverLogsThePassword(t *testing.T) {
	app := newTestApp(t)
	loginPage := app.send(http.MethodGet, "/admin/login", nil, nil)
	anonymous := sessionCookieFrom(t, loginPage)

	response := app.send(http.MethodPost, "/admin/login",
		url.Values{"password": {"wrong-secret-guess"}, "csrf_token": {csrfTokenFrom(t, loginPage)}}, anonymous)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "Incorrect password.") {
		t.Fatalf("status %d body %q", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.MaxAge >= 0 {
			if decoded, err := app.handler.codec.decode(cookie.Value, app.clock.now()); err == nil && decoded.Authenticated {
				t.Error("a failed login must not produce an authenticated session")
			}
		}
	}
	if !strings.Contains(app.logs.String(), "admin login failed") || !strings.Contains(app.logs.String(), "203.0.113.7") {
		t.Errorf("failure should be logged with the client IP: %s", app.logs.String())
	}
	if strings.Contains(app.logs.String(), "wrong-secret-guess") || strings.Contains(app.logs.String(), testPassword) {
		t.Errorf("log leaks a password: %s", app.logs.String())
	}
}

func TestLoginRateLimit(t *testing.T) {
	app := newTestApp(t)
	loginPage := app.send(http.MethodGet, "/admin/login", nil, nil)
	anonymous := sessionCookieFrom(t, loginPage)
	token := csrfTokenFrom(t, loginPage)
	attempt := func(password string) *httptest.ResponseRecorder {
		return app.send(http.MethodPost, "/admin/login",
			url.Values{"password": {password}, "csrf_token": {token}}, anonymous)
	}

	for failure := 1; failure <= 5; failure++ {
		if response := attempt("wrong"); response.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: status %d, want 401", failure, response.Code)
		}
	}
	blocked := attempt(testPassword) // even the right password is refused while blocked
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("after 5 failures: status %d retry-after %q, want 429 with Retry-After", blocked.Code, blocked.Header().Get("Retry-After"))
	}
	if !strings.Contains(blocked.Body.String(), "Too many failed sign-in attempts") {
		t.Errorf("blocked page should explain itself: %s", blocked.Body.String())
	}

	app.clock.advance(15*time.Minute + time.Second)
	if response := attempt(testPassword); response.Code != http.StatusSeeOther {
		t.Errorf("after the window passes: status %d, want a successful login", response.Code)
	}
}

func TestSuccessfulLoginResetsTheFailureCount(t *testing.T) {
	app := newTestApp(t)
	loginPage := app.send(http.MethodGet, "/admin/login", nil, nil)
	anonymous := sessionCookieFrom(t, loginPage)
	token := csrfTokenFrom(t, loginPage)
	attempt := func(password string) int {
		return app.send(http.MethodPost, "/admin/login",
			url.Values{"password": {password}, "csrf_token": {token}}, anonymous).Code
	}
	for i := 0; i < 4; i++ {
		attempt("wrong")
	}
	if attempt(testPassword) != http.StatusSeeOther {
		t.Fatal("login with 4 prior failures should still work")
	}
	for i := 0; i < 4; i++ {
		if attempt("wrong") != http.StatusUnauthorized {
			t.Fatal("the counter should have been reset by the successful login")
		}
	}
}

func TestLoginRejectsMissingOrWrongCSRF(t *testing.T) {
	app := newTestApp(t)
	loginPage := app.send(http.MethodGet, "/admin/login", nil, nil)
	anonymous := sessionCookieFrom(t, loginPage)
	validToken := csrfTokenFrom(t, loginPage)
	tampered := &http.Cookie{Name: sessionCookieName, Value: anonymous.Value + "x"}

	testCases := []struct {
		name   string
		form   url.Values
		cookie *http.Cookie
	}{
		{"no token", url.Values{"password": {testPassword}}, anonymous},
		{"wrong token", url.Values{"password": {testPassword}, "csrf_token": {"nope"}}, anonymous},
		{"no cookie", url.Values{"password": {testPassword}, "csrf_token": {validToken}}, nil},
		{"tampered cookie", url.Values{"password": {testPassword}, "csrf_token": {validToken}}, tampered},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response := app.send(http.MethodPost, "/admin/login", testCase.form, testCase.cookie)
			if response.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", response.Code)
			}
			for _, cookie := range response.Result().Cookies() {
				if cookie.Name == sessionCookieName {
					t.Error("a rejected login must not set a session cookie")
				}
			}
		})
	}
}

func TestDashboardRequiresASignedInSession(t *testing.T) {
	app := newTestApp(t)
	loginPage := app.send(http.MethodGet, "/admin/login", nil, nil)
	anonymous := sessionCookieFrom(t, loginPage)
	signedIn, _ := app.signIn(t)

	testCases := []struct {
		name   string
		cookie *http.Cookie
	}{
		{"no cookie", nil},
		{"anonymous login session", anonymous},
		{"tampered signature", &http.Cookie{Name: sessionCookieName, Value: signedIn.Value + "x"}},
		{"garbage", &http.Cookie{Name: sessionCookieName, Value: "not-a-session"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response := app.send(http.MethodGet, "/admin", nil, testCase.cookie)
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/login" {
				t.Errorf("status %d location %q, want redirect to /admin/login", response.Code, response.Header().Get("Location"))
			}
		})
	}
}

func TestSessionExpiresAfterTwelveHours(t *testing.T) {
	app := newTestApp(t)
	cookie, _ := app.signIn(t)

	app.clock.advance(11*time.Hour + 59*time.Minute)
	if app.send(http.MethodGet, "/admin", nil, cookie).Code != http.StatusOK {
		t.Error("the session should still be valid just before 12 hours")
	}
	app.clock.advance(2 * time.Minute)
	if response := app.send(http.MethodGet, "/admin", nil, cookie); response.Code != http.StatusSeeOther {
		t.Errorf("the session should have expired after 12 hours, got status %d", response.Code)
	}
}

func TestSessionSignedWithAnotherSecretIsRejected(t *testing.T) {
	app := newTestApp(t)
	forgedSession, err := newSession(app.clock.now(), true)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	forgedValue, err := sessionCodec{secret: []byte("some-other-secret-some-other-secret")}.encode(forgedSession)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	response := app.send(http.MethodGet, "/admin", nil, &http.Cookie{Name: sessionCookieName, Value: forgedValue})
	if response.Code != http.StatusSeeOther {
		t.Errorf("a forged session must be rejected, got status %d", response.Code)
	}
}

func TestLogout(t *testing.T) {
	app := newTestApp(t)
	cookie, token := app.signIn(t)

	if response := app.send(http.MethodPost, "/admin/logout", url.Values{}, cookie); response.Code != http.StatusForbidden {
		t.Errorf("logout without a CSRF token: status %d, want 403", response.Code)
	}
	response := app.send(http.MethodPost, "/admin/logout", url.Values{"csrf_token": {token}}, cookie)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/login" {
		t.Fatalf("logout: status %d location %q", response.Code, response.Header().Get("Location"))
	}
	cleared := sessionCookieFrom(t, response)
	if cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("logout should clear the cookie, got %+v", cleared)
	}
}

func TestLoginPagesSendNoWrongSessionAfterReload(t *testing.T) {
	app := newTestApp(t)
	first := app.send(http.MethodGet, "/admin/login", nil, nil)
	anonymous := sessionCookieFrom(t, first)
	second := app.send(http.MethodGet, "/admin/login", nil, anonymous)
	if csrfTokenFrom(t, second) != csrfTokenFrom(t, first) {
		t.Error("reloading the login page with a valid session must keep its CSRF token (so open tabs keep working)")
	}
}

func TestSessionCodec(t *testing.T) {
	codec := sessionCodec{secret: []byte(testSecret)}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	original, err := newSession(now, true)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	original.Flash = &flashMessage{Kind: "created", Slug: "hello"}

	value, err := codec.encode(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := codec.decode(value, now)
	if err != nil || decoded.CSRFToken != original.CSRFToken || !decoded.Authenticated ||
		decoded.Flash == nil || decoded.Flash.Slug != "hello" {
		t.Errorf("round trip failed: %+v, %v", decoded, err)
	}

	payload, mac, _ := strings.Cut(value, ".")
	invalid := map[string]string{
		"no separator":      payload,
		"empty":             "",
		"altered payload":   payload[:len(payload)-2] + "AA." + mac,
		"altered signature": payload + "." + mac[:len(mac)-2] + "AA",
		"swapped parts":     mac + "." + payload,
	}
	for name, candidate := range invalid {
		if _, err := codec.decode(candidate, now); err == nil {
			t.Errorf("%s: decode should fail", name)
		}
	}
	if _, err := codec.decode(value, now.Add(13*time.Hour)); err == nil {
		t.Error("an expired session must not decode")
	}
}

func TestClientIP(t *testing.T) {
	testCases := []struct {
		name       string
		trustProxy bool
		forwarded  string
		expected   string
	}{
		{"direct connection", false, "", "203.0.113.7"},
		{"forwarded header ignored when not trusted", false, "198.51.100.1", "203.0.113.7"},
		{"trusted proxy uses forwarded address", true, "198.51.100.1", "198.51.100.1"},
		{"the proxy-appended entry wins over a forged one", true, "6.6.6.6, 198.51.100.1", "198.51.100.1"},
		{"trusted but no header falls back to the socket", true, "", "203.0.113.7"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
			request.RemoteAddr = "203.0.113.7:51234"
			if testCase.forwarded != "" {
				request.Header.Set("X-Forwarded-For", testCase.forwarded)
			}
			if actual := clientIP(request, testCase.trustProxy); actual != testCase.expected {
				t.Errorf("clientIP = %q, want %q", actual, testCase.expected)
			}
		})
	}
}

func TestRateLimitIsPerClientIP(t *testing.T) {
	app := newTestAppWithOptions(t, true)
	loginPage := app.send(http.MethodGet, "/admin/login", nil, nil)
	anonymous := sessionCookieFrom(t, loginPage)
	token := csrfTokenFrom(t, loginPage)

	attemptFrom := func(address string, password string) int {
		request := httptest.NewRequest(http.MethodPost, "/admin/login",
			strings.NewReader(url.Values{"password": {password}, "csrf_token": {token}}.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("X-Forwarded-For", address)
		request.AddCookie(anonymous)
		recorder := httptest.NewRecorder()
		app.mux.ServeHTTP(recorder, request)
		return recorder.Code
	}
	for i := 0; i < 5; i++ {
		attemptFrom("198.51.100.1", "wrong")
	}
	if attemptFrom("198.51.100.1", testPassword) != http.StatusTooManyRequests {
		t.Error("the abusive client should be blocked")
	}
	if attemptFrom("198.51.100.2", testPassword) != http.StatusSeeOther {
		t.Error("a different client must not be affected")
	}
}

func TestStaticCSSIsServed(t *testing.T) {
	app := newTestApp(t)
	recorder := app.send(http.MethodGet, "/static/admin.css", nil, nil)
	if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/css") {
		t.Errorf("status %d content-type %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	if app.send(http.MethodGet, "/static/missing.css", nil, nil).Code != http.StatusNotFound {
		t.Error("unknown static files should 404")
	}
}

func TestNewHandlerRejectsEmptySecrets(t *testing.T) {
	for name, options := range map[string]Options{
		"no password": {SessionSecret: testSecret},
		"no secret":   {AdminPassword: testPassword},
	} {
		if _, err := NewHandler(options); err == nil {
			t.Errorf("%s: NewHandler should fail", name)
		}
	}
}
