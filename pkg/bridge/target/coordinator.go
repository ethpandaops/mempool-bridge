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
		config:  config,
		log:     log.WithField("component", "target"),
		peers:   &map[string]*Peer{},
		metrics: NewMetrics("mempool_bridge_target"),
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

	if _, err := cr.Every("60s").Do(func() {
		status := c.status(ctx)
		c.log.WithFields(logrus.Fields{
			"connected_peers":    status.ConnectedPeers,
			"disconnected_peers": status.DisconnectedPeers,
		}).Info("status")
	}); err != nil {
		return err
	}

	cr.StartAsync()

	return nil
}

func (c *Coordinator) SendTransactionsToPeers(ctx context.Context, transactions *mimicry.Transactions) error {
	if len(*transactions) == 0 {
		return nil
	}

	// Count transactions by type for logging
	typeCounts := make(map[string]int)
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
		typeCounts[typeStr]++
	}

	// Create log fields
	logFields := logrus.Fields{
		"total": len(*transactions),
	}

	// Add type counts to fields
	for typeStr, count := range typeCounts {
		logFields[typeStr] = count
	}

	c.log.WithFields(logFields).Info("sending transactions to peers")

	errg, ectx := errgroup.WithContext(ctx)

	for _, peer := range *c.peers {
		if peer != nil {
			go func(p *Peer) {
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
			}(peer)
		}
	}

	return errg.Wait()
}
