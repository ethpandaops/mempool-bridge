package source

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/savid/ttlcache/v3"
	"github.com/sirupsen/logrus"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
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

	txProc *processor.BatchItemProcessor[common.Hash]

	chainConfig *params.ChainConfig
	signer      types.Signer
}

func NewPeer(ctx context.Context, log logrus.FieldLogger, nodeRecord string, handler func(ctx context.Context, transactions *mimicry.Transactions) error, sharedCache *cache.SharedCache, txFilterConfig *TransactionFilterConfig) (*Peer, error) {
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
	}, nil
}

func (p *Peer) Start(ctx context.Context) (<-chan error, error) {
	response := make(chan error)

	exporter, err := NewTransactionExporter(p.log, p.ExportTransactions)
	if err != nil {
		return nil, err
	}

	p.txProc = processor.NewBatchItemProcessor[common.Hash](exporter,
		p.log,
		processor.WithMaxQueueSize(100000),
		processor.WithBatchTimeout(1*time.Second),
		processor.WithExportTimeout(1*time.Second),
		// TODO: technically this should actually be 256 and throttle requests to 1 per second(?)
		// https://github.com/ethereum/devp2p/blob/master/caps/eth.md#getpooledtransactions-0x09
		// I think we can get away with much higher as long as it doesn't go above the
		// max client message size.
		processor.WithMaxExportBatchSize(50000),
	)

	p.duplicateCache.Start()

	p.client.OnStatus(ctx, func(ctx context.Context, status *mimicry.Status) error {
		// setup signer and chain config for working out transaction "from" addresses
		p.chainConfig = params.AllEthashProtocolChanges
		chainID := new(big.Int).SetUint64(status.NetworkID)
		p.chainConfig.ChainID = chainID
		p.chainConfig.EIP155Block = big.NewInt(0)
		p.signer = types.MakeSigner(p.chainConfig, big.NewInt(0))

		return nil
	})

	p.client.OnNewPooledTransactionHashes(ctx, func(ctx context.Context, hashes *mimicry.NewPooledTransactionHashes) error {
		if hashes != nil {
			for _, hash := range *hashes {
				if errT := p.processTransaction(ctx, hash); errT != nil {
					p.log.WithError(errT).Error("failed processing event")
				}
			}
		}

		return nil
	})

	p.client.OnNewPooledTransactionHashes68(ctx, func(ctx context.Context, hashes *mimicry.NewPooledTransactionHashes68) error {
		if hashes != nil {
			// TODO: handle eth68+ transaction size/types as well
			for _, hash := range hashes.Transactions {
				if errT := p.processTransaction(ctx, hash); errT != nil {
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

	if err := p.txProc.Shutdown(ctx); err != nil {
		return err
	}

	if p.client != nil {
		if err := p.client.Stop(ctx); err != nil {
			return err
		}
	}

	return nil
}
