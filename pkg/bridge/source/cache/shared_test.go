package cache

// Regression test for the shared Transaction cache's capacity bound, added
// as defense-in-depth against unbounded growth if its eviction goroutine is
// ever not running for any reason.

import (
	"fmt"
	"testing"

	"github.com/jellydator/ttlcache/v3"
)

func TestSharedCache_TransactionCapacityBound(t *testing.T) {
	sc := NewSharedCacheWithRegisterer(nil)

	// Deliberately not calling Start(): the capacity bound must hold on the
	// Set() path itself, independent of whether the janitor is running.
	for i := 0; i < transactionCacheCapacity+1000; i++ {
		sc.Transaction.Set(fmt.Sprintf("0x%d", i), nil, ttlcache.DefaultTTL)
	}

	if got := sc.Transaction.Len(); got > transactionCacheCapacity {
		t.Fatalf("expected Transaction cache to stay within capacity %d, got %d entries", transactionCacheCapacity, got)
	}
}
