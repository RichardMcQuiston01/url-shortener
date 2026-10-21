// Package httpapi serves the JSON API under /api.
package httpapi

// ErrorCode is a stable, machine-readable API error code.
type ErrorCode string

const (
	CodeUnauthorized   ErrorCode = "unauthorized"
	CodeInvalidRequest ErrorCode = "invalid_request"
	CodeInvalidURL     ErrorCode = "invalid_url"
	CodeInvalidSlug    ErrorCode = "invalid_slug"
	CodeSlugReserved   ErrorCode = "slug_reserved"
	CodeSlugTaken      ErrorCode = "slug_taken"
	CodeNotFound       ErrorCode = "not_found"
	CodeRateLimited    ErrorCode = "rate_limited"
	CodeInternalError  ErrorCode = "internal_error"
)

// ErrorBody is the JSON body returned for every API failure.
type ErrorBody struct {
	Error   ErrorCode `json:"error"`
	Message string    `json:"message"`
}
