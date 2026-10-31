package redirect

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const robotsTxt string = "User-agent: *\nDisallow: /admin\nDisallow: /api\n"

// LinkFinder looks up a link by slug. store.LinkStore satisfies it.
type LinkFinder interface {
	GetBySlug(ctx context.Context, slug string) (store.Link, error)
}

// Handler serves the public routes: GET /{slug}, GET / and GET /robots.txt.
// (GET /healthz is HealthHandler.) Go's mux answers HEAD for GET patterns, so
// HEAD requests redirect too.
type Handler struct {
	links           LinkFinder
	recorder        ClickRecorder
	homeRedirectURL string
	logger          *slog.Logger
}

// NewHandler builds the public handler. recorder receives one click per
// redirected GET (never HEAD). homeRedirectURL may be empty, in which case
// GET / returns a plain 404.
func NewHandler(links LinkFinder, recorder ClickRecorder, homeRedirectURL string, logger *slog.Logger) *Handler {
	return &Handler{links: links, recorder: recorder, homeRedirectURL: homeRedirectURL, logger: logger}
}

// Register adds the public routes to mux.
func (handler *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{slug}", handler.serveSlug)
	mux.HandleFunc("GET /{$}", handler.serveHome)
	mux.HandleFunc("GET /robots.txt", handler.serveRobots)
}

func (handler *Handler) serveSlug(responseWriter http.ResponseWriter, request *http.Request) {
	slug := request.PathValue("slug")

	link, err := handler.links.GetBySlug(request.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(responseWriter)
		return
	}
	if err != nil {
		handler.logger.Error("look up link for redirect", "slug", slug, "error", err)
		http.Error(responseWriter, "something went wrong; please try again later", http.StatusInternalServerError)
		return
	}

	// Record never blocks, so statistics cannot slow or break the redirect.
	// HEAD requests (link checkers, previews) are redirected but not counted.
	if request.Method == http.MethodGet {
		handler.recorder.Record(newClick(link, request, time.Now()))
	}

	// 302, not 301, so targets can change later without browsers caching them.
	http.Redirect(responseWriter, request, link.TargetURL, http.StatusFound)
}

func (handler *Handler) serveHome(responseWriter http.ResponseWriter, request *http.Request) {
	if handler.homeRedirectURL == "" {
		writeNotFound(responseWriter)
		return
	}
	http.Redirect(responseWriter, request, handler.homeRedirectURL, http.StatusFound)
}

func (handler *Handler) serveRobots(responseWriter http.ResponseWriter, _ *http.Request) {
	responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = responseWriter.Write([]byte(robotsTxt))
}

func writeNotFound(responseWriter http.ResponseWriter) {
	http.Error(responseWriter, "link not found", http.StatusNotFound)
}
