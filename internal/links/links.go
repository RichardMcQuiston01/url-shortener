// Package links validates slugs and target URLs and generates slugs.
package links

import "errors"

// Limits and alphabet from CLAUDE.md.
const (
	MaxSlugLength       int = 64
	MaxTargetURLLength  int = 2048
	GeneratedSlugLength int = 6
	// GeneratedSlugAlphabet excludes look-alike characters (0 O 1 l I).
	GeneratedSlugAlphabet string = "23456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
)

// Sentinel errors identifying the kind of validation failure. They are never
// returned bare; implementations return a *ValidationError wrapping one.
var (
	ErrInvalidSlug  error = errors.New("invalid slug")
	ErrSlugReserved error = errors.New("slug is reserved")
	ErrInvalidURL   error = errors.New("invalid url")
)

// ValidationError carries a specific, human-readable message (for example
// `slug "admin" is reserved`) and wraps a sentinel for errors.Is matching.
type ValidationError struct {
	Kind    error
	Message string
}

func (validationError *ValidationError) Error() string {
	return validationError.Message
}

func (validationError *ValidationError) Unwrap() error {
	return validationError.Kind
}

// Validator checks user-supplied slugs and target URLs.
type Validator interface {
	// ValidateSlug returns a *ValidationError wrapping ErrInvalidSlug or
	// ErrSlugReserved, or nil.
	ValidateSlug(slug string) error
	// ValidateTargetURL returns a *ValidationError wrapping ErrInvalidURL, or
	// nil. It also rejects targets whose host equals the BASE_URL host.
	ValidateTargetURL(rawURL string) error
}

// SlugGenerator produces random slugs.
type SlugGenerator interface {
	// Generate returns a slug of GeneratedSlugLength characters. Collision
	// handling lives with the caller, which retries a bounded number of times.
	Generate() (string, error)
}
