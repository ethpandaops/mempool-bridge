/*
 * This processor tests was adapted from the OpenTelemetry Collector's batch processor tests.
 *
 * Authors: OpenTelemetry
 * URL: https://github.com/open-telemetry/opentelemetry-go/blob/496c086ece129182662c14d6a023a2b2da09fe30/sdk/trace/batch_item_processor_test.go
 */
package processor

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errFailToExport = errors.New("fail to export")

type TestItem struct {
	name string
}

type testBatchExporter[T TestItem] struct {
	mu            sync.Mutex
	items         []*T
	sizes         []int
	batchCount    int
	shutdownCount int
	errors        []error
	droppedCount  int
	idx           int
	err           error
}

func (t *testBatchExporter[T]) ExportItems(ctx context.Context, items []*T) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.idx < len(t.errors) {
		t.droppedCount += len(items)
		err := t.errors[t.idx]
		t.idx++

		return err
	}

	select {
	case <-ctx.Done():
		t.err = ctx.Err()

		return ctx.Err()
	default:
	}

	t.items = append(t.items, items...)
	t.sizes = append(t.sizes, len(items))
	t.batchCount++

	return nil
}

func (t *testBatchExporter[T]) Shutdown(context.Context) error {
	t.shutdownCount++

	return nil
}

func (t *testBatchExporter[T]) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.items)
}

func (t *testBatchExporter[T]) getBatchCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.batchCount
}

func (t *testBatchExporter[T]) getErr() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.err
}

func (t *testBatchExporter[T]) getDroppedCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.droppedCount
}

func TestNewBatchItemProcessorWithNilExporter(t *testing.T) {
	bsp := NewBatchItemProcessor[TestItem](nil, nullLogger())

	bsp.Write(&TestItem{
		name: "test",
	})

	if err := bsp.ForceFlush(context.Background()); err != nil {
		t.Errorf("failed to ForceFlush the BatchItemProcessor: %v", err)
	}

	if err := bsp.Shutdown(context.Background()); err != nil {
		t.Errorf("failed to Shutdown the BatchItemProcessor: %v", err)
	}
}

type testOption struct {
	name           string
	o              []BatchItemProcessorOption
	writeNumItems  int
	genNumItems    int
	wantNumItems   int
	wantBatchCount int
}

func TestNewBatchItemProcessorWithOptions(t *testing.T) {
	schDelay := 100 * time.Millisecond
	options := []testOption{
		{
			name:           "default",
			o:              []BatchItemProcessorOption{},
			genNumItems:    2053,
			wantNumItems:   2048,
			wantBatchCount: 4,
		},
		{
			name: "non-default BatchTimeout",
			o: []BatchItemProcessorOption{
				WithBatchTimeout(schDelay),
			},
			writeNumItems:  2048,
			genNumItems:    2053,
			wantNumItems:   2053,
			wantBatchCount: 5,
		},
		{
			name: "non-default MaxQueueSize and BatchTimeout",
			o: []BatchItemProcessorOption{
				WithBatchTimeout(schDelay),
				WithMaxQueueSize(200),
			},
			writeNumItems:  200,
			genNumItems:    205,
			wantNumItems:   205,
			wantBatchCount: 1,
		},
		{
			name: "blocking option",
			o: []BatchItemProcessorOption{
				WithBatchTimeout(schDelay),
				WithMaxQueueSize(200),
				WithMaxExportBatchSize(20),
			},
			writeNumItems:  200,
			genNumItems:    205,
			wantNumItems:   205,
			wantBatchCount: 11,
		},
	}

	for _, option := range options {
		t.Run(option.name, func(t *testing.T) {
			te := testBatchExporter[TestItem]{}

			ssp := createAndRegisterBatchSP(option.o, &te)
			if ssp == nil {
				t.Fatalf("%s: Error creating new instance of BatchItemProcessor\n", option.name)
			}

			for i := 0; i < option.genNumItems; i++ {
				if option.writeNumItems > 0 && i%option.writeNumItems == 0 {
					time.Sleep(10 * time.Millisecond)
				}

				ssp.Write(&TestItem{
					name: "test",
				})
			}

			time.Sleep(schDelay * 2)

			gotNumOfItems := te.len()
			if option.wantNumItems > 0 && option.wantNumItems != gotNumOfItems {
				t.Errorf("number of exported items: got %+v, want %+v\n",
					gotNumOfItems, option.wantNumItems)
			}

			gotBatchCount := te.getBatchCount()
			if option.wantBatchCount > 0 && gotBatchCount != option.wantBatchCount {
				t.Errorf("number batches: got %+v, want >= %+v\n",
					gotBatchCount, option.wantBatchCount)
				t.Errorf("Batches %v\n", te.sizes)
			}
		})
	}
}

type stuckExporter[T TestItem] struct {
	testBatchExporter[T]
}

// ExportItems waits for ctx to expire and returns that error.
func (e *stuckExporter[T]) ExportItems(ctx context.Context, _ []*T) error {
	<-ctx.Done()

	e.mu.Lock()
	e.err = ctx.Err()
	e.mu.Unlock()

	return ctx.Err()
}

func TestBatchItemProcessorExportTimeout(t *testing.T) {
	exp := new(stuckExporter[TestItem])
	bvp := NewBatchItemProcessor[TestItem](
		exp,
		nullLogger(),
		// Set a non-zero export timeout so a deadline is set.
		WithExportTimeout(1*time.Microsecond),
		WithBatchTimeout(1*time.Microsecond),
	)

	bvp.Write(&TestItem{
		name: "test",
	})

	time.Sleep(1 * time.Millisecond)

	if !errors.Is(exp.getErr(), context.DeadlineExceeded) {
		t.Errorf("context deadline error not returned: got %+v", exp.getErr())
	}
}

func createAndRegisterBatchSP[T TestItem](options []BatchItemProcessorOption, te *testBatchExporter[T]) *BatchItemProcessor[T] {
	return NewBatchItemProcessor[T](te, nullLogger(), options...)
}

func TestBatchItemProcessorShutdown(t *testing.T) {
	var bp testBatchExporter[TestItem]
	bvp := NewBatchItemProcessor[TestItem](&bp, nullLogger())

	err := bvp.Shutdown(context.Background())
	if err != nil {
		t.Error("Error shutting the BatchItemProcessor down\n")
	}

	assert.Equal(t, 1, bp.shutdownCount, "shutdown from item exporter not called")

	// Multiple call to Shutdown() should not panic.
	err = bvp.Shutdown(context.Background())
	if err != nil {
		t.Error("Error shutting the BatchItemProcessor down\n")
	}

	assert.Equal(t, 1, bp.shutdownCount)
}

func TestBatchItemProcessorPostShutdown(t *testing.T) {
	be := testBatchExporter[TestItem]{}
	bsp := NewBatchItemProcessor[TestItem](&be, nullLogger(), WithMaxExportBatchSize(50))

	for i := 0; i < 60; i++ {
		bsp.Write(&TestItem{
			name: strconv.Itoa(i),
		})
	}

	require.NoError(t, bsp.Shutdown(context.Background()), "shutting down BatchItemProcessor")

	lenJustAfterShutdown := be.len()

	assert.NoError(t, bsp.ForceFlush(context.Background()), "force flushing BatchItemProcessor")

	assert.Equal(t, lenJustAfterShutdown, be.len(), "Write and ForceFlush should have no effect after Shutdown")
}

func TestBatchItemProcessorForceFlushSucceeds(t *testing.T) {
	te := testBatchExporter[TestItem]{}
	option := testOption{
		name: "default BatchItemProcessorOptions",
		o: []BatchItemProcessorOption{
			WithMaxQueueSize(0),
			WithMaxExportBatchSize(3000),
		},
		genNumItems:    2053,
		wantNumItems:   2053,
		wantBatchCount: 1,
	}
	ssp := createAndRegisterBatchSP(option.o, &te)

	if ssp == nil {
		t.Fatalf("%s: Error creating new instance of BatchItemProcessor\n", option.name)
	}

	for i := 0; i < option.genNumItems; i++ {
		ssp.Write(&TestItem{
			name: strconv.Itoa(i),
		})
		time.Sleep(1 * time.Millisecond)
	}

	// Force flush any held item batches
	err := ssp.ForceFlush(context.Background())

	assertMaxItemDiff(t, te.len(), option.wantNumItems, 10)

	gotBatchCount := te.getBatchCount()
	if gotBatchCount < option.wantBatchCount {
		t.Errorf("number batches: got %+v, want >= %+v\n",
			gotBatchCount, option.wantBatchCount)
		t.Errorf("Batches %v\n", te.sizes)
	}

	assert.NoError(t, err)
}

func TestBatchItemProcessorDropBatchIfFailed(t *testing.T) {
	te := testBatchExporter[TestItem]{
		errors: []error{errFailToExport},
	}
	option := testOption{
		o: []BatchItemProcessorOption{
			WithMaxQueueSize(0),
			WithMaxExportBatchSize(2000),
		},
		genNumItems:    1000,
		wantNumItems:   1000,
		wantBatchCount: 1,
	}
	ssp := createAndRegisterBatchSP(option.o, &te)

	if ssp == nil {
		t.Fatalf("%s: Error creating new instance of BatchItemProcessor\n", option.name)
	}

	for i := 0; i < option.genNumItems; i++ {
		ssp.Write(&TestItem{
			name: strconv.Itoa(i),
		})
		time.Sleep(1 * time.Millisecond)
	}

	// Force flush any held item batches
	err := ssp.ForceFlush(context.Background())
	assert.Error(t, err)
	assert.EqualError(t, err, "fail to export")

	// First flush will fail, nothing should be exported.
	assertMaxItemDiff(t, te.getDroppedCount(), option.wantNumItems, 10)
	assert.Equal(t, 0, te.len())
	assert.Equal(t, 0, te.getBatchCount())

	// Generate a new batch, this will succeed
	for i := 0; i < option.genNumItems; i++ {
		ssp.Write(&TestItem{
			name: strconv.Itoa(i),
		})
		time.Sleep(1 * time.Millisecond)
	}

	// Force flush any held item batches
	err = ssp.ForceFlush(context.Background())
	assert.NoError(t, err)

	assertMaxItemDiff(t, te.len(), option.wantNumItems, 10)
	gotBatchCount := te.getBatchCount()

	if gotBatchCount < option.wantBatchCount {
		t.Errorf("number batches: got %+v, want >= %+v\n",
			gotBatchCount, option.wantBatchCount)
		t.Errorf("Batches %v\n", te.sizes)
	}
}

func assertMaxItemDiff(t *testing.T, got, want, maxDif int) {
	t.Helper()

	itemDifference := want - got
	if itemDifference < 0 {
		itemDifference *= -1
	}

	if itemDifference > maxDif {
		t.Errorf("number of exported item not equal to or within %d less than: got %+v, want %+v\n",
			maxDif, got, want)
	}
}

type indefiniteExporter[T TestItem] struct{}

func (indefiniteExporter[T]) Shutdown(context.Context) error { return nil }
func (indefiniteExporter[T]) ExportItems(ctx context.Context, _ []*T) error {
	<-ctx.Done()

	return ctx.Err()
}

func TestBatchItemProcessorForceFlushTimeout(t *testing.T) {
	// Add timeout to context to test deadline
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	bsp := NewBatchItemProcessor[TestItem](indefiniteExporter[TestItem]{}, nullLogger())
	if got, want := bsp.ForceFlush(ctx), context.DeadlineExceeded; !errors.Is(got, want) {
		t.Errorf("expected %q error, got %v", want, got)
	}
}

func TestBatchItemProcessorForceFlushCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel the context
	cancel()

	bsp := NewBatchItemProcessor[TestItem](indefiniteExporter[TestItem]{}, nullLogger())
	if got, want := bsp.ForceFlush(ctx), context.Canceled; !errors.Is(got, want) {
		t.Errorf("expected %q error, got %v", want, got)
	}
}

// Regression test for enqueueDrop silently dropping items once the
// processor is stopped, without counting them the way the queue-full path
// already does.
func TestEnqueueDrop_CountsDropsAfterStop(t *testing.T) {
	te := testBatchExporter[TestItem]{}
	bvp := NewBatchItemProcessor[TestItem](&te, nullLogger())

	require.NoError(t, bvp.Shutdown(context.Background()))

	before := atomic.LoadUint32(&bvp.dropped)

	if wrote := bvp.enqueueDrop(context.Background(), &TestItem{name: "test"}); wrote {
		t.Fatal("expected enqueueDrop to report the item as not written once stopped")
	}

	after := atomic.LoadUint32(&bvp.dropped)
	if after != before+1 {
		t.Fatalf("expected the dropped counter to increment by 1, got %d -> %d", before, after)
	}
}

// blockingExporter blocks inside ExportItems until told to proceed, so a
// caller can force ForceFlush to hit ctx.Done() before the detached export
// goroutine finishes.
type blockingExporter[T TestItem] struct {
	proceed chan struct{}
}

func (e *blockingExporter[T]) ExportItems(_ context.Context, _ []*T) error {
	<-e.proceed

	return nil
}

func (e *blockingExporter[T]) Shutdown(context.Context) error { return nil }

// Regression test for ForceFlush leaking its detached export goroutine when
// ctx is cancelled before the export finishes: the goroutine still tries to
// deliver its result on wait, and with wait unbuffered and ForceFlush
// already returned, that send blocked forever.
func TestForceFlush_DoesNotLeakGoroutineOnCtxCancel(t *testing.T) {
	const n = 20

	proceed := make([]chan struct{}, n)
	bvps := make([]*BatchItemProcessor[TestItem], n)

	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for i := 0; i < n; i++ {
		proceed[i] = make(chan struct{})
		bvps[i] = NewBatchItemProcessor[TestItem](&blockingExporter[TestItem]{proceed: proceed[i]}, nullLogger())

		bvps[i].Write(&TestItem{name: "test"})
		time.Sleep(20 * time.Millisecond) // let processQueue move it into bvp.batch

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // already cancelled: ForceFlush must return via ctx.Done(), not wait

		if err := bvps[i].ForceFlush(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("ForceFlush() error = %v, want %v", err, context.Canceled)
		}
	}

	// Let every blocked ExportItems call finish now that nothing is
	// waiting on ForceFlush's result. Each detached goroutine should still
	// be able to deliver into wait and exit, not leak blocked forever.
	for _, p := range proceed {
		close(p)
	}

	time.Sleep(100 * time.Millisecond)

	// Shut every processor down cleanly so its always-present
	// processQueue/drainQueue goroutine (unrelated to the bug under test)
	// doesn't get counted as part of the leak.
	for _, bvp := range bvps {
		require.NoError(t, bvp.Shutdown(context.Background()))
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()

		if runtime.NumGoroutine() <= baseline+2 { // small slack for unrelated background goroutines
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("goroutine count did not settle back to baseline, leak suspected: baseline=%d, now=%d",
		baseline, runtime.NumGoroutine())
}

func nullLogger() *logrus.Logger {
	log := logrus.New()
	log.Out = io.Discard

	return log
}
