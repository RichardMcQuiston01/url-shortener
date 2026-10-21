package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/links"
	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const (
	maxRequestBodyBytes int64 = 16 * 1024
	defaultListLimit    int   = 50
	maxListLimit        int   = 200

	internalErrorMessage string = "an internal error occurred; the details were written to the server log"
)

// Handler serves the JSON API under /api, protected by a bearer API key.
type Handler struct {
	linkService *links.Service
	statsStore  store.StatsStore
	baseURL     string
	logger      *slog.Logger
}

// NewHandler builds the API handler. baseURL is the public BASE_URL (no
// trailing slash) used to build each link's short_url.
func NewHandler(
	linkService *links.Service,
	statsStore store.StatsStore,
	baseURL string,
	logger *slog.Logger,
) *Handler {
	return &Handler{linkService: linkService, statsStore: statsStore, baseURL: baseURL, logger: logger}
}

// Register adds the API routes to mux, each behind RequireAPIKey. Unknown
// paths under /api/ answer with a JSON not_found (after authentication).
func (handler *Handler) Register(mux *http.ServeMux, apiKey string) {
	protect := func(next http.HandlerFunc) http.Handler {
		return RequireAPIKey(apiKey, handler.logger, next)
	}
	mux.Handle("POST /api/links", protect(handler.createLink))
	mux.Handle("GET /api/links", protect(handler.listLinks))
	mux.Handle("GET /api/links/{slug}", protect(handler.getLink))
	mux.Handle("/api/", protect(handler.unknownEndpoint))
}

type createLinkRequest struct {
	URL  string `json:"url"`
	Slug string `json:"slug"`
	Note string `json:"note"`
}

type linkResponse struct {
	ID        string  `json:"id"`
	Slug      string  `json:"slug"`
	ShortURL  string  `json:"short_url"`
	TargetURL string  `json:"target_url"`
	Note      *string `json:"note"`
	CreatedAt string  `json:"created_at"`
}

// linkSummaryResponse is a link plus its click counts. Bot clicks are never
// counted.
type linkSummaryResponse struct {
	linkResponse
	TotalClicks  int64 `json:"total_clicks"`
	Clicks7Days  int64 `json:"clicks_7_days"`
	Clicks30Days int64 `json:"clicks_30_days"`
}

type listLinksResponse struct {
	Links  []linkSummaryResponse `json:"links"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

func (handler *Handler) toLinkSummaryResponse(summary store.LinkSummary) linkSummaryResponse {
	return linkSummaryResponse{
		linkResponse: handler.toLinkResponse(summary.Link),
		TotalClicks:  summary.TotalClicks,
		Clicks7Days:  summary.Clicks7Days,
		Clicks30Days: summary.Clicks30Days,
	}
}

func (handler *Handler) toLinkResponse(link store.Link) linkResponse {
	return linkResponse{
		ID:        link.ID,
		Slug:      link.Slug,
		ShortURL:  handler.baseURL + "/" + link.Slug,
		TargetURL: link.TargetURL,
		Note:      link.Note,
		CreatedAt: link.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (handler *Handler) createLink(responseWriter http.ResponseWriter, request *http.Request) {
	createRequest, problem := decodeCreateRequest(responseWriter, request)
	if problem != nil {
		writeError(responseWriter, handler.logger, problem.status, CodeInvalidRequest, problem.message)
		return
	}

	link, created, err := handler.linkService.Create(request.Context(), links.CreateRequest{
		URL:  createRequest.URL,
		Slug: createRequest.Slug,
		Note: createRequest.Note,
	})
	if err != nil {
		handler.writeCreateError(responseWriter, err)
		return
	}

	status := http.StatusOK // the URL was already shortened
	if created {
		status = http.StatusCreated
	}
	writeJSON(responseWriter, handler.logger, status, handler.toLinkResponse(link))
}

func (handler *Handler) listLinks(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()

	limit, err := parseBoundedInt(query.Get("limit"), "limit", defaultListLimit, 1, maxListLimit)
	if err != nil {
		writeError(responseWriter, handler.logger, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	offset, err := parseBoundedInt(query.Get("offset"), "offset", 0, 0, math.MaxInt32)
	if err != nil {
		writeError(responseWriter, handler.logger, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}

	summaries, err := handler.statsStore.ListLinkSummaries(request.Context(), limit, offset)
	if err != nil {
		handler.writeInternalError(responseWriter, "list links", err)
		return
	}

	response := listLinksResponse{Links: make([]linkSummaryResponse, 0, len(summaries)), Limit: limit, Offset: offset}
	for _, summary := range summaries {
		response.Links = append(response.Links, handler.toLinkSummaryResponse(summary))
	}
	writeJSON(responseWriter, handler.logger, http.StatusOK, response)
}

func (handler *Handler) getLink(responseWriter http.ResponseWriter, request *http.Request) {
	slug := request.PathValue("slug")

	summary, err := handler.statsStore.GetLinkSummaryBySlug(request.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		writeError(responseWriter, handler.logger, http.StatusNotFound, CodeNotFound,
			fmt.Sprintf("no link with slug %q exists", slug))
		return
	}
	if err != nil {
		handler.writeInternalError(responseWriter, "get link summary by slug", err)
		return
	}
	writeJSON(responseWriter, handler.logger, http.StatusOK, handler.toLinkSummaryResponse(summary))
}

func (handler *Handler) unknownEndpoint(responseWriter http.ResponseWriter, request *http.Request) {
	writeError(responseWriter, handler.logger, http.StatusNotFound, CodeNotFound,
		fmt.Sprintf("no API endpoint for %s %s", request.Method, request.URL.Path))
}

// writeCreateError maps a link service error to its status and API code. Rule
// violations use the service's specific message; anything else is internal.
func (handler *Handler) writeCreateError(responseWriter http.ResponseWriter, err error) {
	var validationError *links.ValidationError
	if !errors.As(err, &validationError) {
		handler.writeInternalError(responseWriter, "create link", err)
		return
	}

	status, code := http.StatusBadRequest, CodeInvalidRequest
	switch {
	case errors.Is(err, store.ErrSlugTaken):
		status, code = http.StatusConflict, CodeSlugTaken
	case errors.Is(err, links.ErrInvalidURL):
		code = CodeInvalidURL
	case errors.Is(err, links.ErrSlugReserved):
		code = CodeSlugReserved
	case errors.Is(err, links.ErrInvalidSlug):
		code = CodeInvalidSlug
	}
	writeError(responseWriter, handler.logger, status, code, err.Error())
}

// writeInternalError logs the detail and sends only a generic message.
func (handler *Handler) writeInternalError(responseWriter http.ResponseWriter, action string, err error) {
	handler.logger.Error("api request failed", "action", action, "error", err)
	writeError(responseWriter, handler.logger, http.StatusInternalServerError, CodeInternalError, internalErrorMessage)
}

type requestProblem struct {
	status  int
	message string
}

// decodeCreateRequest reads exactly one JSON object of at most 16 KB, rejecting
// unknown fields so typos such as "slg" are reported instead of ignored.
func decodeCreateRequest(responseWriter http.ResponseWriter, request *http.Request) (createLinkRequest, *requestProblem) {
	request.Body = http.MaxBytesReader(responseWriter, request.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var createRequest createLinkRequest
	if err := decoder.Decode(&createRequest); err != nil {
		return createLinkRequest{}, describeDecodeError(err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return createLinkRequest{}, &requestProblem{http.StatusBadRequest, "request body must contain a single JSON object"}
	}
	return createRequest, nil
}

func describeDecodeError(err error) *requestProblem {
	var tooLarge *http.MaxBytesError
	var typeError *json.UnmarshalTypeError
	var syntaxError *json.SyntaxError

	switch {
	case errors.As(err, &tooLarge):
		return &requestProblem{http.StatusRequestEntityTooLarge,
			fmt.Sprintf("request body is larger than the %d byte limit", maxRequestBodyBytes)}
	case errors.Is(err, io.EOF):
		return &requestProblem{http.StatusBadRequest, `request body is empty; send a JSON object such as {"url": "https://example.com"}`}
	case errors.As(err, &typeError):
		return &requestProblem{http.StatusBadRequest,
			fmt.Sprintf("field %q must be a %s, not a %s", typeError.Field, typeError.Type, typeError.Value)}
	case errors.As(err, &syntaxError):
		return &requestProblem{http.StatusBadRequest, fmt.Sprintf("request body is not valid JSON: %v", syntaxError)}
	case errors.Is(err, io.ErrUnexpectedEOF):
		return &requestProblem{http.StatusBadRequest, "request body is not valid JSON: it ended before the object was complete"}
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return &requestProblem{http.StatusBadRequest,
			fmt.Sprintf("%s; allowed fields are url, slug and note", strings.TrimPrefix(err.Error(), "json: "))}
	default:
		return &requestProblem{http.StatusBadRequest, fmt.Sprintf("request body could not be read: %v", err)}
	}
}

func parseBoundedInt(rawValue string, name string, defaultValue int, minValue int, maxValue int) (int, error) {
	if rawValue == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(rawValue)
	if err != nil {
		return 0, fmt.Errorf("query parameter %q must be a whole number, got %q", name, rawValue)
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("query parameter %q must be between %d and %d, got %d", name, minValue, maxValue, value)
	}
	return value, nil
}
