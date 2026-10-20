// Package config loads and validates settings from environment variables.
package config

// Config holds every setting described in CLAUDE.md. Load and validation are
// added in Stage 1; secrets must never appear in error messages or logs.
type Config struct {
	BaseURL         string
	AdminPassword   string
	APIKey          string
	SessionSecret   string
	DatabasePath    string
	Port            int
	HomeRedirectURL string
	TrustProxy      bool
}
