package redirect

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const (
	defaultQueueSize     int           = 1024
	defaultBatchSize     int           = 100
	defaultFlushInterval time.Duration = time.Second
	defaultWriteTimeout  time.Duration = 10 * time.Second
)

// BatchRecorderOptions tunes a BatchRecorder. Zero values use the defaults:
// a 1024-click queue, batches of 100, flushed at least once a second.
type BatchRecorderOptions struct {
	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
	WriteTimeout  time.Duration
}

// BatchRecorder implements ClickRecorder. Record only does a non-blocking
// channel send, so statistics can never slow or break a redirect; one
// background goroutine writes the queued clicks to the database in batches.
type BatchRecorder struct {
	sink    store.ClickStore
	logger  *slog.Logger
	options BatchRecorderOptions

	queue chan store.Click
	done  chan struct{}

	// closeMutex guards closed and the close of queue, so Record can never
	// send on a closed channel.
	closeMutex sync.RWMutex
	closed     bool

	dropped atomic.Int64
}

var _ ClickRecorder = (*BatchRecorder)(nil)

// NewBatchRecorder starts the background writer. Call Close to flush and stop it.
func NewBatchRecorder(sink store.ClickStore, logger *slog.Logger, options BatchRecorderOptions) *BatchRecorder {
	if options.QueueSize <= 0 {
		options.QueueSize = defaultQueueSize
	}
	if options.BatchSize <= 0 {
		options.BatchSize = defaultBatchSize
	}
	if options.FlushInterval <= 0 {
		options.FlushInterval = defaultFlushInterval
	}
	if options.WriteTimeout <= 0 {
		options.WriteTimeout = defaultWriteTimeout
	}

	recorder := &BatchRecorder{
		sink:    sink,
		logger:  logger,
		options: options,
		queue:   make(chan store.Click, options.QueueSize),
		done:    make(chan struct{}),
	}
	go recorder.run()
	return recorder
}

// Record queues a click. It never blocks: if the queue is full, or the recorder
// is closed, the click is dropped, counted, and reported in the log.
func (recorder *BatchRecorder) Record(click store.Click) bool {
	recorder.closeMutex.RLock()
	defer recorder.closeMutex.RUnlock()

	if !recorder.closed {
		select {
		case recorder.queue <- click:
			return true
		default:
		}
	}
	recorder.dropped.Add(1)
	return false
}

// Dropped returns how many clicks have been dropped so far.
func (recorder *BatchRecorder) Dropped() int64 {
	return recorder.dropped.Load()
}

// Close stops accepting clicks, flushes everything queued, and waits for the
// writer to finish or ctx to expire. It is safe to call more than once.
func (recorder *BatchRecorder) Close(ctx context.Context) error {
	recorder.closeMutex.Lock()
	if !recorder.closed {
		recorder.closed = true
		close(recorder.queue)
	}
	recorder.closeMutex.Unlock()

	select {
	case <-recorder.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("flush queued clicks before shutdown: %w", ctx.Err())
	}
}

func (recorder *BatchRecorder) run() {
	defer close(recorder.done)

	ticker := time.NewTicker(recorder.options.FlushInterval)
	defer ticker.Stop()

	batch := make([]store.Click, 0, recorder.options.BatchSize)
	var reportedDrops int64

	for {
		select {
		case click, open := <-recorder.queue:
			if !open {
				recorder.flush(batch)
				recorder.reportDrops(&reportedDrops)
				return
			}
			batch = append(batch, click)
			if len(batch) >= recorder.options.BatchSize {
				recorder.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			recorder.flush(batch)
			batch = batch[:0]
			recorder.reportDrops(&reportedDrops)
		}
	}
}

// flush writes the batch. A failed write is logged and the batch discarded:
// losing a few statistics is better than blocking or retrying forever.
func (recorder *BatchRecorder) flush(batch []store.Click) {
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), recorder.options.WriteTimeout)
	defer cancel()

	if err := recorder.sink.InsertClicks(ctx, batch); err != nil {
		recorder.logger.Error("write click batch failed; clicks discarded", "clicks", len(batch), "error", err)
	}
}

func (recorder *BatchRecorder) reportDrops(reported *int64) {
	total := recorder.dropped.Load()
	if total > *reported {
		recorder.logger.Warn("click queue was full; clicks dropped",
			"dropped_since_last_report", total-*reported, "dropped_total", total)
		*reported = total
	}
}
