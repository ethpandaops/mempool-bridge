package rpc

// Regression tests for the local seen-hash cache. It used to be a plain
// map with no time-based expiry at all: entries only ever went away in a
// single full wipe once the map passed 10000 entries, which cleared
// recently-seen entries for still-legitimately-pending transactions right
// alongside stale ones. Individual entries now expire on their own TTL
// instead.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/jellydator/ttlcache/v3"
	"github.com/sirupsen/logrus"
)

func newTestPeerForSeenHashes(t *testing.T) *Peer {
	t.Helper()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sharedCache := cache.NewSharedCacheWithRegisterer(nil)
	metrics := source.NewMetricsWithRegisterer("test_seen_tx_hashes", nil)

	peer, err := NewPeer(context.Background(), log, "http://localhost:8545", nil, sharedCache, nil, metrics, time.Second)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}

	return peer
}

func TestIsNewTransaction_DedupesRepeatedHash(t *testing.T) {
	p := newTestPeerForSeenHashes(t)

	if !p.isNewTransaction("0xabc") {
		t.Fatal("expected the first observation of a hash to be reported as new")
	}

	if p.isNewTransaction("0xabc") {
		t.Fatal("expected a repeated observation of the same hash to be reported as not new")
	}
}

func TestIsNewTransaction_EntriesExpireIndividually(t *testing.T) {
	p := newTestPeerForSeenHashes(t)

	// Swap in a short-TTL cache so the test doesn't need to wait 5 minutes.
	p.seenTxHashes = ttlcache.New(
		ttlcache.WithTTL[string, struct{}](50 * time.Millisecond),
	)
	go p.seenTxHashes.Start()

	defer p.seenTxHashes.Stop()

	if !p.isNewTransaction("0xexpiring") {
		t.Fatal("expected the first observation to be new")
	}

	time.Sleep(100 * time.Millisecond)

	// The old implementation had no way to expire a single entry: it only
	// ever cleared everything at once at a size threshold. With a
	// per-entry TTL, a hash must be treated as new again once it actually
	// expires, rather than staying wrongly "seen" forever between wipes.
	if !p.isNewTransaction("0xexpiring") {
		t.Fatal("expected the expired hash to be treated as new again")
	}
}

func TestIsNewTransaction_CapacityBound(t *testing.T) {
	p := newTestPeerForSeenHashes(t)

	for i := 0; i < seenTxHashesCapacity+1000; i++ {
		p.isNewTransaction(fmt.Sprintf("0x%d", i))
	}

	if got := p.seenTxHashes.Len(); got > seenTxHashesCapacity {
		t.Fatalf("expected the seen-hash cache to stay within capacity %d, got %d entries", seenTxHashesCapacity, got)
	}
}
