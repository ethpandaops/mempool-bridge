package rpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
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

// Peer represents an HTTP RPC connection that polls txpool_content
type Peer struct {
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

	// local state for tracking seen transactions (for this peer only)
	seenTxHashes map[string]bool
	mu           sync.RWMutex

	// track if initial pool load is complete
	initialLoadComplete bool

	// done channel for shutdown
	done chan struct{}
}

// TxPoolContent represents the response from txpool_content RPC call
type TxPoolContent struct {
	Pending map[string]map[string]*types.Transaction `json:"pending"`
	Queued  map[string]map[string]*types.Transaction `json:"queued"`
}

// NewPeer creates a new HTTP polling peer for sourcing transactions
func NewPeer(
	_ context.Context,
	log logrus.FieldLogger,
	rpcEndpoint string,
	handler func(ctx context.Context, transactions *mimicry.Transactions) error,
	sharedCache *cache.SharedCache,
	txFilterConfig *source.TransactionFilterConfig,
	metrics *source.Metrics,
	pollInterval time.Duration,
) (*Peer, error) {
	return &Peer{
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
func (p *Peer) Start(ctx context.Context) (<-chan error, error) {
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
func (p *Peer) pollLoop(ctx context.Context, _ chan<- error) error {
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
func (p *Peer) pollTxPool(ctx context.Context) error {
	pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var content TxPoolContent

	// Call txpool_content RPC method
	if err := p.rpcClient.CallContext(pollCtx, &content, "txpool_content"); err != nil {
		return err
	}

	// Extract all transaction hashes from pending and queued
	allTxHashes := make([]string, 0)
	txCount := 0
	newTxCount := 0

	// Process pending transactions
	for _, txsByNonce := range content.Pending {
		for _, tx := range txsByNonce {
			txCount++
			txHash := tx.Hash().String()
			if p.isNewTransaction(txHash) {
				allTxHashes = append(allTxHashes, txHash)
				newTxCount++
			}
		}
	}

	// Process queued transactions
	for _, txsByNonce := range content.Queued {
		for _, tx := range txsByNonce {
			txCount++
			txHash := tx.Hash().String()
			if p.isNewTransaction(txHash) {
				allTxHashes = append(allTxHashes, txHash)
				newTxCount++
			}
		}
	}

	p.mu.Lock()
	isInitialLoad := !p.initialLoadComplete
	if isInitialLoad {
		p.initialLoadComplete = true
	}
	p.mu.Unlock()

	if isInitialLoad {
		// On initial load, just populate the cache without sending to targets
		return p.populateCacheOnly(ctx, allTxHashes)
	}

	p.log.WithFields(logrus.Fields{
		"total_in_pool": txCount,
		"new_txs":       newTxCount,
		"already_seen":  txCount - newTxCount,
	}).Debug("polled txpool_content")

	if len(allTxHashes) == 0 {
		return nil
	}

	// After initial load, fetch and process new transactions
	return p.fetchAndProcessTransactions(ctx, allTxHashes)
}

// isNewTransaction checks if we've seen this transaction before and marks it as seen
func (p *Peer) isNewTransaction(txHash string) bool {
	// Check sent cache first (prevents duplicate sends across all peers)
	if exists := p.sharedCache.Sent.Transaction.Get(txHash); exists != nil {
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
func (p *Peer) processNewTransactions(ctx context.Context, transactions []*types.Transaction) error {
	newTxs := mimicry.Transactions{}

	for _, tx := range transactions {
		txHash := tx.Hash().String()

		// Check if transaction was already sent to targets using sent cache
		if exists := p.sharedCache.Sent.Transaction.Get(txHash); exists != nil {
			p.log.WithField("tx_hash", txHash).Debug("transaction already sent to targets, skipping")
			continue
		}

		// Track transaction type in metrics
		p.metrics.IncReceivedTxCount(tx.Type())

		// Get transaction type name
		txTypeName := getTxTypeName(tx.Type())

		logLevel := p.log.WithFields(logrus.Fields{
			"tx_hash":      txHash,
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
			// Don't log here - let pollLoop handle logging
			return err
		}

		// Mark transactions as sent in the sent cache
		for _, tx := range newTxs {
			p.sharedCache.Sent.Transaction.Set(tx.Hash().String(), time.Now(), ttlcache.DefaultTTL)
		}
	}

	// Cleanup old seen hashes periodically (keep last 10000)
	p.cleanupSeenHashes()

	return nil
}

// cleanupSeenHashes removes old entries from the seen map to prevent unbounded growth
func (p *Peer) cleanupSeenHashes() {
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
func (p *Peer) filterTransaction(_ context.Context, transaction *types.Transaction) (bool, error) {
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

// populateCacheOnly fetches transactions and adds them to cache without forwarding to targets
// This is used on initial connection to populate the cache with existing pool transactions
//
//nolint:unparam // Best-effort cache population, error always nil by design
func (p *Peer) populateCacheOnly(_ context.Context, txHashes []string) error {
	if len(txHashes) == 0 {
		return nil
	}

	// Just mark all hashes as seen in sent cache so we don't send them to targets
	for _, txHash := range txHashes {
		p.sharedCache.Sent.Transaction.Set(txHash, time.Now(), ttlcache.DefaultTTL)
	}

	p.log.WithField("total_hashes", len(txHashes)).Info("initial pool load - marked hashes as seen, NOT sending to targets")

	return nil
}

// fetchAndProcessTransactions fetches full transaction data and processes them for forwarding
func (p *Peer) fetchAndProcessTransactions(ctx context.Context, txHashes []string) error {
	if len(txHashes) == 0 {
		return nil
	}

	// Fetch transactions concurrently to avoid delays
	type fetchResult struct {
		tx          *types.Transaction
		notPending  bool
		err         error
		txHash      string
	}

	results := make(chan fetchResult, len(txHashes))
	concurrency := 50 // Limit concurrent requests

	sem := make(chan struct{}, concurrency)

	for _, txHash := range txHashes {
		go func(txHash string) {
			sem <- struct{}{}        // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			// Create a fresh context for fetching
			fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()

			// Fetch transaction via RPC using eth_getTransactionByHash
			hash := common.HexToHash(txHash)
			tx, pending, err := p.ethClient.TransactionByHash(fetchCtx, hash)

			results <- fetchResult{
				tx:         tx,
				notPending: !pending,
				err:        err,
				txHash:     txHash,
			}
		}(txHash)
	}

	// Collect results
	transactions := make([]*types.Transaction, 0, len(txHashes))
	fetchedCount := 0
	notPendingCount := 0
	errorCount := 0

	for i := 0; i < len(txHashes); i++ {
		result := <-results

		if result.err != nil {
			errorCount++
			// "not found" errors are expected when tx was mined/dropped between polls
			if result.err.Error() == "not found" {
				p.log.WithField("tx_hash", result.txHash).Trace("transaction no longer in mempool (mined or dropped)")
			} else {
				p.log.WithFields(logrus.Fields{
					"tx_hash":    result.txHash,
					"error":      result.err.Error(),
					"error_type": fmt.Sprintf("%T", result.err),
				}).Debug("failed to fetch transaction")
			}
			continue
		}

		if result.notPending {
			notPendingCount++
			continue
		}

		fetchedCount++
		transactions = append(transactions, result.tx)
	}

	// Count transactions by type
	txByType := make(map[string]int)
	for _, tx := range transactions {
		txTypeName := getTxTypeName(tx.Type())
		txByType[txTypeName]++
	}

	p.log.WithFields(logrus.Fields{
		"total_hashes": len(txHashes),
		"fetched":      fetchedCount,
		"not_pending":  notPendingCount,
		"errors":       errorCount,
		"to_process":   len(transactions),
	}).Debug("fetched transactions from poll")

	if len(transactions) > 0 {
		// Log new transactions found with type breakdown
		logFields := logrus.Fields{"total": len(transactions)}
		for txType, count := range txByType {
			logFields[txType] = count
		}
		p.log.WithFields(logFields).Info("new transactions found from poll")

		return p.processNewTransactions(ctx, transactions)
	}

	return nil
}

// Stop stops the HTTP polling peer
func (p *Peer) Stop(_ context.Context) error {
	close(p.done)

	if p.ethClient != nil {
		p.ethClient.Close()
	}

	if p.rpcClient != nil {
		p.rpcClient.Close()
	}

	return nil
}

// getTxTypeName returns a human-readable transaction type name
func getTxTypeName(txType uint8) string {
	switch txType {
	case 0x00:
		return "legacy"
	case 0x01:
		return "access_list"
	case 0x02:
		return "dynamic_fee"
	case 0x03:
		return "blob"
	case 0x04:
		return "set_code"
	default:
		return fmt.Sprintf("unknown_0x%02x", txType)
	}
}
