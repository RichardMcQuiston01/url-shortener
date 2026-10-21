package links

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

type sequenceGenerator struct {
	slugs []string
	calls int
}

func (generator *sequenceGenerator) Generate() (string, error) {
	index := generator.calls
	if index >= len(generator.slugs) {
		index = len(generator.slugs) - 1
	}
	generator.calls++
	return generator.slugs[index], nil
}

func newTestService(t *testing.T, generator SlugGenerator) *Service {
	t.Helper()
	sqlite, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return NewService(sqlite, newTestValidator(t), generator)
}

func TestServiceCreateWithCustomSlug(t *testing.T) {
	service := newTestService(t, &sequenceGenerator{slugs: []string{"unused"}})
	link, created, err := service.Create(context.Background(),
		CreateRequest{URL: "  https://example.com/a  ", Slug: " hello ", Note: "  my note "})
	if err != nil || !created {
		t.Fatalf("Create = %+v, created %v, err %v", link, created, err)
	}
	if link.Slug != "hello" || link.TargetURL != "https://example.com/a" || link.Note == nil || *link.Note != "my note" {
		t.Errorf("inputs should be trimmed: %+v", link)
	}
}

func TestServiceCreateRuleViolations(t *testing.T) {
	service := newTestService(t, &sequenceGenerator{slugs: []string{"unused"}})
	if _, _, err := service.Create(context.Background(), CreateRequest{URL: "https://example.com/taken", Slug: "hello"}); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	testCases := []struct {
		name            string
		request         CreateRequest
		expectedKind    error
		expectedMessage string
	}{
		{"bad url", CreateRequest{URL: "ftp://example.com"}, ErrInvalidURL, `scheme "ftp"`},
		{"empty url", CreateRequest{URL: " "}, ErrInvalidURL, "must not be empty"},
		{"reserved slug", CreateRequest{URL: "https://example.com/b", Slug: "api"}, ErrSlugReserved, `slug "api" is reserved`},
		{"invalid slug", CreateRequest{URL: "https://example.com/b", Slug: "a b"}, ErrInvalidSlug, "invalid character"},
		{"slug taken", CreateRequest{URL: "https://example.com/b", Slug: "hello"}, store.ErrSlugTaken, `slug "hello" is already used by another link`},
		{"note too long", CreateRequest{URL: "https://example.com/b", Note: strings.Repeat("n", 501)}, ErrInvalidNote, "note is too long (501 characters, maximum is 500)"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, created, err := service.Create(context.Background(), testCase.request)
			assertValidation(t, err, testCase.expectedKind, testCase.expectedMessage)
			if created {
				t.Error("created must be false on error")
			}
		})
	}
}

func TestServiceNoteBoundary(t *testing.T) {
	service := newTestService(t, &sequenceGenerator{slugs: []string{"abc234"}})
	_, created, err := service.Create(context.Background(), CreateRequest{URL: "https://example.com/n", Note: strings.Repeat("é", 500)})
	if err != nil || !created {
		t.Errorf("a 500-character note (counted in characters, not bytes) must be accepted: %v", err)
	}
}

func TestServiceGeneratedSlugReturnsExistingLinkForSameURL(t *testing.T) {
	service := newTestService(t, RandomSlugGenerator{})
	first, created, err := service.Create(context.Background(), CreateRequest{URL: "https://example.com/same"})
	if err != nil || !created {
		t.Fatalf("first Create: %v, created %v", err, created)
	}
	second, created, err := service.Create(context.Background(), CreateRequest{URL: "https://example.com/same"})
	if err != nil || created || second.ID != first.ID {
		t.Errorf("second Create = %+v, created %v, err %v; want the existing link", second, created, err)
	}

	custom, created, err := service.Create(context.Background(), CreateRequest{URL: "https://example.com/same", Slug: "second"})
	if err != nil || !created || custom.ID == first.ID {
		t.Errorf("a custom slug must always create a new link: %+v, created %v, err %v", custom, created, err)
	}
}

func TestServiceRetriesCollisionsAndSkipsReservedSlugs(t *testing.T) {
	generator := &sequenceGenerator{slugs: []string{"taken1", "static", "fresh2"}}
	service := newTestService(t, generator)
	if _, _, err := service.Create(context.Background(), CreateRequest{URL: "https://example.com/existing", Slug: "taken1"}); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	link, created, err := service.Create(context.Background(), CreateRequest{URL: "https://example.com/new"})
	if err != nil || !created || link.Slug != "fresh2" {
		t.Fatalf("Create = %+v, created %v, err %v; want slug fresh2", link, created, err)
	}
	if generator.calls != 3 {
		t.Errorf("generator called %d times, want 3", generator.calls)
	}
}

func TestServiceGivesUpAfterBoundedAttempts(t *testing.T) {
	generator := &sequenceGenerator{slugs: []string{"taken1"}}
	service := newTestService(t, generator)
	if _, _, err := service.Create(context.Background(), CreateRequest{URL: "https://example.com/existing", Slug: "taken1"}); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	_, _, err := service.Create(context.Background(), CreateRequest{URL: "https://example.com/new"})
	if !errors.Is(err, ErrNoFreeSlug) {
		t.Fatalf("error = %v, want ErrNoFreeSlug", err)
	}
	var validationError *ValidationError
	if errors.As(err, &validationError) {
		t.Error("exhaustion is an internal failure, not a user-facing validation error")
	}
	if generator.calls != maxSlugAttempts {
		t.Errorf("generator called %d times, want exactly %d", generator.calls, maxSlugAttempts)
	}
}
