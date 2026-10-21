package redirect

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

// memorySink stores inserted clicks. If block is non-nil, InsertClicks waits on
// it first, simulating a stalled database. If fail is set, it returns that error.
type memorySink struct {
	mutex   sync.Mutex
	clicks  []store.Click
	batches []int
	block   chan struct{}
	fail    error
}

func (sink *memorySink) InsertClicks(ctx context.Context, clicks []store.Click) error {
	if sink.block != nil {
		select {
		case <-sink.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	sink.batches = append(sink.batches, len(clicks))
	if sink.fail != nil {
		return sink.fail
	}
	sink.clicks = append(sink.clicks, clicks...)
	return nil
}

func (sink *memorySink) count() int {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	return len(sink.clicks)
}

func waitForCount(t *testing.T, sink *memorySink, expected int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sink.count() == expected {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("sink has %d clicks, want %d", sink.count(), expected)
}

func testClick(index int) store.Click {
	return store.Click{LinkID: "link", ClickedAt: time.Now().UTC().Add(time.Duration(index) * time.Second)}
}

func closeWithin(t *testing.T, recorder *BatchRecorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := recorder.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestFlushesWhenBatchIsFull(t *testing.T) {
	sink := &memorySink{}
	recorder := NewBatchRecorder(sink, discardLogger(), BatchRecorderOptions{BatchSize: 3, FlushInterval: time.Hour})
	defer closeWithin(t, recorder)

	for index := 0; index < 3; index++ {
		if !recorder.Record(testClick(index)) {
			t.Fatalf("Record %d was rejected", index)
		}
	}
	waitForCount(t, sink, 3)
}

func TestFlushesOnInterval(t *testing.T) {
	sink := &memorySink{}
	recorder := NewBatchRecorder(sink, discardLogger(), BatchRecorderOptions{BatchSize: 100, FlushInterval: 20 * time.Millisecond})
	defer closeWithin(t, recorder)

	recorder.Record(testClick(0))
	waitForCount(t, sink, 1)
}

func TestCloseFlushesQueuedClicks(t *testing.T) {
	sink := &memorySink{}
	recorder := NewBatchRecorder(sink, discardLogger(), BatchRecorderOptions{BatchSize: 100, FlushInterval: time.Hour})

	for index := 0; index < 5; index++ {
		recorder.Record(testClick(index))
	}
	closeWithin(t, recorder)

	if sink.count() != 5 {
		t.Errorf("sink has %d clicks after Close, want all 5", sink.count())
	}
}

func TestRecordNeverBlocksAndDropsWhenQueueIsFull(t *testing.T) {
	sink := &memorySink{block: make(chan struct{})}
	recorder := NewBatchRecorder(sink, discardLogger(), BatchRecorderOptions{QueueSize: 2, BatchSize: 1, FlushInterval: time.Hour})

	startedAt := time.Now()
	accepted := 0
	for index := 0; index < 200; index++ {
		if recorder.Record(testClick(index)) {
			accepted++
		}
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Errorf("200 Record calls took %v against a stalled database; they must never block", elapsed)
	}
	if accepted == 200 || recorder.Dropped() == 0 {
		t.Errorf("accepted %d, dropped %d: a full queue must drop clicks", accepted, recorder.Dropped())
	}
	if int64(accepted)+recorder.Dropped() != 200 {
		t.Errorf("accepted %d + dropped %d != 200", accepted, recorder.Dropped())
	}

	close(sink.block) // unstall so Close can flush what was accepted
	closeWithin(t, recorder)
	if sink.count() != accepted {
		t.Errorf("sink has %d clicks, want the %d accepted ones", sink.count(), accepted)
	}
}

func TestRecordAfterCloseIsRejectedWithoutPanicking(t *testing.T) {
	recorder := NewBatchRecorder(&memorySink{}, discardLogger(), BatchRecorderOptions{})
	closeWithin(t, recorder)
	closeWithin(t, recorder) // closing twice is fine

	if recorder.Record(testClick(0)) {
		t.Error("Record after Close should return false")
	}
	if recorder.Dropped() != 1 {
		t.Errorf("dropped = %d, want 1", recorder.Dropped())
	}
}

func TestWriteFailureIsLoggedAndLaterBatchesStillWork(t *testing.T) {
	sink := &memorySink{fail: errors.New("disk on fire")}
	recorder := NewBatchRecorder(sink, discardLogger(), BatchRecorderOptions{BatchSize: 1, FlushInterval: time.Hour})
	defer closeWithin(t, recorder)

	recorder.Record(testClick(0))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sink.mutex.Lock()
		attempts := len(sink.batches)
		sink.mutex.Unlock()
		if attempts >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	sink.mutex.Lock()
	sink.fail = nil
	sink.mutex.Unlock()
	recorder.Record(testClick(1))
	waitForCount(t, sink, 1)
}

func TestCloseGivesUpWhenContextExpires(t *testing.T) {
	sink := &memorySink{block: make(chan struct{})}
	recorder := NewBatchRecorder(sink, discardLogger(), BatchRecorderOptions{BatchSize: 1, FlushInterval: time.Hour})
	recorder.Record(testClick(0))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := recorder.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close error = %v, want a deadline error", err)
	}
	close(sink.block)
	closeWithin(t, recorder)
}

func TestBatchRecorderWithRealStore(t *testing.T) {
	ctx := context.Background()
	sqlite, err := store.Open(ctx, t.TempDir()+"/links.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlite.Close()
	link, err := sqlite.Create(ctx, store.NewLink{Slug: "a", TargetURL: "https://example.com/a"})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}

	recorder := NewBatchRecorder(sqlite, discardLogger(), BatchRecorderOptions{FlushInterval: time.Hour})
	host := "news.example"
	recorder.Record(store.Click{LinkID: link.ID, ClickedAt: time.Now().UTC(), ReferrerHost: &host})
	recorder.Record(store.Click{LinkID: link.ID, ClickedAt: time.Now().UTC(), IsBot: true})
	closeWithin(t, recorder)

	summary, err := sqlite.GetLinkSummaryBySlug(ctx, "a")
	if err != nil {
		t.Fatalf("GetLinkSummaryBySlug: %v", err)
	}
	if summary.TotalClicks != 1 {
		t.Errorf("total clicks = %d, want 1 (the bot click is stored but not counted)", summary.TotalClicks)
	}
}
