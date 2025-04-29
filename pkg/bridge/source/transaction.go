package source

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/savid/ttlcache/v3"
	"github.com/sirupsen/logrus"
)

func (p *Peer) filterTransaction(ctx context.Context, transaction *types.Transaction) (bool, error) {
	if p.txFilterConfig != nil {
		if p.txFilterConfig.To != nil && len(p.txFilterConfig.To) > 0 {
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

type TransactionExporter struct {
	log logrus.FieldLogger

	handler func(ctx context.Context, items []*common.Hash) error
}

func NewTransactionExporter(log logrus.FieldLogger, handler func(ctx context.Context, items []*common.Hash) error) (TransactionExporter, error) {
	return TransactionExporter{
		log:     log,
		handler: handler,
	}, nil
}

func (t TransactionExporter) ExportItems(ctx context.Context, items []*common.Hash) error {
	return t.handler(ctx, items)
}

func (t TransactionExporter) Shutdown(ctx context.Context) error {
	return nil
}

// ExportNormalTransactions handles non-blob transactions
func (p *Peer) ExportNormalTransactions(ctx context.Context, items []*common.Hash) error {
	go func() {
		if len(items) == 0 {
			return
		}

		p.log.WithField("count", len(items)).Debug("exporting normal transactions")

		hashes := make([]common.Hash, len(items))

		for i, item := range items {
			exists := p.sharedCache.Transaction.Get(item.String())
			if exists == nil {
				hashes[i] = *item
			}
		}

		txs, err := p.client.GetPooledTransactions(ctx, hashes)
		if err != nil {
			p.log.WithError(err).Error("Failed to get normal pooled transactions")
			return
		}

		if txs != nil {
			newTxs := mimicry.Transactions{}

			for _, tx := range txs.PooledTransactionsResponse {
				exists := p.sharedCache.Transaction.Get(tx.Hash().String())
				if exists == nil {
					p.sharedCache.Transaction.Set(tx.Hash().String(), tx, ttlcache.DefaultTTL)

					// Track transaction type in metrics
					p.metrics.IncReceivedTxCount(tx.Type())

					p.log.WithFields(logrus.Fields{
						"tx_hash": tx.Hash().String(),
						"tx_size": tx.Size(),
						"tx_type": tx.Type(),
					}).Debug("processing normal transaction")

					valid, err := p.filterTransaction(ctx, tx)
					if err != nil {
						p.log.WithError(err).Error("failed handling transaction")
					}

					if valid {
						newTxs = append(newTxs, tx)
					}
				}
			}

			if len(newTxs) > 0 {
				if err := p.handler(ctx, &newTxs); err != nil {
					p.log.WithError(err).Error("failed handling transactions")
				}
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
		exists := p.sharedCache.Transaction.Get(item.String())
		if exists != nil {
			return // Already processed
		}

		hash := *item
		hashes := []common.Hash{hash}

		txs, err := p.client.GetPooledTransactions(ctx, hashes)
		if err != nil {
			p.log.WithError(err).Error("Failed to get blob pooled transaction")
			return
		}

		if txs != nil && len(txs.PooledTransactionsResponse) > 0 {
			newTxs := mimicry.Transactions{}

			for _, tx := range txs.PooledTransactionsResponse {
				exists := p.sharedCache.Transaction.Get(tx.Hash().String())
				if exists == nil {
					p.sharedCache.Transaction.Set(tx.Hash().String(), tx, ttlcache.DefaultTTL)

					// Track transaction type in metrics
					p.metrics.IncReceivedTxCount(tx.Type())

					valid, err := p.filterTransaction(ctx, tx)
					if err != nil {
						p.log.WithError(err).Error("failed handling blob transaction")
					}

					if valid {
						newTxs = append(newTxs, tx)
					}
				}
			}

			if len(newTxs) > 0 {
				if err := p.handler(ctx, &newTxs); err != nil {
					p.log.WithError(err).Error("failed handling blob transaction")
				}
			}
		}
	}()

	return nil
}
