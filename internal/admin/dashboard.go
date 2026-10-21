package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/richardmcquiston01/url-shortener/internal/links"
	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const (
	dashboardPageSize int = 50
	maxDashboardPage  int = 100000

	flashCreated  string = "created"
	flashExisting string = "existing"
)

// formValues are the add-link form fields, kept so a rejected form can be
// shown again without retyping.
type formValues struct {
	URL  string
	Slug string
	Note string
}

// notice is the one-time success message shown after adding a link.
type notice struct {
	Message  string
	ShortURL string
}

type dashboardContent struct {
	Links        []store.LinkSummary
	Form         formValues
	FormError    string
	Notice       *notice
	Page         int
	HasPrevious  bool
	HasNext      bool
	PreviousPage int
	NextPage     int
}

func (handler *Handler) showDashboard(responseWriter http.ResponseWriter, request *http.Request, current *session) {
	handler.renderDashboard(responseWriter, request, current, http.StatusOK, formValues{}, "")
}

// renderDashboard draws the link table and the add form. A pending flash
// message is shown once and removed from the session.
func (handler *Handler) renderDashboard(responseWriter http.ResponseWriter, request *http.Request, current *session, status int, form formValues, formError string) {
	pageNumber := parsePageNumber(request.URL.Query().Get("page"))

	summaries, err := handler.stats.ListLinkSummaries(request.Context(), dashboardPageSize+1, (pageNumber-1)*dashboardPageSize)
	if err != nil {
		handler.fail(responseWriter, "list link summaries for the dashboard", err)
		return
	}
	hasNext := len(summaries) > dashboardPageSize
	if hasNext {
		summaries = summaries[:dashboardPageSize]
	}

	content := dashboardContent{
		Links:        summaries,
		Form:         form,
		FormError:    formError,
		Page:         pageNumber,
		HasPrevious:  pageNumber > 1,
		HasNext:      hasNext,
		PreviousPage: pageNumber - 1,
		NextPage:     pageNumber + 1,
	}

	if current.Flash != nil {
		content.Notice = handler.noticeFor(*current.Flash)
		withoutFlash := *current
		withoutFlash.Flash = nil
		if err := handler.startSession(responseWriter, withoutFlash); err != nil {
			handler.logger.Error("clear flash message", "error", err)
		}
	}
	handler.render(responseWriter, status, "dashboard", current, "Links", content)
}

func (handler *Handler) noticeFor(message flashMessage) *notice {
	shortURL := handler.baseURL + "/" + message.Slug
	switch message.Kind {
	case flashCreated:
		return &notice{Message: "Short link created:", ShortURL: shortURL}
	case flashExisting:
		return &notice{Message: "That URL already has a short link:", ShortURL: shortURL}
	default:
		return nil
	}
}

func (handler *Handler) createLink(responseWriter http.ResponseWriter, request *http.Request, current *session) {
	form := formValues{
		URL:  request.PostForm.Get("url"),
		Slug: request.PostForm.Get("slug"),
		Note: request.PostForm.Get("note"),
	}

	link, created, err := handler.linkService.Create(request.Context(),
		links.CreateRequest{URL: form.URL, Slug: form.Slug, Note: form.Note})
	if err != nil {
		var validationError *links.ValidationError
		if !errors.As(err, &validationError) {
			handler.fail(responseWriter, "create link", err)
			return
		}
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrSlugTaken) {
			status = http.StatusConflict
		}
		handler.renderDashboard(responseWriter, request, current, status, form, err.Error())
		return
	}

	kind := flashExisting
	if created {
		kind = flashCreated
	}
	withFlash := *current
	withFlash.Flash = &flashMessage{Kind: kind, Slug: link.Slug}
	if err := handler.startSession(responseWriter, withFlash); err != nil {
		handler.fail(responseWriter, "store flash message", err)
		return
	}
	http.Redirect(responseWriter, request, "/admin", http.StatusSeeOther)
}

// parsePageNumber turns ?page= into a page number, falling back to 1 for
// anything missing, malformed or absurd.
func parsePageNumber(raw string) int {
	pageNumber, err := strconv.Atoi(raw)
	if err != nil || pageNumber < 1 || pageNumber > maxDashboardPage {
		return 1
	}
	return pageNumber
}
