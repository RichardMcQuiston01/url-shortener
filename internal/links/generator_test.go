package links

import (
	"strings"
	"testing"
)

func TestRandomSlugGeneratorProducesValidSlugs(t *testing.T) {
	generator := RandomSlugGenerator{}
	validator := newTestValidator(t)
	seen := make(map[string]bool)
	for iteration := 0; iteration < 2000; iteration++ {
		slug, err := generator.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if len(slug) != GeneratedSlugLength {
			t.Fatalf("slug %q has length %d, want %d", slug, len(slug), GeneratedSlugLength)
		}
		for _, character := range slug {
			if !strings.ContainsRune(GeneratedSlugAlphabet, character) {
				t.Fatalf("slug %q contains %q outside the alphabet", slug, character)
			}
		}
		if err := validator.ValidateSlug(slug); err != nil {
			t.Fatalf("generated slug %q fails validation: %v", slug, err)
		}
		seen[slug] = true
	}
	if len(seen) < 1990 {
		t.Errorf("only %d unique slugs in 2000 draws; randomness looks broken", len(seen))
	}
}
