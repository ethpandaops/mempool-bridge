package rpc

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/ethpandaops/mempool-bridge/pkg/processor"
	"github.com/jellydator/ttlcache/v3"
	"github.com/sirupsen/logrus"
)

// Peer represents an RPC connection to an Ethereum node for sourcing mempool transactions
type Peer struct {
	log logrus.FieldLogger

	rpcEndpoint    string
	handler        func(ctx context.Context, transactions *mimicry.Transactions) error
	txFilterConfig *source.TransactionFilterConfig

	rpcClient *rpc.Client
	ethClient *ethclient.Client

	// don't send duplicate events from the same client
	duplicateCache *cache.DuplicateCache
	// shared cache between clients
	sharedCache *cache.SharedCache

	// Queue for normal transactions (non-blob)
	normalTxProc *processor.BatchItemProcessor[common.Hash]
	// Queue for blob transactions (prioritized)
	blobTxProc *processor.BatchItemProcessor[common.Hash]

	// metrics for tracking transaction types
	metrics *source.Metrics

	// done channel for shutdown
	done chan struct{}
}

// TransactionExporter handles exporting transaction hashes
type TransactionExporter struct {
	log     logrus.FieldLogger
	handler func(ctx context.Context, items []*common.Hash) error
}

// NewTransactionExporter creates a new transaction exporter
func NewTransactionExporter(log logrus.FieldLogger, handler func(ctx context.Context, items []*common.Hash) error) (TransactionExporter, error) {
	return TransactionExporter{
		log:     log,
		handler: handler,
	}, nil
}

// ExportItems exports transaction hashes.
func (t TransactionExporter) ExportItems(ctx context.Context, items []*common.Hash) error {
	return t.handler(ctx, items)
}

// Shutdown handles cleanup for the exporter.
func (t TransactionExporter) Shutdown(_ context.Context) error {
	return nil
}

// NewPeer creates a new RPC peer for sourcing transactions
func NewPeer(_ context.Context, log logrus.FieldLogger, rpcEndpoint string, handler func(ctx context.Context, transactions *mimicry.Transactions) error, sharedCache *cache.SharedCache, txFilterConfig *source.TransactionFilterConfig, metrics *source.Metrics) (*Peer, error) {
	duplicateCache := cache.NewDuplicateCache()

	return &Peer{
		log:            log.WithField("rpc_endpoint", rpcEndpoint),
		rpcEndpoint:    rpcEndpoint,
		handler:        handler,
		txFilterConfig: txFilterConfig,
		duplicateCache: duplicateCache,
		sharedCache:    sharedCache,
		metrics:        metrics,
		done:           make(chan struct{}),
	}, nil
}

// Start begins the RPC peer subscription
func (p *Peer) Start(ctx context.Context) (<-chan error, error) {
	response := make(chan error, 1)

	// Connect to RPC endpoint
	rpcClient, err := rpc.DialContext(ctx, p.rpcEndpoint)
	if err != nil {
		return nil, err
	}

	p.rpcClient = rpcClient
	p.ethClient = ethclient.NewClient(rpcClient)

	// Initialize normal transaction exporter
	normalExporter, err := NewTransactionExporter(p.log.WithField("queue", "normal"), p.ExportNormalTransactions)
	if err != nil {
		return nil, err
	}

	// Initialize blob transaction exporter - blob transactions are processed one at a time
	blobExporter, err := NewTransactionExporter(p.log.WithField("queue", "blob"), p.ExportBlobTransactions)
	if err != nil {
		return nil, err
	}

	// Configure normal transaction processor with larger batch size
	p.normalTxProc = processor.NewBatchItemProcessor(normalExporter,
		p.log.WithField("processor", "normal"),
		processor.WithMaxQueueSize(100000),
		processor.WithBatchTimeout(1*time.Second),
		processor.WithExportTimeout(1*time.Second),
		processor.WithMaxExportBatchSize(50000),
	)

	// Configure blob transaction processor with batch size 1 to process one at a time
	p.blobTxProc = processor.NewBatchItemProcessor(blobExporter,
		p.log.WithField("processor", "blob"),
		processor.WithMaxQueueSize(10000),
		processor.WithBatchTimeout(500*time.Millisecond),
		processor.WithExportTimeout(500*time.Millisecond),
		processor.WithMaxExportBatchSize(1), // Process only one blob at a time
	)

	p.duplicateCache.Start()

	p.log.Debug("attempting to subscribe to pending transactions")

	// Subscribe to pending transactions
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.log.WithField("panic", r).Error("panic in subscription handler")
				response <- ErrSubscriptionPanicked
			}
		}()

		if err := p.subscribeToPendingTransactions(ctx, response); err != nil {
			p.log.WithError(err).Error("subscription failed")
			response <- err
		}
	}()

	return response, nil
}

// subscribeToPendingTransactions subscribes to newPendingTransactions via WebSocket
func (p *Peer) subscribeToPendingTransactions(ctx context.Context, errorChan chan<- error) error {
	// Create subscription channel
	txHashChan := make(chan common.Hash, 10000)

	// Subscribe to newPendingTransactions
	sub, err := p.rpcClient.EthSubscribe(ctx, txHashChan, "newPendingTransactions")
	if err != nil {
		return err
	}

	p.log.Info("subscribed to pending transactions")

	// Handle subscription
	for {
		select {
		case <-ctx.Done():
			sub.Unsubscribe()
			return ctx.Err()
		case <-p.done:
			sub.Unsubscribe()
			return nil
		case err := <-sub.Err():
			if err != nil {
				p.log.WithError(err).Error("subscription error")
				errorChan <- err

				return err
			}
		case txHash := <-txHashChan:
			// Check if we've already seen this transaction
			exists := p.sharedCache.Transaction.Get(txHash.String())
			if exists != nil {
				continue
			}

			item := txHash
			p.log.WithField("tx_hash", txHash.String()).Debug("received new pending transaction hash")
			p.normalTxProc.Write(&item)
		}
	}
}

// Stop stops the RPC peer
func (p *Peer) Stop(ctx context.Context) error {
	close(p.done)
	p.duplicateCache.Stop()

	if err := p.normalTxProc.Shutdown(ctx); err != nil {
		return err
	}

	if err := p.blobTxProc.Shutdown(ctx); err != nil {
		return err
	}

	if p.ethClient != nil {
		p.ethClient.Close()
	}

	if p.rpcClient != nil {
		p.rpcClient.Close()
	}

	return nil
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

// ExportNormalTransactions handles non-blob transactions
func (p *Peer) ExportNormalTransactions(ctx context.Context, items []*common.Hash) error {
	go func() {
		if len(items) == 0 {
			return
		}

		p.log.WithField("count", len(items)).Debug("exporting normal transactions")

		// Filter out nil items
		validItems := make([]*common.Hash, 0, len(items))

		for _, item := range items {
			if item != nil {
				validItems = append(validItems, item)
			}
		}

		if len(validItems) == 0 {
			return
		}

		newTxs := mimicry.Transactions{}
		fetchedCount := 0
		cacheHitCount := 0
		notPendingCount := 0
		errorCount := 0

		for _, item := range validItems {
			exists := p.sharedCache.Transaction.Get(item.String())
			if exists != nil {
				cacheHitCount++
				continue
			}

			// Create a fresh context for fetching to avoid using canceled parent context
			fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

			// Fetch transaction via RPC
			tx, pending, err := p.ethClient.TransactionByHash(fetchCtx, *item)
			cancel()

			if err != nil {
				errorCount++
				// Log with more detail about the error type
				p.log.WithFields(logrus.Fields{
					"tx_hash":      item.String(),
					"error":        err.Error(),
					"error_type":   fmt.Sprintf("%T", err),
					"fetched":      fetchedCount,
					"errors":       errorCount,
					"cache_hits":   cacheHitCount,
					"not_pending":  notPendingCount,
				}).Warn("failed to fetch transaction - might be blob tx not yet available")
				continue
			}

			if !pending {
				notPendingCount++
				p.log.WithFields(logrus.Fields{
					"tx_hash":     item.String(),
					"tx_type":     tx.Type(),
					"not_pending": notPendingCount,
				}).Debug("transaction not in pending state (already mined or dropped)")
				continue
			}

			fetchedCount++

			// Add to cache
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
				logLevel.Info("🎯 BLOB TRANSACTION detected and fetched successfully")
			} else {
				logLevel.Debug("fetched and processing transaction")
			}

			valid, err := p.filterTransaction(ctx, tx)
			if err != nil {
				p.log.WithError(err).Error("failed filtering transaction")
			}

			if valid {
				newTxs = append(newTxs, tx)
			}
		}

		logEntry := p.log.WithFields(logrus.Fields{
			"total_items":   len(validItems),
			"fetched":       fetchedCount,
			"cache_hits":    cacheHitCount,
			"not_pending":   notPendingCount,
			"errors":        errorCount,
			"forwarding":    len(newTxs),
		})

		if errorCount > 0 {
			logEntry.Warn("normal transaction batch complete WITH ERRORS - check if blob txs")
		} else {
			logEntry.Debug("normal transaction batch complete")
		}

		if len(newTxs) > 0 {
			if err := p.handler(ctx, &newTxs); err != nil {
				p.log.WithError(err).Error("failed handling transactions")
			}
		}
	}()

	return nil
}

// ExportBlobTransactions handles blob transactions with priority
func (p *Peer) ExportBlobTransactions(ctx context.Context, items []*common.Hash) error {
	go func() {
		if len(items) == 0 {
			return
		}

		// Process one blob at a time
		item := items[0]
		if item == nil {
			return
		}

		exists := p.sharedCache.Transaction.Get(item.String())
		if exists != nil {
			p.log.WithField("tx_hash", item.String()).Debug("blob transaction already in cache")
			return // Already processed
		}

		// Create a fresh context for fetching to avoid using canceled parent context
		fetchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Fetch transaction via RPC
		tx, pending, err := p.ethClient.TransactionByHash(fetchCtx, *item)
		if err != nil {
			p.log.WithFields(logrus.Fields{
				"tx_hash":    item.String(),
				"error":      err.Error(),
				"error_type": fmt.Sprintf("%T", err),
			}).Warn("failed to fetch blob transaction")

			return
		}

		if !pending {
			p.log.WithFields(logrus.Fields{
				"tx_hash": item.String(),
				"tx_type": tx.Type(),
			}).Debug("blob transaction not in pending state (already mined or dropped)")

			return
		}

		// Add to cache
		p.sharedCache.Transaction.Set(tx.Hash().String(), tx, ttlcache.DefaultTTL)

		// Track transaction type in metrics
		p.metrics.IncReceivedTxCount(tx.Type())

		// Get transaction type name
		txTypeName := getTxTypeName(tx.Type())

		p.log.WithFields(logrus.Fields{
			"tx_hash":      tx.Hash().String(),
			"tx_size":      tx.Size(),
			"tx_type":      tx.Type(),
			"tx_type_name": txTypeName,
			"gas_price":    tx.GasPrice(),
			"gas_tip_cap":  tx.GasTipCap(),
			"gas_fee_cap":  tx.GasFeeCap(),
			"blob_count":   len(tx.BlobHashes()),
		}).Info("fetched blob transaction (priority)")

		valid, err := p.filterTransaction(ctx, tx)
		if err != nil {
			p.log.WithError(err).Error("failed filtering blob transaction")
		}

		if valid {
			newTxs := mimicry.Transactions{tx}
			if err := p.handler(ctx, &newTxs); err != nil {
				p.log.WithError(err).Error("failed handling blob transaction")
			} else {
				p.log.WithField("tx_hash", tx.Hash().String()).Info("blob transaction forwarded successfully")
			}
		}
	}()

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
