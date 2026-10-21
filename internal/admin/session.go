package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	adminSessionLifetime time.Duration = 12 * time.Hour
	// A visitor who has not signed in yet gets a short-lived session that only
	// carries the CSRF token for the login form.
	anonymousSessionLifetime time.Duration = 30 * time.Minute

	csrfTokenBytes int = 32
)

var errInvalidSession error = errors.New("session cookie is missing, malformed, tampered with or expired")

// flashMessage is shown once on the next dashboard view. It stores only a kind
// and a slug; the text is built at render time.
type flashMessage struct {
	Kind string `json:"kind"`
	Slug string `json:"slug,omitempty"`
}

// session is the whole server-side state, kept inside a signed cookie.
type session struct {
	ExpiresAt     int64         `json:"exp"`
	CSRFToken     string        `json:"csrf"`
	Authenticated bool          `json:"auth"`
	Flash         *flashMessage `json:"flash,omitempty"`
}

// sessionCodec signs and verifies sessions with HMAC-SHA256. The cookie value
// is base64url(json) + "." + base64url(mac).
type sessionCodec struct {
	secret []byte
}

func (codec sessionCodec) encode(current session) (string, error) {
	payload, err := json.Marshal(current)
	if err != nil {
		return "", fmt.Errorf("encode session: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	return encodedPayload + "." + codec.sign(encodedPayload), nil
}

// decode verifies the signature and expiry. It returns errInvalidSession for
// every kind of failure so callers cannot leak which check failed.
func (codec sessionCodec) decode(cookieValue string, now time.Time) (session, error) {
	encodedPayload, encodedMAC, found := strings.Cut(cookieValue, ".")
	if !found {
		return session{}, errInvalidSession
	}
	if !hmac.Equal([]byte(encodedMAC), []byte(codec.sign(encodedPayload))) {
		return session{}, errInvalidSession
	}

	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return session{}, errInvalidSession
	}
	var decoded session
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return session{}, errInvalidSession
	}
	if decoded.CSRFToken == "" || now.Unix() >= decoded.ExpiresAt {
		return session{}, errInvalidSession
	}
	return decoded, nil
}

func (codec sessionCodec) sign(encodedPayload string) string {
	mac := hmac.New(sha256.New, codec.secret)
	mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func newCSRFToken() (string, error) {
	raw := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate csrf token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func newSession(now time.Time, authenticated bool) (session, error) {
	token, err := newCSRFToken()
	if err != nil {
		return session{}, err
	}
	lifetime := anonymousSessionLifetime
	if authenticated {
		lifetime = adminSessionLifetime
	}
	return session{
		ExpiresAt:     now.Add(lifetime).Unix(),
		CSRFToken:     token,
		Authenticated: authenticated,
	}, nil
}
