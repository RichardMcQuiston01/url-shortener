package redirect

import "testing"

func TestIsBot(t *testing.T) {
	testCases := []struct {
		name      string
		userAgent string
		expected  bool
	}{
		{"firefox", "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0", false},
		{"safari on iphone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1", false},
		{"googlebot", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", true},
		{"bingbot", "Mozilla/5.0 (compatible; bingbot/2.0)", true},
		{"curl", "curl/8.5.0", true},
		{"wget", "Wget/1.21", true},
		{"python requests", "python-requests/2.31.0", true},
		{"go client", "Go-http-client/1.1", true},
		{"facebook preview", "facebookexternalhit/1.1", true},
		{"slack link preview", "Slackbot-LinkExpanding 1.0", true},
		{"headless chrome", "Mozilla/5.0 HeadlessChrome/120.0", true},
		{"uptime monitor", "UptimeRobot/2.0", true},
		{"empty", "", true},
		{"whitespace only", "   ", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := isBot(testCase.userAgent); actual != testCase.expected {
				t.Errorf("isBot(%q) = %v, want %v", testCase.userAgent, actual, testCase.expected)
			}
		})
	}
}

func TestReferrerHost(t *testing.T) {
	testCases := []struct {
		name     string
		referer  string
		expected string // empty means nil
	}{
		{"https with path and query", "https://news.example.com/story?id=7#top", "news.example.com"},
		{"port is dropped", "http://example.com:8080/x", "example.com"},
		{"host is lower-cased", "https://EXAMPLE.com/", "example.com"},
		{"missing", "", ""},
		{"not a url", "not a url", ""},
		{"non-http scheme", "android-app://com.example.app/", ""},
		{"no host", "https:///path", ""},
		{"unparseable", "https://exa mple.com/", ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := referrerHost(testCase.referer)
			switch {
			case testCase.expected == "" && actual != nil:
				t.Errorf("referrerHost(%q) = %q, want nil", testCase.referer, *actual)
			case testCase.expected != "" && (actual == nil || *actual != testCase.expected):
				t.Errorf("referrerHost(%q) = %v, want %q", testCase.referer, actual, testCase.expected)
			}
		})
	}
}
