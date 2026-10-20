// Package redirect serves public short-link redirects and records clicks.
package redirect

import (
	"context"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

// ClickRecorder queues clicks without ever blocking a redirect.
type ClickRecorder interface {
	// Record queues a click. It returns false if the queue was full and the
	// click was dropped (the drop is counted and logged).
	Record(click store.Click) bool
	// Close flushes queued clicks and stops the background writer. It
	// returns when flushing finishes or ctx expires.
	Close(ctx context.Context) error
}
