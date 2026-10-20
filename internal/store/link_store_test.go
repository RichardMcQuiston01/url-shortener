package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func stringPointer(value string) *string { return &value }

func TestCreateAndGetLink(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()

	created, err := sqlite.Create(ctx, NewLink{Slug: "hello", TargetURL: "https://example.com/a", Note: stringPointer("Chrome ext")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !uuidV4Pattern.MatchString(created.ID) {
		t.Errorf("id %q is not a v4 UUID", created.ID)
	}
	if created.CreatedAt.Location() != time.UTC || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Errorf("timestamps should be UTC and equal on create: %v %v", created.CreatedAt, created.UpdatedAt)
	}

	lookups := map[string]func() (Link, error){
		"slug": func() (Link, error) { return sqlite.GetBySlug(ctx, "hello") },
		"id":   func() (Link, error) { return sqlite.GetByID(ctx, created.ID) },
		"url":  func() (Link, error) { return sqlite.GetByTargetURL(ctx, "https://example.com/a") },
	}
	for name, lookup := range lookups {
		found, err := lookup()
		if err != nil {
			t.Fatalf("lookup by %s: %v", name, err)
		}
		if found.ID != created.ID || found.Slug != "hello" || found.TargetURL != "https://example.com/a" {
			t.Errorf("lookup by %s returned %+v", name, found)
		}
		if found.Note == nil || *found.Note != "Chrome ext" {
			t.Errorf("lookup by %s lost the note: %v", name, found.Note)
		}
		if !found.CreatedAt.Equal(created.CreatedAt) {
			t.Errorf("lookup by %s created_at = %v, want %v", name, found.CreatedAt, created.CreatedAt)
		}
	}
}

func TestCreateWithoutNoteReadsBackNil(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	if _, err := sqlite.Create(ctx, NewLink{Slug: "plain", TargetURL: "https://example.com"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	found, err := sqlite.GetBySlug(ctx, "plain")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if found.Note != nil {
		t.Errorf("note = %q, want nil", *found.Note)
	}
}

func TestCreateDuplicateSlugReturnsErrSlugTaken(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	if _, err := sqlite.Create(ctx, NewLink{Slug: "dup", TargetURL: "https://example.com/1"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := sqlite.Create(ctx, NewLink{Slug: "dup", TargetURL: "https://example.com/2"})
	if !errors.Is(err, ErrSlugTaken) {
		t.Errorf("error = %v, want ErrSlugTaken", err)
	}
}

func TestSlugsAreCaseSensitive(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	for _, slug := range []string{"Hello", "hello"} {
		if _, err := sqlite.Create(ctx, NewLink{Slug: slug, TargetURL: "https://example.com/" + slug}); err != nil {
			t.Fatalf("Create %q: %v", slug, err)
		}
	}
	found, err := sqlite.GetBySlug(ctx, "Hello")
	if err != nil || found.TargetURL != "https://example.com/Hello" {
		t.Errorf("GetBySlug(Hello) = %+v, %v", found, err)
	}
}

func TestLookupsReturnErrNotFound(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	if _, err := sqlite.GetBySlug(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetBySlug error = %v, want ErrNotFound", err)
	}
	if _, err := sqlite.GetByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID error = %v, want ErrNotFound", err)
	}
	if _, err := sqlite.GetByTargetURL(ctx, "https://nowhere.example"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByTargetURL error = %v, want ErrNotFound", err)
	}
}

func TestGetByTargetURLReturnsOldestLink(t *testing.T) {
	sqlite, _ := openTestStore(t)
	ctx := context.Background()
	older, err := sqlite.Create(ctx, NewLink{Slug: "first", TargetURL: "https://example.com/same"})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := sqlite.db.ExecContext(ctx, "UPDATE link SET created_at = '2000-01-01T00:00:00Z' WHERE id = ?", older.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := sqlite.Create(ctx, NewLink{Slug: "second", TargetURL: "https://example.com/same"}); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	found, err := sqlite.GetByTargetURL(ctx, "https://example.com/same")
	if err != nil || found.Slug != "first" {
		t.Errorf("GetByTargetURL = %+v, %v; want the oldest link (first)", found, err)
	}
}
