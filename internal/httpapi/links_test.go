package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardmcquiston01/url-shortener/internal/links"
	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const testBaseURL string = "https://go.example.com"

// sequenceGenerator returns the given slugs in order, then repeats the last.
type sequenceGenerator struct {
	slugs []string
	calls int
}

func (generator *sequenceGenerator) Generate() (string, error) {
	index := generator.calls
	if index >= len(generator.slugs) {
		index = len(generator.slugs) - 1
	}
	generator.calls++
	return generator.slugs[index], nil
}

// brokenLookupStore fails GetByTargetURL to exercise the internal_error path.
type brokenLookupStore struct{ store.LinkStore }

func (brokenLookupStore) GetByTargetURL(context.Context, string) (store.Link, error) {
	return store.Link{}, errors.New("disk on fire")
}

type testServer struct {
	mux       *http.ServeMux
	linkStore *store.SQLite
}

func newTestServer(t *testing.T, generator links.SlugGenerator) testServer {
	t.Helper()
	sqlite, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return testServer{mux: buildMux(t, sqlite, generator), linkStore: sqlite}
}

func buildMux(t *testing.T, linkStore store.LinkStore, generator links.SlugGenerator) *http.ServeMux {
	t.Helper()
	validator, err := links.NewValidator(testBaseURL)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	mux := http.NewServeMux()
	NewHandler(linkStore, validator, generator, testBaseURL, discardLogger()).Register(mux, testAPIKey)
	return mux
}

func (server testServer) do(method string, path string, body string) *httptest.ResponseRecorder {
	return doRequest(server.mux, method, path, body, "Bearer "+testAPIKey)
}

func doRequest(mux *http.ServeMux, method string, path string, body string, authorization string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

func decodeLink(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, recorder.Body.String())
	}
	return decoded
}

func assertError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code ErrorCode, messagePart string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", recorder.Code, status, recorder.Body.String())
	}
	var body ErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the error shape: %v: %s", err, recorder.Body.String())
	}
	if body.Error != code {
		t.Errorf("error code = %q, want %q", body.Error, code)
	}
	if !strings.Contains(body.Message, messagePart) {
		t.Errorf("message %q should contain %q", body.Message, messagePart)
	}
}

func TestCreateLinkWithCustomSlug(t *testing.T) {
	server := newTestServer(t, &sequenceGenerator{slugs: []string{"unused"}})
	recorder := server.do(http.MethodPost, "/api/links",
		`{"url":"https://example.com/some/long/path","slug":"hello","note":"  Chrome ext: Hired Hand  "}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	body := decodeLink(t, recorder)
	if body["slug"] != "hello" || body["short_url"] != "https://go.example.com/hello" ||
		body["target_url"] != "https://example.com/some/long/path" || body["note"] != "Chrome ext: Hired Hand" {
		t.Errorf("unexpected body: %v", body)
	}
	if id, _ := body["id"].(string); len(id) != 36 {
		t.Errorf("id %q should be a UUID", body["id"])
	}
	if createdAt, _ := body["created_at"].(string); !strings.HasSuffix(createdAt, "Z") {
		t.Errorf("created_at %q should be RFC 3339 UTC", body["created_at"])
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", contentType)
	}
}

func TestCreateLinkWithoutNoteReturnsNullNote(t *testing.T) {
	server := newTestServer(t, &sequenceGenerator{slugs: []string{"abc234"}})
	body := decodeLink(t, server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/a"}`))
	if value, present := body["note"]; !present || value != nil {
		t.Errorf("note = %v (present %v), want JSON null", value, present)
	}
}

func TestCreateLinkGeneratesSlugAndReturnsExistingForSameURL(t *testing.T) {
	server := newTestServer(t, links.RandomSlugGenerator{})

	first := server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/same"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201: %s", first.Code, first.Body.String())
	}
	firstBody := decodeLink(t, first)
	if slug, _ := firstBody["slug"].(string); len(slug) != links.GeneratedSlugLength {
		t.Errorf("generated slug %q should have %d characters", firstBody["slug"], links.GeneratedSlugLength)
	}

	second := server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/same"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200 for an existing URL", second.Code)
	}
	if decodeLink(t, second)["id"] != firstBody["id"] {
		t.Error("second request should return the existing link, not create a duplicate")
	}
}

func TestCreateLinkWithCustomSlugAllowsSameURLAgain(t *testing.T) {
	server := newTestServer(t, links.RandomSlugGenerator{})
	server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/same"}`)
	recorder := server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/same","slug":"second"}`)
	if recorder.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201 when a slug is supplied", recorder.Code)
	}
}

func TestCreateLinkErrors(t *testing.T) {
	server := newTestServer(t, &sequenceGenerator{slugs: []string{"unused"}})
	server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/taken","slug":"hello"}`)

	testCases := []struct {
		name            string
		body            string
		expectedStatus  int
		expectedCode    ErrorCode
		expectedMessage string
	}{
		{"slug taken", `{"url":"https://example.com/b","slug":"hello"}`, http.StatusConflict, CodeSlugTaken, `slug "hello" is already used by another link`},
		{"slug reserved", `{"url":"https://example.com/b","slug":"admin"}`, http.StatusBadRequest, CodeSlugReserved, `slug "admin" is reserved`},
		{"slug invalid character", `{"url":"https://example.com/b","slug":"a b"}`, http.StatusBadRequest, CodeInvalidSlug, "invalid character"},
		{"slug too long", `{"url":"https://example.com/b","slug":"` + strings.Repeat("a", 65) + `"}`, http.StatusBadRequest, CodeInvalidSlug, "too long"},
		{"url missing", `{"slug":"x"}`, http.StatusBadRequest, CodeInvalidURL, "must not be empty"},
		{"url bad scheme", `{"url":"ftp://example.com/x"}`, http.StatusBadRequest, CodeInvalidURL, `scheme "ftp"`},
		{"url own host", `{"url":"https://go.example.com/x"}`, http.StatusBadRequest, CodeInvalidURL, "redirect loop"},
		{"url with credentials", `{"url":"https://user:pw@example.com/"}`, http.StatusBadRequest, CodeInvalidURL, "username or password"},
		{"note too long", `{"url":"https://example.com/n","note":"` + strings.Repeat("n", 501) + `"}`, http.StatusBadRequest, CodeInvalidRequest, "note is too long"},
		{"empty body", ``, http.StatusBadRequest, CodeInvalidRequest, "request body is empty"},
		{"malformed json", `{"url":`, http.StatusBadRequest, CodeInvalidRequest, "not valid JSON"},
		{"wrong field type", `{"url":123}`, http.StatusBadRequest, CodeInvalidRequest, `field "url" must be a string`},
		{"unknown field", `{"url":"https://example.com/z","slg":"x"}`, http.StatusBadRequest, CodeInvalidRequest, `unknown field "slg"`},
		{"two objects", `{"url":"https://example.com/z"}{"url":"https://example.com/y"}`, http.StatusBadRequest, CodeInvalidRequest, "single JSON object"},
		{"body too large", `{"url":"https://example.com/` + strings.Repeat("a", 17*1024) + `"}`, http.StatusRequestEntityTooLarge, CodeInvalidRequest, "larger than"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assertError(t, server.do(http.MethodPost, "/api/links", testCase.body),
				testCase.expectedStatus, testCase.expectedCode, testCase.expectedMessage)
		})
	}
}

func TestGeneratedSlugCollisionsAreRetried(t *testing.T) {
	generator := &sequenceGenerator{slugs: []string{"taken1", "static", "fresh2"}}
	server := newTestServer(t, generator)
	server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/existing","slug":"taken1"}`)

	recorder := server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/new"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if slug := decodeLink(t, recorder)["slug"]; slug != "fresh2" {
		t.Errorf("slug = %v, want fresh2 (taken1 is used, static is reserved)", slug)
	}
	if generator.calls != 3 {
		t.Errorf("generator called %d times, want 3", generator.calls)
	}
}

func TestGeneratedSlugExhaustionIsInternalError(t *testing.T) {
	server := newTestServer(t, &sequenceGenerator{slugs: []string{"taken1"}})
	server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/existing","slug":"taken1"}`)

	recorder := server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/new"}`)
	assertError(t, recorder, http.StatusInternalServerError, CodeInternalError, "internal error occurred")
	if strings.Contains(recorder.Body.String(), "attempts") {
		t.Errorf("internal_error message leaks detail: %s", recorder.Body.String())
	}
}

func TestStoreFailureIsInternalErrorWithoutDetail(t *testing.T) {
	realServer := newTestServer(t, links.RandomSlugGenerator{})
	mux := buildMux(t, brokenLookupStore{realServer.linkStore}, links.RandomSlugGenerator{})

	recorder := doRequest(mux, http.MethodPost, "/api/links", `{"url":"https://example.com/x"}`, "Bearer "+testAPIKey)
	assertError(t, recorder, http.StatusInternalServerError, CodeInternalError, "internal error occurred")
	if strings.Contains(recorder.Body.String(), "disk on fire") {
		t.Errorf("internal_error message leaks detail: %s", recorder.Body.String())
	}
}

func TestEveryRouteRequiresTheAPIKey(t *testing.T) {
	server := newTestServer(t, links.RandomSlugGenerator{})
	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/links"},
		{http.MethodGet, "/api/links"},
		{http.MethodGet, "/api/links/hello"},
		{http.MethodGet, "/api/anything-else"},
	}
	for _, route := range routes {
		for name, authorization := range map[string]string{"missing": "", "wrong": "Bearer nope"} {
			t.Run(route.method+" "+route.path+" "+name, func(t *testing.T) {
				recorder := doRequest(server.mux, route.method, route.path, `{"url":"https://example.com"}`, authorization)
				assertError(t, recorder, http.StatusUnauthorized, CodeUnauthorized, "")
			})
		}
	}
	if count, _ := server.linkStore.List(context.Background(), 10, 0); len(count) != 0 {
		t.Error("an unauthorized request created a link")
	}
}

func TestListLinks(t *testing.T) {
	server := newTestServer(t, links.RandomSlugGenerator{})
	for _, slug := range []string{"one", "two", "three"} {
		server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/`+slug+`","slug":"`+slug+`"}`)
	}

	recorder := server.do(http.MethodGet, "/api/links?limit=2&offset=1", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Links  []map[string]any `json:"links"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Links) != 2 || response.Limit != 2 || response.Offset != 1 {
		t.Errorf("unexpected response: %+v", response)
	}
	for _, link := range response.Links {
		if link["short_url"] == nil {
			t.Errorf("list item lacks short_url: %v", link)
		}
	}

	defaults := server.do(http.MethodGet, "/api/links", "")
	if !bytes.Contains(defaults.Body.Bytes(), []byte(`"limit":50`)) {
		t.Errorf("default limit should be 50: %s", defaults.Body.String())
	}
	empty := server.do(http.MethodGet, "/api/links?offset=100", "")
	if !bytes.Contains(empty.Body.Bytes(), []byte(`"links":[]`)) {
		t.Errorf("empty page should be [] not null: %s", empty.Body.String())
	}
}

func TestListLinksRejectsBadPaging(t *testing.T) {
	server := newTestServer(t, links.RandomSlugGenerator{})
	testCases := []struct{ query, messagePart string }{
		{"limit=abc", `"limit" must be a whole number`},
		{"limit=0", `"limit" must be between 1 and 200`},
		{"limit=201", `"limit" must be between 1 and 200`},
		{"offset=-1", `"offset" must be between 0`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.query, func(t *testing.T) {
			assertError(t, server.do(http.MethodGet, "/api/links?"+testCase.query, ""),
				http.StatusBadRequest, CodeInvalidRequest, testCase.messagePart)
		})
	}
}

func TestGetLink(t *testing.T) {
	server := newTestServer(t, links.RandomSlugGenerator{})
	server.do(http.MethodPost, "/api/links", `{"url":"https://example.com/one","slug":"one"}`)

	found := server.do(http.MethodGet, "/api/links/one", "")
	if found.Code != http.StatusOK || decodeLink(t, found)["target_url"] != "https://example.com/one" {
		t.Errorf("found: %d %s", found.Code, found.Body.String())
	}
	assertError(t, server.do(http.MethodGet, "/api/links/missing", ""),
		http.StatusNotFound, CodeNotFound, `no link with slug "missing" exists`)
}

func TestUnknownEndpointAndMethodReturnJSONNotFound(t *testing.T) {
	server := newTestServer(t, links.RandomSlugGenerator{})
	assertError(t, server.do(http.MethodGet, "/api/nope", ""), http.StatusNotFound, CodeNotFound, "no API endpoint")
	assertError(t, server.do(http.MethodDelete, "/api/links", ""), http.StatusNotFound, CodeNotFound, "DELETE /api/links")
}
