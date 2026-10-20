package httpapi

import (
	"encoding/json"
	"testing"
)

func TestErrorCodesAreStable(t *testing.T) {
	testCases := []struct {
		code     ErrorCode
		expected string
	}{
		{CodeUnauthorized, "unauthorized"},
		{CodeInvalidURL, "invalid_url"},
		{CodeInvalidSlug, "invalid_slug"},
		{CodeSlugReserved, "slug_reserved"},
		{CodeSlugTaken, "slug_taken"},
		{CodeNotFound, "not_found"},
		{CodeRateLimited, "rate_limited"},
		{CodeInternalError, "internal_error"},
	}
	for _, testCase := range testCases {
		if string(testCase.code) != testCase.expected {
			t.Errorf("error code %q changed; clients depend on %q", testCase.code, testCase.expected)
		}
	}
}

func TestErrorBodyJSONShape(t *testing.T) {
	encoded, err := json.Marshal(ErrorBody{Error: CodeSlugTaken, Message: `slug "hello" is already used by another link`})
	if err != nil {
		t.Fatalf("marshal error body: %v", err)
	}
	expected := `{"error":"slug_taken","message":"slug \"hello\" is already used by another link"}`
	if string(encoded) != expected {
		t.Errorf("got %s, want %s", encoded, expected)
	}
}
