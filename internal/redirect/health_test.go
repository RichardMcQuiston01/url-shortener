package redirect

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

type failingPinger struct{}

func (failingPinger) Ping(context.Context) error { return errors.New("disk on fire") }

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHealthHandlerOKWithRealDatabase(t *testing.T) {
	sqlite, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlite.Close()

	recorder := httptest.NewRecorder()
	HealthHandler(sqlite, discardLogger())(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "ok" {
		t.Errorf("got %d %q, want 200 ok", recorder.Code, recorder.Body.String())
	}
}

func TestHealthHandlerReports503WithoutLeakingDetail(t *testing.T) {
	recorder := httptest.NewRecorder()
	HealthHandler(failingPinger{}, discardLogger())(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "disk on fire") {
		t.Error("response leaks internal error detail")
	}
}
