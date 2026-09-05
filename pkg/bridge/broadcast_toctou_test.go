package bridge

// Regression test for a TOCTOU race in broadcast's dedup check. A plain Get
// then later Set has a window where two concurrent callers racing the same
// transaction hash can both observe a cache miss and both proceed to
// forward it. broadcast now uses the cache's atomic GetOrSet instead, so
// releasing many goroutines at once against the same hash must always
// result in exactly one of them winning.
//
// Run: go test -race -run TestBroadcast_ConcurrentSameTxOnlyForwardsOnce -v ./pkg/bridge/...

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/sirupsen/logrus"
)

func TestBroadcast_ConcurrentSameTxOnlyForwardsOnce(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	const (
		trials          = 300
		goroutinesPerTx = 20
	)

	for trial := 0; trial < trials; trial++ {
		b := &Bridge{
			log:       log,
			sentCache: mustCreateSentCache(t),
			metrics:   NewMetricsWithRegisterer("test_broadcast_toctou", nil),
		}

		ctx := context.Background()
		if err := b.sentCache.Start(ctx); err != nil {
			t.Fatalf("failed to start sentCache: %v", err)
		}

		var forwardedCount int64
		b.t = &mockTargetCoordinator{
			sendFunc: func(_ context.Context, txs *mimicry.Transactions) error {
				atomic.AddInt64(&forwardedCount, int64(len(*txs)))
				return nil
			},
		}

		// Models N concurrent source-peer goroutines (e.g. multiple p2p
		// peers, or concurrent poll cycles) all observing the exact same
		// pending tx at the same instant.
		tx := types.NewTransaction(uint64(trial), common.HexToAddress("0x1234"), nil, 21000, nil, nil)
		txs := mimicry.Transactions{tx}

		var wg sync.WaitGroup

		start := make(chan struct{})

		for g := 0; g < goroutinesPerTx; g++ {
			wg.Add(1)

			go func() {
				defer wg.Done()
				<-start // release all goroutines at once to maximize the race window

				_ = b.broadcast(ctx, &txs)
			}()
		}

		close(start)
		wg.Wait()
		b.sentCache.Stop()

		if got := atomic.LoadInt64(&forwardedCount); got != 1 {
			t.Fatalf("trial %d: expected exactly 1 forward from %d goroutines racing the same tx, got %d",
				trial, goroutinesPerTx, got)
		}
	}
}
