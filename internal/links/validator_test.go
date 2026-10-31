package links

import (
	"errors"
	"strings"
	"testing"
)

func newTestValidator(t *testing.T) *RuleValidator {
	t.Helper()
	validator, err := NewValidator("https://go.example.com")
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return validator
}

func TestValidateSlug(t *testing.T) {
	validator := newTestValidator(t)
	testCases := []struct {
		name            string
		slug            string
		expectedKind    error
		expectedMessage string
	}{
		{"simple", "hello", nil, ""},
		{"mixed case", "Hello-World_9", nil, ""},
		{"single character", "a", nil, ""},
		{"maximum length", strings.Repeat("a", 64), nil, ""},
		{"case sensitive reserved word is allowed", "Admin", nil, ""},
		{"empty", "", ErrInvalidSlug, "must not be empty"},
		{"too long", strings.Repeat("a", 65), ErrInvalidSlug, "too long (65 characters, maximum is 64)"},
		{"space", "has space", ErrInvalidSlug, `invalid character ' '`},
		{"slash", "a/b", ErrInvalidSlug, `invalid character '/'`},
		{"dot", "a.b", ErrInvalidSlug, `invalid character '.'`},
		{"unicode", "café", ErrInvalidSlug, "invalid character"},
		{"reserved admin", "admin", ErrSlugReserved, `slug "admin" is reserved`},
		{"reserved api", "api", ErrSlugReserved, `slug "api" is reserved`},
		{"reserved healthz", "healthz", ErrSlugReserved, `slug "healthz" is reserved`},
		{"reserved robots", "robots.txt", ErrSlugReserved, `slug "robots.txt" is reserved`},
		{"reserved favicon", "favicon.ico", ErrSlugReserved, `slug "favicon.ico" is reserved`},
		{"reserved static", "static", ErrSlugReserved, `slug "static" is reserved`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assertValidation(t, validator.ValidateSlug(testCase.slug), testCase.expectedKind, testCase.expectedMessage)
		})
	}
}

func TestValidateTargetURL(t *testing.T) {
	validator := newTestValidator(t)
	testCases := []struct {
		name            string
		rawURL          string
		expectedKind    error
		expectedMessage string
	}{
		{"https", "https://example.com/some/long/path?q=1#frag", nil, ""},
		{"http with port", "http://example.com:8080/x", nil, ""},
		{"maximum length", "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/")), nil, ""},
		{"empty", "", ErrInvalidURL, "must not be empty"},
		{"too long", "https://example.com/" + strings.Repeat("a", 2048), ErrInvalidURL, "too long"},
		{"ftp scheme", "ftp://example.com/file", ErrInvalidURL, `scheme "ftp" is not allowed`},
		{"javascript scheme", "javascript:alert(1)", ErrInvalidURL, `scheme "javascript" is not allowed`},
		{"no scheme", "example.com/path", ErrInvalidURL, `scheme "" is not allowed`},
		{"no host", "https:///path", ErrInvalidURL, "must include a host"},
		{"userinfo", "https://user:pass@example.com/", ErrInvalidURL, "username or password"},
		{"username only", "https://user@example.com/", ErrInvalidURL, "username or password"},
		{"unparseable", "https://exa mple.com/", ErrInvalidURL, "could not be parsed"},
		{"own host", "https://go.example.com/other", ErrInvalidURL, "redirect loop"},
		{"own host different case", "https://GO.Example.com/other", ErrInvalidURL, "redirect loop"},
		{"own host with port", "https://go.example.com:8443/x", ErrInvalidURL, "redirect loop"},
		{"similar host is fine", "https://notgo.example.com/", nil, ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assertValidation(t, validator.ValidateTargetURL(testCase.rawURL), testCase.expectedKind, testCase.expectedMessage)
		})
	}
}

func TestValidationErrorsDoNotEchoCredentials(t *testing.T) {
	err := newTestValidator(t).ValidateTargetURL("https://user:hunter2@example.com/")
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error must exist and not echo the password, got %v", err)
	}
}

func TestNewValidatorRejectsBaseURLWithoutHost(t *testing.T) {
	if _, err := NewValidator("not-a-url"); err == nil {
		t.Error("expected an error for a base url without a host")
	}
}

func assertValidation(t *testing.T, err error, expectedKind error, expectedMessage string) {
	t.Helper()
	if expectedKind == nil {
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected %v, got no error", expectedKind)
	}
	if !errors.Is(err, expectedKind) {
		t.Errorf("error %q should wrap %v", err, expectedKind)
	}
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error %q is not a *ValidationError", err)
	}
	if !strings.Contains(err.Error(), expectedMessage) {
		t.Errorf("message %q should contain %q", err.Error(), expectedMessage)
	}
}
