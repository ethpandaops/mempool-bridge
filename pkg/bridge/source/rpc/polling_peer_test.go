package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/sirupsen/logrus"
)

func TestNewPollingPeer(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sharedCache := cache.NewSharedCacheWithRegisterer(nil)
	metrics := source.NewMetricsWithRegisterer("test", nil)
	txFilterConfig := &source.TransactionFilterConfig{}

	peer, err := NewPollingPeer(
		context.Background(),
		log,
		"http://localhost:8545",
		func(_ context.Context, _ *mimicry.Transactions) error { return nil },
		sharedCache,
		txFilterConfig,
		metrics,
		10*time.Second,
	)

	if err != nil {
		t.Fatalf("NewPollingPeer() error = %v", err)
	}

	if peer == nil {
		t.Fatal("NewPollingPeer() returned nil")
	}

	if peer.rpcEndpoint != "http://localhost:8545" {
		t.Errorf("rpcEndpoint = %v, want %v", peer.rpcEndpoint, "http://localhost:8545")
	}

	if peer.pollInterval != 10*time.Second {
		t.Errorf("pollInterval = %v, want %v", peer.pollInterval, 10*time.Second)
	}

	if peer.seenTxHashes == nil {
		t.Error("seenTxHashes map is nil")
	}
}

func TestPollingPeer_IsNewTransaction(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sharedCache := cache.NewSharedCacheWithRegisterer(nil)
	ctx := context.Background()
	err := sharedCache.Start(ctx)
	if err != nil {
		t.Fatalf("sharedCache.Start() error = %v", err)
	}

	metrics := source.NewMetricsWithRegisterer("test", nil)
	peer, err := NewPollingPeer(
		ctx,
		log,
		"http://localhost:8545",
		func(_ context.Context, _ *mimicry.Transactions) error { return nil },
		sharedCache,
		&source.TransactionFilterConfig{},
		metrics,
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("NewPollingPeer() error = %v", err)
	}

	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	// First check - should be new
	if !peer.isNewTransaction(txHash) {
		t.Error("isNewTransaction() = false for first check, want true")
	}

	// Second check - should not be new (already in local map)
	if peer.isNewTransaction(txHash) {
		t.Error("isNewTransaction() = true for second check, want false")
	}
}

func TestPollingPeer_CleanupSeenHashes(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sharedCache := cache.NewSharedCacheWithRegisterer(nil)
	metrics := source.NewMetricsWithRegisterer("test", nil)
	peer, err := NewPollingPeer(
		context.Background(),
		log,
		"http://localhost:8545",
		func(_ context.Context, _ *mimicry.Transactions) error { return nil },
		sharedCache,
		&source.TransactionFilterConfig{},
		metrics,
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("NewPollingPeer() error = %v", err)
	}

	// Add 15000 hashes to trigger cleanup
	for i := 0; i < 15000; i++ {
		peer.seenTxHashes[string(rune(i))] = true
	}

	if len(peer.seenTxHashes) != 15000 {
		t.Errorf("seenTxHashes length = %d, want 15000", len(peer.seenTxHashes))
	}

	// Trigger cleanup
	peer.cleanupSeenHashes()

	// Should be cleared since > 10000
	if len(peer.seenTxHashes) != 0 {
		t.Errorf("seenTxHashes length after cleanup = %d, want 0", len(peer.seenTxHashes))
	}
}

func TestPollingPeer_FilterTransaction(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name           string
		filterConfig   *source.TransactionFilterConfig
		txTo           *common.Address
		expectedValid  bool
	}{
		{
			name:          "No filter - should pass",
			filterConfig:  &source.TransactionFilterConfig{},
			txTo:          mustAddress("0x1234567890123456789012345678901234567890"),
			expectedValid: true,
		},
		{
			name: "Filter with matching address - should pass",
			filterConfig: &source.TransactionFilterConfig{
				To: []string{"0x1234567890123456789012345678901234567890"},
			},
			txTo:          mustAddress("0x1234567890123456789012345678901234567890"),
			expectedValid: true,
		},
		{
			name: "Filter with non-matching address - should fail",
			filterConfig: &source.TransactionFilterConfig{
				To: []string{"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
			},
			txTo:          mustAddress("0x1234567890123456789012345678901234567890"),
			expectedValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sharedCache := cache.NewSharedCacheWithRegisterer(nil)
			metrics := source.NewMetricsWithRegisterer("test", nil)
			peer, err := NewPollingPeer(
				context.Background(),
				log,
				"http://localhost:8545",
				func(_ context.Context, _ *mimicry.Transactions) error { return nil },
				sharedCache,
				tt.filterConfig,
				metrics,
				10*time.Second,
			)
			if err != nil {
				t.Fatalf("NewPollingPeer() error = %v", err)
			}

			// Create a mock transaction
			tx := types.NewTransaction(0, *tt.txTo, nil, 21000, nil, nil)

			valid, err := peer.filterTransaction(context.Background(), tx)
			if err != nil {
				t.Errorf("filterTransaction() error = %v", err)
			}

			if valid != tt.expectedValid {
				t.Errorf("filterTransaction() = %v, want %v", valid, tt.expectedValid)
			}
		})
	}
}

func TestPollingPeer_ProcessNewTransactions(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sharedCache := cache.NewSharedCacheWithRegisterer(nil)
	ctx := context.Background()
	if err := sharedCache.Start(ctx); err != nil {
		t.Fatalf("sharedCache.Start() error = %v", err)
	}

	metrics := source.NewMetricsWithRegisterer("test", nil)

	handlerCalled := false
	var receivedTxs *mimicry.Transactions

	peer, err := NewPollingPeer(
		ctx,
		log,
		"http://localhost:8545",
		func(_ context.Context, txs *mimicry.Transactions) error {
			handlerCalled = true
			receivedTxs = txs
			return nil
		},
		sharedCache,
		&source.TransactionFilterConfig{},
		metrics,
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("NewPollingPeer() error = %v", err)
	}

	// Create test transaction
	tx := types.NewTransaction(0, common.HexToAddress("0x1234567890123456789012345678901234567890"), nil, 21000, nil, nil)
	transactions := []*types.Transaction{tx}

	// Process transactions - handler should be called
	err = peer.processNewTransactions(ctx, transactions)
	if err != nil {
		t.Fatalf("processNewTransactions() error = %v", err)
	}

	if !handlerCalled {
		t.Error("Handler should be called when processing new transactions")
	}

	if receivedTxs == nil || len(*receivedTxs) != 1 {
		t.Errorf("Expected 1 transaction to be forwarded, got %d", len(*receivedTxs))
	}
}

// Helper function to create address from string
func mustAddress(addr string) *common.Address {
	address := common.HexToAddress(addr)
	return &address
}
