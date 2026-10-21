package links

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const (
	// MaxNoteLength is the longest allowed note, in characters.
	MaxNoteLength int = 500

	maxSlugAttempts int = 10
)

var (
	// ErrInvalidNote marks a note that breaks the note rules.
	ErrInvalidNote error = errors.New("invalid note")
	// ErrNoFreeSlug means every generated slug collided or was reserved.
	ErrNoFreeSlug error = errors.New("no free slug found")
)

// CreateRequest is the user-supplied data for a new link. Slug and Note are
// optional; empty means omitted.
type CreateRequest struct {
	URL  string
	Slug string
	Note string
}

// Service creates links. It is shared by the JSON API and the admin UI so both
// apply exactly the same rules.
type Service struct {
	linkStore     store.LinkStore
	validator     Validator
	slugGenerator SlugGenerator
}

// NewService builds a Service from its collaborators.
func NewService(linkStore store.LinkStore, validator Validator, slugGenerator SlugGenerator) *Service {
	return &Service{linkStore: linkStore, validator: validator, slugGenerator: slugGenerator}
}

// Create validates the request and stores a new link.
//
// With a custom slug it always creates (created is true) or fails with a
// *ValidationError wrapping store.ErrSlugTaken. Without a slug it returns the
// existing link for the same URL (created is false) or creates one with a
// generated slug, retrying a bounded number of times on collisions.
//
// Rule violations are *ValidationError values whose message is safe to show
// to the user. Any other error is an internal failure whose detail must be
// logged, not shown.
func (service *Service) Create(ctx context.Context, request CreateRequest) (link store.Link, created bool, err error) {
	targetURL := strings.TrimSpace(request.URL)
	if err := service.validator.ValidateTargetURL(targetURL); err != nil {
		return store.Link{}, false, err
	}

	note, err := normalizeNote(request.Note)
	if err != nil {
		return store.Link{}, false, err
	}

	slug := strings.TrimSpace(request.Slug)
	if slug != "" {
		link, err := service.createWithCustomSlug(ctx, slug, targetURL, note)
		return link, err == nil, err
	}
	return service.createWithGeneratedSlug(ctx, targetURL, note)
}

func normalizeNote(rawNote string) (*string, error) {
	note := strings.TrimSpace(rawNote)
	if note == "" {
		return nil, nil
	}
	if length := utf8.RuneCountInString(note); length > MaxNoteLength {
		return nil, &ValidationError{
			Kind:    ErrInvalidNote,
			Message: fmt.Sprintf("note is too long (%d characters, maximum is %d)", length, MaxNoteLength),
		}
	}
	return &note, nil
}

func (service *Service) createWithCustomSlug(ctx context.Context, slug string, targetURL string, note *string) (store.Link, error) {
	if err := service.validator.ValidateSlug(slug); err != nil {
		return store.Link{}, err
	}
	link, err := service.linkStore.Create(ctx, store.NewLink{Slug: slug, TargetURL: targetURL, Note: note})
	if errors.Is(err, store.ErrSlugTaken) {
		return store.Link{}, &ValidationError{
			Kind:    store.ErrSlugTaken,
			Message: fmt.Sprintf("slug %q is already used by another link", slug),
		}
	}
	if err != nil {
		return store.Link{}, fmt.Errorf("create link with custom slug %q: %w", slug, err)
	}
	return link, nil
}

func (service *Service) createWithGeneratedSlug(ctx context.Context, targetURL string, note *string) (store.Link, bool, error) {
	existing, err := service.linkStore.GetByTargetURL(ctx, targetURL)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Link{}, false, fmt.Errorf("look up existing link by target url: %w", err)
	}

	for attempt := 1; attempt <= maxSlugAttempts; attempt++ {
		slug, err := service.slugGenerator.Generate()
		if err != nil {
			return store.Link{}, false, fmt.Errorf("generate slug: %w", err)
		}
		if service.validator.ValidateSlug(slug) != nil {
			continue // a generated slug that is reserved: draw again
		}

		link, err := service.linkStore.Create(ctx, store.NewLink{Slug: slug, TargetURL: targetURL, Note: note})
		if errors.Is(err, store.ErrSlugTaken) {
			continue
		}
		if err != nil {
			return store.Link{}, false, fmt.Errorf("create link with generated slug: %w", err)
		}
		return link, true, nil
	}
	return store.Link{}, false, fmt.Errorf("%w after %d attempts", ErrNoFreeSlug, maxSlugAttempts)
}
