package p2p

// Regression test for a data race between SendTransactionsToPeers and the
// peer map mutations that happen during connect/disconnect churn. The peer
// map is guarded by c.mu, but SendTransactionsToPeers used to copy only the
// map header under the lock and then range over the live map unlocked,
// while connect/disconnect goroutines kept writing to that same map under
// the lock. This drives both sides concurrently and requires a clean run
// under the race detector.
//
// Run: go test -race -run TestSendTransactionsToPeers_ConcurrentPeerMapAccess ./pkg/bridge/target/p2p/...

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/target"
	"github.com/sirupsen/logrus"
)

func TestSendTransactionsToPeers_ConcurrentPeerMapAccess(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	c := &Coordinator{
		log:        log,
		peers:      &map[string]*Peer{},
		metrics:    target.NewMetrics("mempool_bridge_target_test_map_access"),
		txCounters: make(map[string]int),
	}

	records := []string{"peerA", "peerB", "peerC", "peerD", "peerE"}
	for _, r := range records {
		c.mu.Lock()
		(*c.peers)[r] = &Peer{ready: false, log: log}
		c.mu.Unlock()
	}

	tx := types.NewTransaction(0, common.HexToAddress("0x1234"), nil, 21000, nil, nil)
	txs := mimicry.Transactions{tx}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	stop := make(chan struct{})

	// Writer goroutines mimic connect/disconnect churn: peers dropping out
	// and reconnecting while transactions are being broadcast.
	for i := 0; i < 4; i++ {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			record := records[id%len(records)]

			for {
				select {
				case <-stop:
					return
				default:
				}

				c.mu.Lock()
				(*c.peers)[record] = nil
				c.mu.Unlock()

				c.mu.Lock()
				(*c.peers)[record] = &Peer{ready: false, log: log}
				c.mu.Unlock()
			}
		}(i)
	}

	// Reader goroutines mimic the hot path: broadcasting to peers.
	for i := 0; i < 4; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				_ = c.SendTransactionsToPeers(ctx, &txs)
			}
		}()
	}

	<-ctx.Done()
	close(stop)
	wg.Wait()
}
