package config

import (
	"strings"
	"testing"
)

const (
	validPassword string = "correct-horse-battery"
	validAPIKey   string = "0123456789abcdef0123456789abcdef"
	validSecret   string = "fedcba9876543210fedcba9876543210"
)

func envFrom(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func validEnv() map[string]string {
	return map[string]string{
		"BASE_URL":       "https://go.example.com",
		"ADMIN_PASSWORD": validPassword,
		"API_KEY":        validAPIKey,
		"SESSION_SECRET": validSecret,
	}
}

func TestLoadDefaults(t *testing.T) {
	config, err := Load(envFrom(validEnv()))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if config.DatabasePath != "/data/links.db" || config.Port != 8080 || config.TrustProxy || config.HomeRedirectURL != "" {
		t.Errorf("unexpected defaults: %+v", config)
	}
}

func TestLoadOptionalValues(t *testing.T) {
	env := validEnv()
	env["DATABASE_PATH"] = "/tmp/x.db"
	env["PORT"] = "9090"
	env["HOME_REDIRECT_URL"] = "https://richardmcquiston.com"
	env["TRUST_PROXY"] = "true"
	config, err := Load(envFrom(env))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if config.DatabasePath != "/tmp/x.db" || config.Port != 9090 || !config.TrustProxy || config.HomeRedirectURL != "https://richardmcquiston.com" {
		t.Errorf("unexpected config: %+v", config)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	testCases := []struct {
		name          string
		variable      string
		value         string
		expectedError string
	}{
		{"missing base url", "BASE_URL", "", "BASE_URL is required"},
		{"base url trailing slash", "BASE_URL", "https://go.example.com/", "trailing slash"},
		{"base url bad scheme", "BASE_URL", "ftp://go.example.com", "BASE_URL must start with http"},
		{"base url no host", "BASE_URL", "https://", "BASE_URL must include a host"},
		{"missing password", "ADMIN_PASSWORD", "", "ADMIN_PASSWORD is required"},
		{"short password", "ADMIN_PASSWORD", "short", "ADMIN_PASSWORD is too short"},
		{"short api key", "API_KEY", "short", "API_KEY is too short"},
		{"short session secret", "SESSION_SECRET", "short", "SESSION_SECRET is too short"},
		{"port not a number", "PORT", "abc", "PORT must be"},
		{"port out of range", "PORT", "70000", "PORT must be"},
		{"bad home redirect", "HOME_REDIRECT_URL", "not-a-url", "HOME_REDIRECT_URL must start with http"},
		{"bad trust proxy", "TRUST_PROXY", "maybe", "TRUST_PROXY must be"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			env := validEnv()
			env[testCase.variable] = testCase.value
			_, err := Load(envFrom(env))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), testCase.expectedError) {
				t.Errorf("error %q does not contain %q", err.Error(), testCase.expectedError)
			}
		})
	}
}

func TestLoadNeverEchoesSecretValues(t *testing.T) {
	env := validEnv()
	env["ADMIN_PASSWORD"] = "tooshort-secret"
	env["API_KEY"] = "tooshort-key"
	_, err := Load(envFrom(env))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, secret := range []string{"tooshort-secret", "tooshort-key"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error message leaks secret value %q", secret)
		}
	}
}

func TestLoadReportsAllProblems(t *testing.T) {
	_, err := Load(envFrom(map[string]string{}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, name := range []string{"BASE_URL", "ADMIN_PASSWORD", "API_KEY", "SESSION_SECRET"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error should mention %s: %v", name, err)
		}
	}
}
