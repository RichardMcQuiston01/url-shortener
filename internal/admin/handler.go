// Package admin serves the server-rendered admin UI: login, sessions, CSRF
// protection, the dashboard and per-link stats.
package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/links"
	"github.com/richardmcquiston01/url-shortener/internal/store"
	"github.com/richardmcquiston01/url-shortener/web"
)

const (
	sessionCookieName string = "admin_session"
	adminCookiePath   string = "/admin"

	maxFormBodyBytes int64 = 16 * 1024

	contentSecurityPolicy string = "default-src 'none'; style-src 'self'; img-src 'self'; " +
		"form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
)

// Options configures the admin handler. Now may be nil (the wall clock is used).
type Options struct {
	AdminPassword string
	SessionSecret string
	BaseURL       string
	TrustProxy    bool
	Links         *links.Service
	Stats         store.StatsStore
	Logger        *slog.Logger
	Now           func() time.Time
}

// Handler serves everything under /admin plus the /static assets.
type Handler struct {
	passwordHash [sha256.Size]byte
	codec        sessionCodec
	baseURL      string
	trustProxy   bool
	linkService  *links.Service
	stats        store.StatsStore
	logger       *slog.Logger
	now          func() time.Time
	limiter      *loginLimiter
	pages        map[string]*pageTemplate
}

// NewHandler validates the options and parses the embedded templates.
func NewHandler(options Options) (*Handler, error) {
	if options.AdminPassword == "" {
		return nil, errors.New("admin handler needs a non-empty admin password")
	}
	if options.SessionSecret == "" {
		return nil, errors.New("admin handler needs a non-empty session secret")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	handler := &Handler{
		passwordHash: sha256.Sum256([]byte(options.AdminPassword)),
		codec:        sessionCodec{secret: []byte(options.SessionSecret)},
		baseURL:      options.BaseURL,
		trustProxy:   options.TrustProxy,
		linkService:  options.Links,
		stats:        options.Stats,
		logger:       options.Logger,
		now:          now,
		limiter:      newLoginLimiter(now),
	}
	pages, err := parsePages(options.BaseURL)
	if err != nil {
		return nil, err
	}
	handler.pages = pages
	return handler, nil
}

// Register adds the admin routes and the static asset route to mux.
func (handler *Handler) Register(mux *http.ServeMux) error {
	staticFiles, err := fs.Sub(web.Files, "static")
	if err != nil {
		return fmt.Errorf("open embedded static files: %w", err)
	}
	staticServer := http.StripPrefix("/static/", http.FileServerFS(staticFiles))
	mux.HandleFunc("GET /static/", func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Cache-Control", "public, max-age=3600")
		responseWriter.Header().Set("X-Content-Type-Options", "nosniff")
		staticServer.ServeHTTP(responseWriter, request)
	})

	mux.Handle("GET /admin/login", handler.wrap(false, handler.showLogin))
	mux.Handle("POST /admin/login", handler.wrap(false, handler.submitLogin))
	mux.Handle("POST /admin/logout", handler.wrap(true, handler.logout))
	mux.Handle("GET /admin", handler.wrap(true, handler.showDashboard))
	return nil
}

// pageHandler is an admin route. current is nil when the request carried no
// valid session cookie.
type pageHandler func(responseWriter http.ResponseWriter, request *http.Request, current *session)

// wrap applies, to every admin route: security headers, session loading, CSRF
// verification for POST, and (when requireAuth) the sign-in redirect.
func (handler *Handler) wrap(requireAuth bool, next pageHandler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		setSecurityHeaders(responseWriter)
		current := handler.currentSession(request)

		if request.Method == http.MethodPost {
			request.Body = http.MaxBytesReader(responseWriter, request.Body, maxFormBodyBytes)
			if err := request.ParseForm(); err != nil {
				handler.renderError(responseWriter, current, http.StatusBadRequest, "Bad request",
					"The form could not be read: it was malformed or larger than 16 KB.", "/admin", "Back to the dashboard")
				return
			}
			if !validCSRFToken(current, request.PostForm.Get("csrf_token")) {
				handler.logger.Warn("admin request rejected: csrf token missing or wrong", "path", request.URL.Path)
				handler.renderError(responseWriter, current, http.StatusForbidden, "Form expired",
					"The security token on that form was missing, wrong or expired. Reload the page and try again.",
					"/admin/login", "Go to the sign-in page")
				return
			}
		}

		if requireAuth && (current == nil || !current.Authenticated) {
			http.Redirect(responseWriter, request, "/admin/login", http.StatusSeeOther)
			return
		}
		next(responseWriter, request, current)
	})
}

// currentSession returns the verified session from the request cookie, or nil.
func (handler *Handler) currentSession(request *http.Request) *session {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	decoded, err := handler.codec.decode(cookie.Value, handler.now())
	if err != nil {
		return nil
	}
	return &decoded
}

// startSession signs the session and sets it as the admin cookie.
func (handler *Handler) startSession(responseWriter http.ResponseWriter, current session) error {
	value, err := handler.codec.encode(current)
	if err != nil {
		return err
	}
	http.SetCookie(responseWriter, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     adminCookiePath,
		Expires:  time.Unix(current.ExpiresAt, 0).UTC(),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (handler *Handler) endSession(responseWriter http.ResponseWriter) {
	http.SetCookie(responseWriter, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     adminCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func validCSRFToken(current *session, submitted string) bool {
	if current == nil || submitted == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(current.CSRFToken), []byte(submitted)) == 1
}

func setSecurityHeaders(responseWriter http.ResponseWriter) {
	headers := responseWriter.Header()
	headers.Set("Content-Security-Policy", contentSecurityPolicy)
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Frame-Options", "DENY")
	headers.Set("X-Robots-Tag", "noindex")
	headers.Set("Cache-Control", "no-store")
}
