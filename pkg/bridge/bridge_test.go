package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/sirupsen/logrus"
)

func TestBridge_BroadcastDeduplication(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create a simple bridge with mocked components
	b := &Bridge{
		log:       log,
		sentCache: mustCreateSentCache(t),
		metrics:   NewMetricsWithRegisterer("test", nil),
	}

	// Start the sent cache
	ctx := context.Background()
	if err := b.sentCache.Start(ctx); err != nil {
		t.Fatalf("Failed to start sentCache: %v", err)
	}
	defer b.sentCache.Stop()

	// Create test transactions
	tx1 := types.NewTransaction(0, common.HexToAddress("0x1234"), nil, 21000, nil, nil)
	tx2 := types.NewTransaction(1, common.HexToAddress("0x5678"), nil, 21000, nil, nil)
	tx3 := types.NewTransaction(2, common.HexToAddress("0xabcd"), nil, 21000, nil, nil)

	transactions := mimicry.Transactions{tx1, tx2, tx3}

	// Mock target that tracks sent transactions
	sentTxs := make([]string, 0)
	b.t = &mockTargetCoordinator{
		sendFunc: func(_ context.Context, txs *mimicry.Transactions) error {
			for _, tx := range *txs {
				sentTxs = append(sentTxs, tx.Hash().String())
			}
			return nil
		},
	}

	// First broadcast - should send all 3 transactions
	err := b.broadcast(ctx, &transactions)
	if err != nil {
		t.Fatalf("First broadcast() error = %v", err)
	}

	if len(sentTxs) != 3 {
		t.Errorf("First broadcast sent %d transactions, want 3", len(sentTxs))
	}

	// Reset sentTxs
	sentTxs = make([]string, 0)

	// Second broadcast with same transactions - should send 0 (all duplicates)
	err = b.broadcast(ctx, &transactions)
	if err != nil {
		t.Fatalf("Second broadcast() error = %v", err)
	}

	if len(sentTxs) != 0 {
		t.Errorf("Second broadcast sent %d transactions, want 0 (all should be duplicates)", len(sentTxs))
	}

	// Third broadcast with mix of new and old transactions
	tx4 := types.NewTransaction(3, common.HexToAddress("0xefef"), nil, 21000, nil, nil)
	mixedTransactions := mimicry.Transactions{tx1, tx4} // tx1 is duplicate, tx4 is new

	err = b.broadcast(ctx, &mixedTransactions)
	if err != nil {
		t.Fatalf("Third broadcast() error = %v", err)
	}

	if len(sentTxs) != 1 {
		t.Errorf("Third broadcast sent %d transactions, want 1 (only new tx)", len(sentTxs))
	}

	if len(sentTxs) > 0 && sentTxs[0] != tx4.Hash().String() {
		t.Errorf("Third broadcast sent wrong transaction: got %s, want %s", sentTxs[0], tx4.Hash().String())
	}
}

func TestBridge_BroadcastNilTransactions(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	b := &Bridge{
		log:       log,
		sentCache: mustCreateSentCache(t),
		metrics:   NewMetricsWithRegisterer("test", nil),
	}

	ctx := context.Background()
	if err := b.sentCache.Start(ctx); err != nil {
		t.Fatalf("Failed to start sentCache: %v", err)
	}
	defer b.sentCache.Stop()

	// Test nil transactions
	err := b.broadcast(ctx, nil)
	if err != nil {
		t.Errorf("broadcast(nil) error = %v, want nil", err)
	}
}

func TestBridge_BroadcastEmptyTransactions(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	b := &Bridge{
		log:       log,
		sentCache: mustCreateSentCache(t),
		metrics:   NewMetricsWithRegisterer("test", nil),
	}

	ctx := context.Background()
	if err := b.sentCache.Start(ctx); err != nil {
		t.Fatalf("Failed to start sentCache: %v", err)
	}
	defer b.sentCache.Stop()

	// Test empty transactions slice
	emptyTxs := mimicry.Transactions{}
	err := b.broadcast(ctx, &emptyTxs)
	if err != nil {
		t.Errorf("broadcast(empty) error = %v, want nil", err)
	}
}

func TestBridge_SentCacheExpiration(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	b := &Bridge{
		log:       log,
		sentCache: mustCreateSentCache(t),
		metrics:   NewMetricsWithRegisterer("test", nil),
	}

	ctx := context.Background()
	if err := b.sentCache.Start(ctx); err != nil {
		t.Fatalf("Failed to start sentCache: %v", err)
	}
	defer b.sentCache.Stop()

	tx := types.NewTransaction(0, common.HexToAddress("0x1234"), nil, 21000, nil, nil)
	transactions := mimicry.Transactions{tx}

	sentCount := 0
	b.t = &mockTargetCoordinator{
		sendFunc: func(_ context.Context, txs *mimicry.Transactions) error {
			sentCount += len(*txs)
			return nil
		},
	}

	// First broadcast
	err := b.broadcast(ctx, &transactions)
	if err != nil {
		t.Fatalf("First broadcast() error = %v", err)
	}

	if sentCount != 1 {
		t.Errorf("First broadcast sent %d transactions, want 1", sentCount)
	}

	// Manually set a short TTL for testing
	b.sentCache.Transaction.Set(tx.Hash().String(), time.Now(), 100*time.Millisecond)

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	// Reset counter
	sentCount = 0

	// Broadcast again after expiration - should send again
	err = b.broadcast(ctx, &transactions)
	if err != nil {
		t.Fatalf("Second broadcast() error = %v", err)
	}

	if sentCount != 1 {
		t.Errorf("Second broadcast after expiration sent %d transactions, want 1", sentCount)
	}
}

// Mock target coordinator for testing
type mockTargetCoordinator struct {
	sendFunc func(ctx context.Context, transactions *mimicry.Transactions) error
}

func (m *mockTargetCoordinator) Start(_ context.Context) error {
	return nil
}

func (m *mockTargetCoordinator) Stop(_ context.Context) error {
	return nil
}

func (m *mockTargetCoordinator) SendTransactionsToPeers(ctx context.Context, transactions *mimicry.Transactions) error {
	if m.sendFunc != nil {
		return m.sendFunc(ctx, transactions)
	}
	return nil
}

// Helper to create sent cache with error handling
func mustCreateSentCache(t *testing.T) *cache.SentCache {
	t.Helper()
	return cache.NewSentCacheWithRegisterer(nil)
}
