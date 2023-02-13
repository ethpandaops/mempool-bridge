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

		if p.txFilterConfig.From != nil && len(p.txFilterConfig.From) > 0 {
			from, err := p.signer.Sender(transaction)
			if err != nil {
				p.log.WithError(err).Error("failed to get sender")
				return false, err
			}

			found := false

			for _, filterFrom := range p.txFilterConfig.From {
				if filterFrom == from.String() {
					found = true
					break
				}
			}

			if !found {
				p.log.WithField("from", from.String()).Debug("Transaction filtered out")
				return false, nil
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

func (p *Peer) ExportTransactions(ctx context.Context, items []*common.Hash) error {
	go func() {
		hashes := make([]common.Hash, len(items))

		for i, item := range items {
			exists := p.sharedCache.Transaction.Get(item.String())
			if exists == nil {
				hashes[i] = *item
			}
		}

		txs, err := p.client.GetPooledTransactions(ctx, hashes)
		if err != nil {
			p.log.WithError(err).Error("Failed to get pooled transactions")
			return
		}

		if txs != nil {
			newTxs := mimicry.Transactions{}

			for _, tx := range txs.PooledTransactionsPacket {
				exists := p.sharedCache.Transaction.Get(tx.Hash().String())
				if exists == nil {
					p.sharedCache.Transaction.Set(tx.Hash().String(), tx, ttlcache.DefaultTTL)

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

func (p *Peer) processTransaction(ctx context.Context, hash common.Hash) error {
	// check if transaction is already in the shared cache, no need to fetch it again
	exists := p.sharedCache.Transaction.Get(hash.String())
	if exists == nil {
		item := hash
		p.txProc.Write(&item)
	}

	return nil
}
