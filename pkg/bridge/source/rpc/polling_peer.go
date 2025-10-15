package rpc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/jellydator/ttlcache/v3"
	"github.com/sirupsen/logrus"
)

var (
	// ErrPollingPanicked is returned when polling handler panics
	ErrPollingPanicked = errors.New("polling handler panicked")
)

// PollingPeer represents an HTTP RPC connection that polls txpool_content
type PollingPeer struct {
	log logrus.FieldLogger

	rpcEndpoint    string
	handler        func(ctx context.Context, transactions *mimicry.Transactions) error
	txFilterConfig *source.TransactionFilterConfig

	rpcClient *rpc.Client
	ethClient *ethclient.Client

	// shared cache between all peers to prevent duplicate processing
	sharedCache *cache.SharedCache

	// metrics for tracking transaction types
	metrics *source.Metrics

	// polling configuration
	pollInterval time.Duration

	// local state for tracking seen transactions
	seenTxHashes map[string]bool
	mu           sync.RWMutex

	// done channel for shutdown
	done chan struct{}
}

// TxPoolContent represents the response from txpool_content RPC call
type TxPoolContent struct {
	Pending map[string]map[string]*types.Transaction `json:"pending"`
	Queued  map[string]map[string]*types.Transaction `json:"queued"`
}

// NewPollingPeer creates a new HTTP polling peer for sourcing transactions
func NewPollingPeer(
	_ context.Context,
	log logrus.FieldLogger,
	rpcEndpoint string,
	handler func(ctx context.Context, transactions *mimicry.Transactions) error,
	sharedCache *cache.SharedCache,
	txFilterConfig *source.TransactionFilterConfig,
	metrics *source.Metrics,
	pollInterval time.Duration,
) (*PollingPeer, error) {
	return &PollingPeer{
		log:            log.WithField("rpc_endpoint", rpcEndpoint).WithField("mode", "polling"),
		rpcEndpoint:    rpcEndpoint,
		handler:        handler,
		txFilterConfig: txFilterConfig,
		sharedCache:    sharedCache,
		metrics:        metrics,
		pollInterval:   pollInterval,
		seenTxHashes:   make(map[string]bool),
		done:           make(chan struct{}),
	}, nil
}

// Start begins the HTTP polling peer
func (p *PollingPeer) Start(ctx context.Context) (<-chan error, error) {
	response := make(chan error, 1)

	// Connect to RPC endpoint
	rpcClient, err := rpc.DialContext(ctx, p.rpcEndpoint)
	if err != nil {
		return nil, err
	}

	p.rpcClient = rpcClient
	p.ethClient = ethclient.NewClient(rpcClient)

	p.log.WithField("poll_interval", p.pollInterval).Info("started HTTP polling peer")

	// Start polling loop
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.log.WithField("panic", r).Error("panic in polling handler")
				response <- ErrPollingPanicked
			}
		}()

		if err := p.pollLoop(ctx, response); err != nil {
			p.log.WithError(err).Error("polling loop failed")
			response <- err
		}
	}()

	return response, nil
}

// pollLoop continuously polls txpool_content at regular intervals
func (p *PollingPeer) pollLoop(ctx context.Context, _ chan<- error) error {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Do an immediate poll on startup
	if err := p.pollTxPool(ctx); err != nil {
		p.log.WithError(err).Warn("initial poll failed")
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return nil
		case <-ticker.C:
			if err := p.pollTxPool(ctx); err != nil {
				p.log.WithError(err).Warn("poll failed, will retry")
				// Don't return error, just log and continue polling
			}
		}
	}
}

// pollTxPool fetches txpool_content and processes new transactions
func (p *PollingPeer) pollTxPool(ctx context.Context) error {
	pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var content TxPoolContent

	// Call txpool_content RPC method
	if err := p.rpcClient.CallContext(pollCtx, &content, "txpool_content"); err != nil {
		return err
	}

	// Extract all transactions from pending and queued
	allTxs := make([]*types.Transaction, 0)
	txCount := 0
	newTxCount := 0

	// Process pending transactions
	for _, txsByNonce := range content.Pending {
		for _, tx := range txsByNonce {
			txCount++
			if p.isNewTransaction(tx.Hash().String()) {
				allTxs = append(allTxs, tx)
				newTxCount++
			}
		}
	}

	// Process queued transactions
	for _, txsByNonce := range content.Queued {
		for _, tx := range txsByNonce {
			txCount++
			if p.isNewTransaction(tx.Hash().String()) {
				allTxs = append(allTxs, tx)
				newTxCount++
			}
		}
	}

	p.log.WithFields(logrus.Fields{
		"total_in_pool": txCount,
		"new_txs":       newTxCount,
		"already_seen":  txCount - newTxCount,
	}).Debug("polled txpool_content")

	if len(allTxs) == 0 {
		return nil
	}

	// Process new transactions
	return p.processNewTransactions(ctx, allTxs)
}

// isNewTransaction checks if we've seen this transaction before and marks it as seen
func (p *PollingPeer) isNewTransaction(txHash string) bool {
	// Check shared cache first (prevents duplicate processing across all peers)
	if exists := p.sharedCache.Transaction.Get(txHash); exists != nil {
		return false
	}

	// Check local seen map
	p.mu.RLock()
	_, seen := p.seenTxHashes[txHash]
	p.mu.RUnlock()

	if seen {
		return false
	}

	// Mark as seen in local map
	p.mu.Lock()
	p.seenTxHashes[txHash] = true
	p.mu.Unlock()

	return true
}

// processNewTransactions filters and forwards new transactions
func (p *PollingPeer) processNewTransactions(ctx context.Context, transactions []*types.Transaction) error {
	newTxs := mimicry.Transactions{}

	for _, tx := range transactions {
		// Add to shared cache
		p.sharedCache.Transaction.Set(tx.Hash().String(), tx, ttlcache.DefaultTTL)

		// Track transaction type in metrics
		p.metrics.IncReceivedTxCount(tx.Type())

		// Get transaction type name
		txTypeName := getTxTypeName(tx.Type())

		logLevel := p.log.WithFields(logrus.Fields{
			"tx_hash":      tx.Hash().String(),
			"tx_size":      tx.Size(),
			"tx_type":      tx.Type(),
			"tx_type_name": txTypeName,
			"gas_price":    tx.GasPrice(),
			"gas_tip_cap":  tx.GasTipCap(),
			"gas_fee_cap":  tx.GasFeeCap(),
		})

		// Highlight blob transactions
		if tx.Type() == 0x03 {
			logLevel = logLevel.WithField("blob_count", len(tx.BlobHashes()))
			logLevel.Info("BLOB TRANSACTION fetched from poll")
		} else {
			logLevel.Debug("fetched transaction from poll")
		}

		// Apply filters
		valid, err := p.filterTransaction(ctx, tx)
		if err != nil {
			p.log.WithError(err).Error("failed filtering transaction")
			continue
		}

		if valid {
			newTxs = append(newTxs, tx)
		}
	}

	if len(newTxs) > 0 {
		p.log.WithField("forwarding", len(newTxs)).Debug("forwarding polled transactions")

		if err := p.handler(ctx, &newTxs); err != nil {
			p.log.WithError(err).Error("failed handling transactions")
			return err
		}
	}

	// Cleanup old seen hashes periodically (keep last 10000)
	p.cleanupSeenHashes()

	return nil
}

// cleanupSeenHashes removes old entries from the seen map to prevent unbounded growth
func (p *PollingPeer) cleanupSeenHashes() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// If we have more than 10000 entries, clear the oldest half
	// This is a simple approach; could be improved with LRU
	if len(p.seenTxHashes) > 10000 {
		p.log.WithField("count", len(p.seenTxHashes)).Debug("cleaning up seen hashes")

		// Clear all and rely on shared cache for recent transactions
		p.seenTxHashes = make(map[string]bool)
	}
}

// filterTransaction filters transactions based on configuration
//
//nolint:nestif,unparam // Transaction filtering logic requires nested conditions, interface requires error return
func (p *PollingPeer) filterTransaction(_ context.Context, transaction *types.Transaction) (bool, error) {
	if p.txFilterConfig != nil {
		if len(p.txFilterConfig.To) > 0 {
			if transaction.To() != nil {
				to := transaction.To().String()
				found := false

				for _, filterTo := range p.txFilterConfig.To {
					if filterTo == to {
						found = true

						break
					}
				}

				if !found {
					p.log.WithField("to", to).Debug("Transaction filtered out")

					return false, nil
				}
			}
		}
	}

	return true, nil
}

// Stop stops the HTTP polling peer
func (p *PollingPeer) Stop(_ context.Context) error {
	close(p.done)

	if p.ethClient != nil {
		p.ethClient.Close()
	}

	if p.rpcClient != nil {
		p.rpcClient.Close()
	}

	return nil
}
