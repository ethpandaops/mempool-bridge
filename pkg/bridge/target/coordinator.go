package target

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/go-co-op/gocron"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

type Coordinator struct {
	config *Config

	log logrus.FieldLogger

	peers *map[string]*Peer

	metrics *Metrics

	// Transaction summary counters for periodic logging
	txCounters     map[string]int
	txCountersLock sync.Mutex

	mu sync.Mutex
}

type CoordinatorStatus struct {
	ConnectedPeers    int
	DisconnectedPeers int
}

func NewCoordinator(config *Config, log logrus.FieldLogger) (*Coordinator, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &Coordinator{
		config:     config,
		log:        log.WithField("component", "target"),
		peers:      &map[string]*Peer{},
		metrics:    NewMetrics("mempool_bridge_target"),
		txCounters: make(map[string]int),
	}, nil
}

func (c *Coordinator) Start(ctx context.Context) error {
	for _, nodeRecord := range c.config.NodeRecords {
		c.mu.Lock()
		(*c.peers)[nodeRecord] = nil
		c.mu.Unlock()

		go func(record string, peers *map[string]*Peer) {
			_ = retry.Do(
				func() error {
					peer, err := NewPeer(ctx, c.log, record)
					if err != nil {
						return err
					}

					defer func() {
						c.mu.Lock()
						(*peers)[record] = nil
						c.mu.Unlock()

						if peer != nil {
							if err = peer.Stop(ctx); err != nil {
								c.log.WithError(err).Warn("failed to stop peer")
							}
						}
					}()

					disconnect, err := peer.Start(ctx)
					if err != nil {
						return err
					}

					c.mu.Lock()
					(*peers)[record] = peer
					c.mu.Unlock()

					response := <-disconnect

					return response
				},
				retry.Attempts(0),
				retry.DelayType(func(n uint, err error, config *retry.Config) time.Duration {
					c.log.WithError(err).Debug("peer failed")

					return c.config.RetryInterval
				}),
			)
		}(nodeRecord, c.peers)
	}

	if err := c.startCrons(ctx); err != nil {
		return err
	}

	return nil
}

func (c *Coordinator) Stop(ctx context.Context) error {
	return nil
}

func (c *Coordinator) status(ctx context.Context) CoordinatorStatus {
	c.mu.Lock()
	defer c.mu.Unlock()

	connectedPeers := 0

	for _, peer := range *c.peers {
		if peer != nil {
			connectedPeers++
		}
	}

	return CoordinatorStatus{
		ConnectedPeers:    connectedPeers,
		DisconnectedPeers: len(*c.peers) - connectedPeers,
	}
}

func (c *Coordinator) startCrons(ctx context.Context) error {
	cr := gocron.NewScheduler(time.Local)

	if _, err := cr.Every("5s").Do(func() {
		status := c.status(ctx)
		c.metrics.SetPeers(status.ConnectedPeers, "connected")
		c.metrics.SetPeers(status.DisconnectedPeers, "disconnected")
	}); err != nil {
		return err
	}

	if _, err := cr.Every("30s").Do(func() {
		status := c.status(ctx)

		// Log peer status
		c.log.WithFields(logrus.Fields{
			"connected_peers":    status.ConnectedPeers,
			"disconnected_peers": status.DisconnectedPeers,
		}).Info("target peer summary")

		// Handle transaction counters separately
		c.txCountersLock.Lock()
		txTotal := 0
		txFields := logrus.Fields{}

		if len(c.txCounters) > 0 {
			for txType, count := range c.txCounters {
				txFields[txType] = count
				txTotal += count
			}

			// Only log if there were transactions
			if txTotal > 0 {
				txFields["total"] = txTotal
				c.log.WithFields(txFields).Info("transactions sent to target peers summary")
			}

			// Reset counters after logging
			c.txCounters = make(map[string]int)
		}
		c.txCountersLock.Unlock()
	}); err != nil {
		return err
	}

	cr.StartAsync()

	return nil
}

func (c *Coordinator) SendTransactionsToPeers(ctx context.Context, transactions *mimicry.Transactions) error {
	if transactions == nil || len(*transactions) == 0 {
		return nil
	}

	// Count transactions by type for summary counters
	c.txCountersLock.Lock()
	for _, tx := range *transactions {
		txType := tx.Type()
		// Get transaction type string
		typeStr := "unknown"

		switch txType {
		case 0x00: // LegacyTxType
			typeStr = "legacy"
		case 0x01: // AccessListTxType
			typeStr = "access_list"
		case 0x02: // DynamicFeeTxType
			typeStr = "dynamic_fee"
		case 0x03: // BlobTxType
			typeStr = "blob"
		case 0x04: // SetCodeTxType
			typeStr = "set_code"
		}

		c.txCounters[typeStr] += 1
	}
	c.txCountersLock.Unlock()

	// Debug level log for detailed troubleshooting if needed
	c.log.WithField("count", len(*transactions)).Debug("sending transactions to peers")

	errg, ectx := errgroup.WithContext(ctx)

	c.mu.Lock()
	peers := *c.peers
	c.mu.Unlock()

	for _, peer := range peers {
		if peer != nil {
			p := peer // Create a copy of the loop variable to avoid race conditions

			errg.Go(func() error {
				err := p.SendTransactions(ectx, transactions)

				status := "success"
				if err != nil {
					status = "failure"
				}

				c.metrics.AddTransactions(len(*transactions), status)

				// Track individual transaction types
				for _, tx := range *transactions {
					c.metrics.AddTransactionByType(tx.Type(), status)
				}

				return err
			})
		}
	}

	return errg.Wait()
}
