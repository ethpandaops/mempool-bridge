package cache

import (
	"context"
	"testing"
	"time"
)

func TestNewSentCache(t *testing.T) {
	cache := NewSentCacheWithRegisterer(nil)

	if cache == nil {
		t.Fatal("NewSentCache() returned nil")
	}

	if cache.Transaction == nil {
		t.Error("Transaction cache is nil")
	}

	if cache.metrics == nil {
		t.Error("metrics is nil")
	}
}

func TestSentCache_StartStop(t *testing.T) {
	cache := NewSentCacheWithRegisterer(nil)
	ctx := context.Background()

	// Test Start
	err := cache.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Give cache time to start
	time.Sleep(100 * time.Millisecond)

	// Test Stop
	cache.Stop()

	// Give cache time to stop
	time.Sleep(100 * time.Millisecond)
}

func TestSentCache_SetAndGet(t *testing.T) {
	cache := NewSentCacheWithRegisterer(nil)
	ctx := context.Background()

	err := cache.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer cache.Stop()

	// Set a transaction hash
	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	sentTime := time.Now()

	cache.Transaction.Set(txHash, sentTime, time.Minute)

	// Get the transaction hash
	item := cache.Transaction.Get(txHash)
	if item == nil {
		t.Error("Get() returned nil for existing item")
		return
	}

	retrievedTime := item.Value()
	if retrievedTime.Unix() != sentTime.Unix() {
		t.Errorf("Get() time mismatch: got %v, want %v", retrievedTime, sentTime)
	}
}

func TestSentCache_Expiration(t *testing.T) {
	cache := NewSentCacheWithRegisterer(nil)
	ctx := context.Background()

	err := cache.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer cache.Stop()

	// Set a transaction hash with short TTL
	txHash := "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	sentTime := time.Now()

	// Set with 100ms TTL
	cache.Transaction.Set(txHash, sentTime, 100*time.Millisecond)

	// Verify it exists
	item := cache.Transaction.Get(txHash)
	if item == nil {
		t.Error("Get() returned nil immediately after Set()")
	}

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	// Verify it's expired
	item = cache.Transaction.Get(txHash)
	if item != nil {
		t.Error("Get() should return nil for expired item")
	}
}

func TestSentCache_MultipleTransactions(t *testing.T) {
	cache := NewSentCacheWithRegisterer(nil)
	ctx := context.Background()

	err := cache.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer cache.Stop()

	// Set multiple transaction hashes
	txHashes := []string{
		"0x1111111111111111111111111111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222222222222222222222222222",
		"0x3333333333333333333333333333333333333333333333333333333333333333",
	}

	now := time.Now()
	for i, hash := range txHashes {
		cache.Transaction.Set(hash, now.Add(time.Duration(i)*time.Second), time.Minute)
	}

	// Verify all are present
	for _, hash := range txHashes {
		item := cache.Transaction.Get(hash)
		if item == nil {
			t.Errorf("Get() returned nil for hash %s", hash)
		}
	}

	// Verify metrics
	metrics := cache.Transaction.Metrics()
	if metrics.Insertions != uint64(len(txHashes)) {
		t.Errorf("Insertions = %d, want %d", metrics.Insertions, len(txHashes))
	}
}

func TestSentCache_DuplicateCheck(t *testing.T) {
	cache := NewSentCacheWithRegisterer(nil)
	ctx := context.Background()

	err := cache.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer cache.Stop()

	txHash := "0x4444444444444444444444444444444444444444444444444444444444444444"

	// First check - should not exist
	item := cache.Transaction.Get(txHash)
	if item != nil {
		t.Error("Get() should return nil for non-existent item")
	}

	// Add the transaction
	cache.Transaction.Set(txHash, time.Now(), time.Minute)

	// Second check - should exist
	item = cache.Transaction.Get(txHash)
	if item == nil {
		t.Error("Get() should return item after Set()")
	}

	// Third check - should still exist (duplicate check)
	item = cache.Transaction.Get(txHash)
	if item == nil {
		t.Error("Get() should return item on duplicate check")
	}

	metrics := cache.Transaction.Metrics()
	if metrics.Hits < 2 {
		t.Errorf("Expected at least 2 hits for duplicate checks, got %d", metrics.Hits)
	}
}
