package store

import (
	"regexp"
	"testing"
)

var uuidV4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUIDFormatAndUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for iteration := 0; iteration < 1000; iteration++ {
		generated, err := NewUUID()
		if err != nil {
			t.Fatalf("NewUUID returned error: %v", err)
		}
		if !uuidV4Pattern.MatchString(generated) {
			t.Fatalf("%q is not a canonical v4 UUID", generated)
		}
		if seen[generated] {
			t.Fatalf("duplicate UUID %q", generated)
		}
		seen[generated] = true
	}
}
