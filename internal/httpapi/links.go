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
	"unicode/utf8"

	"github.com/richardmcquiston01/url-shortener/internal/links"
	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const (
	maxRequestBodyBytes int64 = 16 * 1024
	maxNoteLength       int   = 500
	maxSlugAttempts     int   = 10
	defaultListLimit    int   = 50
	maxListLimit        int   = 200

	internalErrorMessage string = "an internal error occurred; the details were written to the server log"
)

// Handler serves the JSON API under /api, protected by a bearer API key.
type Handler struct {
	linkStore     store.LinkStore
	validator     links.Validator
	slugGenerator links.SlugGenerator
	baseURL       string
	logger        *slog.Logger
}

// NewHandler builds the API handler. baseURL is the public BASE_URL (no
// trailing slash) used to build each link's short_url.
func NewHandler(
	linkStore store.LinkStore,
	validator links.Validator,
	slugGenerator links.SlugGenerator,
	baseURL string,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		linkStore:     linkStore,
		validator:     validator,
		slugGenerator: slugGenerator,
		baseURL:       baseURL,
		logger:        logger,
	}
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

type listLinksResponse struct {
	Links  []linkResponse `json:"links"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
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

	targetURL := strings.TrimSpace(createRequest.URL)
	if err := handler.validator.ValidateTargetURL(targetURL); err != nil {
		handler.writeValidationError(responseWriter, err)
		return
	}

	var note *string
	if trimmedNote := strings.TrimSpace(createRequest.Note); trimmedNote != "" {
		if utf8.RuneCountInString(trimmedNote) > maxNoteLength {
			writeError(responseWriter, handler.logger, http.StatusBadRequest, CodeInvalidRequest,
				fmt.Sprintf("note is too long (%d characters, maximum is %d)", utf8.RuneCountInString(trimmedNote), maxNoteLength))
			return
		}
		note = &trimmedNote
	}

	slug := strings.TrimSpace(createRequest.Slug)
	if slug != "" {
		handler.createWithCustomSlug(responseWriter, request, slug, targetURL, note)
		return
	}
	handler.createWithGeneratedSlug(responseWriter, request, targetURL, note)
}

func (handler *Handler) createWithCustomSlug(responseWriter http.ResponseWriter, request *http.Request, slug string, targetURL string, note *string) {
	if err := handler.validator.ValidateSlug(slug); err != nil {
		handler.writeValidationError(responseWriter, err)
		return
	}

	link, err := handler.linkStore.Create(request.Context(), store.NewLink{Slug: slug, TargetURL: targetURL, Note: note})
	if errors.Is(err, store.ErrSlugTaken) {
		writeError(responseWriter, handler.logger, http.StatusConflict, CodeSlugTaken,
			fmt.Sprintf("slug %q is already used by another link", slug))
		return
	}
	if err != nil {
		handler.writeInternalError(responseWriter, "create link with custom slug", err)
		return
	}
	writeJSON(responseWriter, handler.logger, http.StatusCreated, handler.toLinkResponse(link))
}

// createWithGeneratedSlug returns the existing link (200) if the URL is already
// shortened, otherwise creates a new one (201) with a generated slug, retrying
// a bounded number of times on collisions.
func (handler *Handler) createWithGeneratedSlug(responseWriter http.ResponseWriter, request *http.Request, targetURL string, note *string) {
	existing, err := handler.linkStore.GetByTargetURL(request.Context(), targetURL)
	if err == nil {
		writeJSON(responseWriter, handler.logger, http.StatusOK, handler.toLinkResponse(existing))
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		handler.writeInternalError(responseWriter, "look up existing link by target url", err)
		return
	}

	for attempt := 1; attempt <= maxSlugAttempts; attempt++ {
		slug, err := handler.slugGenerator.Generate()
		if err != nil {
			handler.writeInternalError(responseWriter, "generate slug", err)
			return
		}
		if handler.validator.ValidateSlug(slug) != nil {
			continue // a generated slug that is reserved: draw again
		}

		link, err := handler.linkStore.Create(request.Context(), store.NewLink{Slug: slug, TargetURL: targetURL, Note: note})
		if errors.Is(err, store.ErrSlugTaken) {
			continue
		}
		if err != nil {
			handler.writeInternalError(responseWriter, "create link with generated slug", err)
			return
		}
		writeJSON(responseWriter, handler.logger, http.StatusCreated, handler.toLinkResponse(link))
		return
	}

	handler.writeInternalError(responseWriter, "generate a free slug",
		fmt.Errorf("no free slug found after %d attempts", maxSlugAttempts))
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

	storedLinks, err := handler.linkStore.List(request.Context(), limit, offset)
	if err != nil {
		handler.writeInternalError(responseWriter, "list links", err)
		return
	}

	response := listLinksResponse{Links: make([]linkResponse, 0, len(storedLinks)), Limit: limit, Offset: offset}
	for _, link := range storedLinks {
		response.Links = append(response.Links, handler.toLinkResponse(link))
	}
	writeJSON(responseWriter, handler.logger, http.StatusOK, response)
}

func (handler *Handler) getLink(responseWriter http.ResponseWriter, request *http.Request) {
	slug := request.PathValue("slug")

	link, err := handler.linkStore.GetBySlug(request.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		writeError(responseWriter, handler.logger, http.StatusNotFound, CodeNotFound,
			fmt.Sprintf("no link with slug %q exists", slug))
		return
	}
	if err != nil {
		handler.writeInternalError(responseWriter, "get link by slug", err)
		return
	}
	writeJSON(responseWriter, handler.logger, http.StatusOK, handler.toLinkResponse(link))
}

func (handler *Handler) unknownEndpoint(responseWriter http.ResponseWriter, request *http.Request) {
	writeError(responseWriter, handler.logger, http.StatusNotFound, CodeNotFound,
		fmt.Sprintf("no API endpoint for %s %s", request.Method, request.URL.Path))
}

// writeValidationError maps a links validation error to its API code, using
// the validator's specific message.
func (handler *Handler) writeValidationError(responseWriter http.ResponseWriter, err error) {
	code := CodeInvalidRequest
	switch {
	case errors.Is(err, links.ErrInvalidURL):
		code = CodeInvalidURL
	case errors.Is(err, links.ErrSlugReserved):
		code = CodeSlugReserved
	case errors.Is(err, links.ErrInvalidSlug):
		code = CodeInvalidSlug
	}
	writeError(responseWriter, handler.logger, http.StatusBadRequest, code, err.Error())
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
