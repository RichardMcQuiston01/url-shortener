package admin

import (
	"errors"
	"net/http"
	"slices"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

type linkDetailContent struct {
	Detail store.LinkDetail
	// DailyNewestFirst is the per-day table, today first.
	DailyNewestFirst []store.DailyClicks
}

func (handler *Handler) showLink(responseWriter http.ResponseWriter, request *http.Request, current *session) {
	detail, err := handler.stats.GetLinkDetail(request.Context(), request.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		handler.renderError(responseWriter, current, http.StatusNotFound, "Link not found",
			"No link with that id exists. It may have been mistyped.", "/admin", "Back to the dashboard")
		return
	}
	if err != nil {
		handler.fail(responseWriter, "get link detail", err)
		return
	}

	newestFirst := slices.Clone(detail.DailyLast30Days)
	slices.Reverse(newestFirst)
	handler.render(responseWriter, http.StatusOK, "link", current, "Stats for /"+detail.Slug,
		linkDetailContent{Detail: detail, DailyNewestFirst: newestFirst})
}
