package links

import (
	"errors"
	"testing"
)

func TestValidationErrorMatchesKindAndKeepsMessage(t *testing.T) {
	var err error = &ValidationError{Kind: ErrSlugReserved, Message: `slug "admin" is reserved`}
	if !errors.Is(err, ErrSlugReserved) {
		t.Error("errors.Is should match the wrapped kind")
	}
	if errors.Is(err, ErrInvalidSlug) {
		t.Error("errors.Is must not match a different kind")
	}
	if err.Error() != `slug "admin" is reserved` {
		t.Errorf("unexpected message %q", err.Error())
	}
}

func TestGeneratedSlugAlphabetExcludesLookAlikes(t *testing.T) {
	for _, forbidden := range "0O1lI" {
		for _, allowed := range GeneratedSlugAlphabet {
			if forbidden == allowed {
				t.Errorf("alphabet contains look-alike %q", forbidden)
			}
		}
	}
}
