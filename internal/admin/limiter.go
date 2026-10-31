package admin

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxLoginFailures   int           = 5
	loginFailureWindow time.Duration = 15 * time.Minute

	// Above this many tracked clients, expired entries are swept on each call so
	// memory cannot grow without bound.
	limiterSweepThreshold int = 1024
)

// loginLimiter counts failed logins per client IP in memory: after
// maxLoginFailures failures inside loginFailureWindow the client is blocked
// until the oldest failure ages out.
type loginLimiter struct {
	mutex    sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	return &loginLimiter{failures: make(map[string][]time.Time), now: now}
}

// blocked reports whether the client has used up its attempts, and how long
// until it may try again.
func (limiter *loginLimiter) blocked(clientIP string) (bool, time.Duration) {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

	recent := limiter.pruned(clientIP)
	if len(recent) < maxLoginFailures {
		return false, 0
	}
	retryAfter := recent[0].Add(loginFailureWindow).Sub(limiter.now())
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return true, retryAfter
}

// recordFailure notes a failed login and returns how many failures the client
// now has inside the window.
func (limiter *loginLimiter) recordFailure(clientIP string) int {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()

	if len(limiter.failures) > limiterSweepThreshold {
		for knownIP := range limiter.failures {
			limiter.pruned(knownIP)
		}
	}
	recent := append(limiter.pruned(clientIP), limiter.now())
	limiter.failures[clientIP] = recent
	return len(recent)
}

// reset forgets a client's failures after a successful login.
func (limiter *loginLimiter) reset(clientIP string) {
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	delete(limiter.failures, clientIP)
}

// pruned drops failures older than the window and returns the rest. The caller
// must hold the mutex.
func (limiter *loginLimiter) pruned(clientIP string) []time.Time {
	cutoff := limiter.now().Add(-loginFailureWindow)
	recent := limiter.failures[clientIP][:0]
	for _, failedAt := range limiter.failures[clientIP] {
		if failedAt.After(cutoff) {
			recent = append(recent, failedAt)
		}
	}
	if len(recent) == 0 {
		delete(limiter.failures, clientIP)
		return nil
	}
	limiter.failures[clientIP] = recent
	return recent
}

// clientIP returns the caller's address. Behind a trusted reverse proxy it is
// the last X-Forwarded-For entry, which the proxy itself appended; earlier
// entries can be forged by the client. Otherwise it is the socket address.
func clientIP(request *http.Request, trustProxy bool) string {
	if trustProxy {
		if forwarded := request.Header.Get("X-Forwarded-For"); forwarded != "" {
			entries := strings.Split(forwarded, ",")
			if candidate := strings.TrimSpace(entries[len(entries)-1]); candidate != "" {
				return candidate
			}
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}
