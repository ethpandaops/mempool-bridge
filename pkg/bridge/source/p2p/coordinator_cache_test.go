package p2p

// Regression test for the shared Transaction cache's eviction goroutine
// never being started. Coordinator.Start went straight to dialing peers and
// starting crons without starting the cache, so expired entries were only
// ever evicted lazily on a Get() of that exact key. Since this cache is
// fed by push-based P2P events, most hashes are never re-queried, so
// expired entries would accumulate for as long as the process runs.
//
// This drives the real Coordinator.Start with no configured node records
// (so there is nothing to dial) and checks that the cache's own eviction
// metric actually increments once an entry expires.
//
// Run: go test -run TestCoordinatorStart_StartsSharedCacheJanitor -v ./pkg/bridge/source/p2p/...

import (
	"context"
	"testing"
	"time"

	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/sirupsen/logrus"
)

func TestCoordinatorStart_StartsSharedCacheJanitor(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	c := &Coordinator{
		config:  &source.Config{},
		log:     log,
		cache:   cache.NewSharedCacheWithRegisterer(nil),
		peers:   &map[string]bool{},
		metrics: source.NewMetricsWithRegisterer("test_coordinator_cache_start", nil),
	}

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.cache.Stop()

	c.cache.Transaction.Set("0xabc", nil, 10*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.cache.Transaction.Metrics().Evictions >= 1 {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("expected the shared cache's janitor to evict the expired entry, but Start() apparently never started it")
}
