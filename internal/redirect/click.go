package redirect

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

// botMarkers are lower-case user agent fragments that mark automated clients.
var botMarkers []string = []string{
	"bot", "crawler", "spider", "slurp", "curl/", "wget", "python-requests",
	"go-http-client", "headless", "facebookexternalhit", "preview", "monitor", "uptime",
}

// isBot reports whether the user agent looks automated. A missing user agent
// counts as a bot. Only the verdict is stored, never the user agent itself.
func isBot(userAgent string) bool {
	normalized := strings.ToLower(strings.TrimSpace(userAgent))
	if normalized == "" {
		return true
	}
	for _, marker := range botMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// referrerHost returns the lower-case host (no port, path or query) of an
// http(s) Referer header, or nil when it is absent or unusable.
func referrerHost(referer string) *string {
	if referer == "" {
		return nil
	}
	parsedReferer, err := url.Parse(referer)
	if err != nil || (parsedReferer.Scheme != "http" && parsedReferer.Scheme != "https") {
		return nil
	}
	host := strings.ToLower(parsedReferer.Hostname())
	if host == "" {
		return nil
	}
	return &host
}

// newClick describes one redirect for statistics.
func newClick(link store.Link, request *http.Request, clickedAt time.Time) store.Click {
	return store.Click{
		LinkID:       link.ID,
		ClickedAt:    clickedAt.UTC(),
		ReferrerHost: referrerHost(request.Header.Get("Referer")),
		IsBot:        isBot(request.UserAgent()),
	}
}
