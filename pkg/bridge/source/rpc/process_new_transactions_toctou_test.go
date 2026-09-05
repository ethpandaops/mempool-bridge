package rpc

// Regression test for a TOCTOU race in processNewTransactions' dedup check
// against the shared Sent cache. A plain Get (early, per-tx) followed by a
// later, batched Set only after a successful handler call left a window
// where two peers (e.g. redundant source endpoints) racing the same
// transaction could both observe a miss and both forward it.
// processNewTransactions now claims each hash atomically via GetOrSet and
// releases the claim if it ends up not delivering the transaction after
// all, so concurrent peers racing the same tx must still result in exactly
// one delivery.
//
// Run: go test -race -run TestProcessNewTransactions_ConcurrentSameTxOnlyForwardsOnce -v ./pkg/bridge/source/rpc/...

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/sirupsen/logrus"
)

func TestProcessNewTransactions_ConcurrentSameTxOnlyForwardsOnce(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	const (
		trials     = 300
		peersPerTx = 20
	)

	for trial := 0; trial < trials; trial++ {
		sharedCache := cache.NewSharedCacheWithRegisterer(nil)
		metrics := source.NewMetricsWithRegisterer("test_process_new_transactions_toctou", nil)

		var forwardedCount int64

		handler := func(_ context.Context, txs *mimicry.Transactions) error {
			if txs != nil {
				atomic.AddInt64(&forwardedCount, int64(len(*txs)))
			}

			return nil
		}

		// Same underlying transaction, observed independently by multiple
		// peers sharing one SharedCache — models redundant source
		// endpoints all polling the same still-pending tx at once.
		tx := types.NewTransaction(uint64(trial), common.HexToAddress("0x1234"), nil, 21000, nil, nil)
		txs := []*types.Transaction{tx}

		peers := make([]*Peer, peersPerTx)

		for i := range peers {
			peer, err := NewPeer(context.Background(), log, "http://peer", handler, sharedCache, nil, metrics, 0)
			if err != nil {
				t.Fatalf("NewPeer: %v", err)
			}

			peers[i] = peer
		}

		var wg sync.WaitGroup

		start := make(chan struct{})

		for _, peer := range peers {
			wg.Add(1)

			go func(p *Peer) {
				defer wg.Done()
				<-start // release all goroutines at once to maximize the race window

				_ = p.processNewTransactions(context.Background(), txs)
			}(peer)
		}

		close(start)
		wg.Wait()

		if got := atomic.LoadInt64(&forwardedCount); got != 1 {
			t.Fatalf("trial %d: expected exactly 1 forward from %d peers racing the same tx, got %d",
				trial, peersPerTx, got)
		}
	}
}
