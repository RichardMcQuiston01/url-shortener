// Package config loads and validates settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	defaultDatabasePath string = "/data/links.db"
	defaultPort         int    = 8080

	minAdminPasswordLength int = 16
	minAPIKeyLength        int = 32
	minSessionSecretLength int = 32
)

// Config holds every setting described in CLAUDE.md. Error messages from Load
// name a variable, never its value, so secrets cannot leak into logs.
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

// Load reads settings through getenv (os.Getenv in production) and validates
// them. It reports every problem at once, joined into a single error.
func Load(getenv func(string) string) (Config, error) {
	var problems []error

	config := Config{
		BaseURL:         strings.TrimSpace(getenv("BASE_URL")),
		AdminPassword:   getenv("ADMIN_PASSWORD"),
		APIKey:          getenv("API_KEY"),
		SessionSecret:   getenv("SESSION_SECRET"),
		DatabasePath:    strings.TrimSpace(getenv("DATABASE_PATH")),
		Port:            defaultPort,
		HomeRedirectURL: strings.TrimSpace(getenv("HOME_REDIRECT_URL")),
	}

	if err := validateBaseURL(config.BaseURL); err != nil {
		problems = append(problems, err)
	}
	if err := validateMinLength("ADMIN_PASSWORD", config.AdminPassword, minAdminPasswordLength); err != nil {
		problems = append(problems, err)
	}
	if err := validateMinLength("API_KEY", config.APIKey, minAPIKeyLength); err != nil {
		problems = append(problems, err)
	}
	if err := validateMinLength("SESSION_SECRET", config.SessionSecret, minSessionSecretLength); err != nil {
		problems = append(problems, err)
	}

	if config.DatabasePath == "" {
		config.DatabasePath = defaultDatabasePath
	}

	if rawPort := strings.TrimSpace(getenv("PORT")); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			problems = append(problems, errors.New("PORT must be a whole number from 1 to 65535"))
		} else {
			config.Port = port
		}
	}

	if config.HomeRedirectURL != "" {
		if err := validateHTTPURL("HOME_REDIRECT_URL", config.HomeRedirectURL); err != nil {
			problems = append(problems, err)
		}
	}

	if rawTrustProxy := strings.TrimSpace(getenv("TRUST_PROXY")); rawTrustProxy != "" {
		trustProxy, err := strconv.ParseBool(rawTrustProxy)
		if err != nil {
			problems = append(problems, errors.New("TRUST_PROXY must be true or false"))
		} else {
			config.TrustProxy = trustProxy
		}
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(problems...))
	}
	return config, nil
}

func validateBaseURL(baseURL string) error {
	if baseURL == "" {
		return errors.New("BASE_URL is required (for example https://go.richardmcquiston.com)")
	}
	if err := validateHTTPURL("BASE_URL", baseURL); err != nil {
		return err
	}
	if strings.HasSuffix(baseURL, "/") {
		return errors.New("BASE_URL must not end with a trailing slash")
	}
	return nil
}

func validateHTTPURL(variableName string, rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", variableName, errors.Unwrap(err))
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("%s must start with http:// or https://", variableName)
	}
	if parsedURL.Host == "" {
		return fmt.Errorf("%s must include a host", variableName)
	}
	return nil
}

func validateMinLength(variableName string, value string, minLength int) error {
	if value == "" {
		return fmt.Errorf("%s is required (at least %d characters)", variableName, minLength)
	}
	if len(value) < minLength {
		return fmt.Errorf("%s is too short: it must be at least %d characters", variableName, minLength)
	}
	return nil
}
