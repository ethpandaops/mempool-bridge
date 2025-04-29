package source

import (
	"context"
	"errors"
	"time"

	"github.com/savid/ttlcache/v3"
	"github.com/sirupsen/logrus"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/ethpandaops/mempool-bridge/pkg/processor"
)

type Peer struct {
	log logrus.FieldLogger

	nodeRecord     string
	handler        func(ctx context.Context, transactions *mimicry.Transactions) error
	txFilterConfig *TransactionFilterConfig

	client *mimicry.Client

	// don't send duplicate events from the same client
	duplicateCache *cache.DuplicateCache
	// shared cache between clients
	sharedCache *cache.SharedCache

	// Queue for normal transactions (non-blob)
	normalTxProc *processor.BatchItemProcessor[common.Hash]
	// Queue for blob transactions (prioritized)
	blobTxProc *processor.BatchItemProcessor[common.Hash]

	// metrics for tracking transaction types
	metrics *Metrics
}

// Transaction type with hash mapping
type TransactionTypeHash struct {
	Hash common.Hash
	Type byte
}

func NewPeer(ctx context.Context, log logrus.FieldLogger, nodeRecord string, handler func(ctx context.Context, transactions *mimicry.Transactions) error, sharedCache *cache.SharedCache, txFilterConfig *TransactionFilterConfig, metrics *Metrics) (*Peer, error) {
	client, err := mimicry.New(ctx, log, nodeRecord, "mempool-bridge")
	if err != nil {
		return nil, err
	}

	duplicateCache := cache.NewDuplicateCache()

	return &Peer{
		log:            log.WithField("node_record", nodeRecord),
		nodeRecord:     nodeRecord,
		handler:        handler,
		txFilterConfig: txFilterConfig,
		client:         client,
		duplicateCache: duplicateCache,
		sharedCache:    sharedCache,
		metrics:        metrics,
	}, nil
}

func (p *Peer) Start(ctx context.Context) (<-chan error, error) {
	response := make(chan error)

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

	p.client.OnStatus(ctx, func(ctx context.Context, status *mimicry.Status) error {
		return nil
	})

	p.client.OnNewPooledTransactionHashes(ctx, func(ctx context.Context, hashes *mimicry.NewPooledTransactionHashes) error {
		if hashes != nil {
			for i, hash := range hashes.Hashes {
				t := hashes.Types[i]
				// Track the transaction type in metrics
				p.metrics.IncNewTxHashesCount(t)

				if errT := p.processTransaction(ctx, hash, t); errT != nil {
					p.log.WithError(errT).Error("failed processing event")
				}
			}
		}

		return nil
	})

	p.client.OnTransactions(ctx, func(ctx context.Context, txs *mimicry.Transactions) error {
		if txs != nil {
			newTxs := mimicry.Transactions{}

			for _, tx := range *txs {
				// only process transactions we haven't seen before across all peers
				exists := p.sharedCache.Transaction.Get(tx.Hash().String())
				if exists == nil {
					p.sharedCache.Transaction.Set(tx.Hash().String(), tx, ttlcache.DefaultTTL)

					// Track the transaction type in metrics
					p.metrics.IncReceivedTxCount(tx.Type())

					valid, errT := p.filterTransaction(ctx, tx)
					if errT != nil {
						p.log.WithError(errT).Error("failed handling transaction")
					}
					if valid {
						newTxs = append(newTxs, tx)
					}
				}
			}

			if len(newTxs) > 0 {
				if errT := p.handler(ctx, &newTxs); errT != nil {
					p.log.WithError(errT).Error("failed handling transactions")
				}
			}
		}
		return nil
	})

	p.client.OnDisconnect(ctx, func(ctx context.Context, reason *mimicry.Disconnect) error {
		str := "unknown"
		if reason != nil {
			str = reason.Reason.String()
		}

		p.log.WithFields(logrus.Fields{
			"reason": str,
		}).Debug("disconnected from client")

		response <- errors.New("disconnected from peer (reason " + str + ")")

		return nil
	})

	p.log.Debug("attempting to connect to client")

	err = p.client.Start(ctx)
	if err != nil {
		p.log.WithError(err).Debug("failed to dial client")
		return nil, err
	}

	return response, nil
}

func (p *Peer) Stop(ctx context.Context) error {
	p.duplicateCache.Stop()

	if err := p.normalTxProc.Shutdown(ctx); err != nil {
		return err
	}

	if err := p.blobTxProc.Shutdown(ctx); err != nil {
		return err
	}

	if p.client != nil {
		if err := p.client.Stop(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (p *Peer) processTransaction(ctx context.Context, hash common.Hash, txType byte) error {
	// check if transaction is already in the shared cache, no need to fetch it again
	exists := p.sharedCache.Transaction.Get(hash.String())
	if exists == nil {
		item := hash

		// Route to the appropriate queue based on type
		if txType == 0x03 { // BlobTxType
			p.log.WithField("tx_hash", hash.String()).Debug("queuing blob transaction")
			p.blobTxProc.Write(&item)
		} else {
			p.log.WithField("tx_hash", hash.String()).Debug("queuing normal transaction")
			p.normalTxProc.Write(&item)
		}
	}

	return nil
}
