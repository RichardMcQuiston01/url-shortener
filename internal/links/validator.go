package links

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// reservedSlugs are paths the server itself uses, so they can never be links.
var reservedSlugs map[string]bool = map[string]bool{
	"admin":       true,
	"api":         true,
	"healthz":     true,
	"robots.txt":  true,
	"favicon.ico": true,
	"static":      true,
}

// RuleValidator implements Validator using the rules in CLAUDE.md.
type RuleValidator struct {
	baseHost string
}

var _ Validator = (*RuleValidator)(nil)

// NewValidator builds a validator that rejects targets pointing at the host of
// baseURL (the public BASE_URL), which would create redirect loops.
func NewValidator(baseURL string) (*RuleValidator, error) {
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", errors.Unwrap(err))
	}
	if parsedBaseURL.Hostname() == "" {
		return nil, errors.New("base url must include a host")
	}
	return &RuleValidator{baseHost: strings.ToLower(parsedBaseURL.Hostname())}, nil
}

// ValidateSlug checks a custom slug: reserved words first, then length and
// the allowed character set [A-Za-z0-9_-].
func (validator *RuleValidator) ValidateSlug(slug string) error {
	if slug == "" {
		return &ValidationError{Kind: ErrInvalidSlug, Message: "slug must not be empty"}
	}
	if reservedSlugs[slug] {
		return &ValidationError{Kind: ErrSlugReserved, Message: fmt.Sprintf("slug %q is reserved", slug)}
	}
	if len(slug) > MaxSlugLength {
		return &ValidationError{
			Kind:    ErrInvalidSlug,
			Message: fmt.Sprintf("slug is too long (%d characters, maximum is %d)", len(slug), MaxSlugLength),
		}
	}
	for _, character := range slug {
		if !isSlugCharacter(character) {
			return &ValidationError{
				Kind: ErrInvalidSlug,
				Message: fmt.Sprintf("slug %q contains invalid character %q; only A-Z, a-z, 0-9, underscore and hyphen are allowed",
					slug, character),
			}
		}
	}
	return nil
}

// ValidateTargetURL checks that rawURL is an absolute http(s) URL with a host,
// no embedded credentials, within the length limit, and not on the BASE_URL host.
func (validator *RuleValidator) ValidateTargetURL(rawURL string) error {
	if rawURL == "" {
		return &ValidationError{Kind: ErrInvalidURL, Message: "url must not be empty"}
	}
	if len(rawURL) > MaxTargetURLLength {
		return &ValidationError{
			Kind:    ErrInvalidURL,
			Message: fmt.Sprintf("url is too long (%d characters, maximum is %d)", len(rawURL), MaxTargetURLLength),
		}
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return &ValidationError{
			Kind:    ErrInvalidURL,
			Message: fmt.Sprintf("url could not be parsed: %v", errors.Unwrap(err)),
		}
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return &ValidationError{
			Kind:    ErrInvalidURL,
			Message: fmt.Sprintf("url scheme %q is not allowed; use http or https", parsedURL.Scheme),
		}
	}
	if parsedURL.Hostname() == "" {
		return &ValidationError{Kind: ErrInvalidURL, Message: "url must include a host"}
	}
	if parsedURL.User != nil {
		return &ValidationError{Kind: ErrInvalidURL, Message: "url must not contain a username or password"}
	}
	if strings.ToLower(parsedURL.Hostname()) == validator.baseHost {
		return &ValidationError{
			Kind:    ErrInvalidURL,
			Message: fmt.Sprintf("url host %q is this shortener's own host; that would create a redirect loop", parsedURL.Hostname()),
		}
	}
	return nil
}

func isSlugCharacter(character rune) bool {
	return (character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z') ||
		(character >= '0' && character <= '9') ||
		character == '_' || character == '-'
}
