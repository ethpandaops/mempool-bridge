package p2p

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/go-co-op/gocron"
	"github.com/sirupsen/logrus"
)

type Coordinator struct {
	config *source.Config

	broadcast func(ctx context.Context, transactions *mimicry.Transactions) error

	log logrus.FieldLogger

	cache *cache.SharedCache
	peers *map[string]bool

	metrics *source.Metrics

	mu sync.Mutex
}

type CoordinatorStatus struct {
	ConnectedPeers    int
	DisconnectedPeers int
}

func NewCoordinator(config *source.Config, broadcast func(ctx context.Context, transactions *mimicry.Transactions) error, log logrus.FieldLogger) (*Coordinator, error) {
	if config == nil {
		return nil, errors.New("config is required")
	}

	if err := config.Validate("p2p"); err != nil {
		return nil, err
	}

	return &Coordinator{
		config:    config,
		broadcast: broadcast,
		log:       log.WithField("component", "source-p2p"),
		cache:     cache.NewSharedCache(),
		peers:     &map[string]bool{},
		metrics:   source.NewMetrics("mempool_bridge_source"),
	}, nil
}

func (c *Coordinator) Start(ctx context.Context) error {
	for _, nodeRecord := range c.config.NodeRecords {
		c.mu.Lock()
		(*c.peers)[nodeRecord] = false
		c.mu.Unlock()

		go func(record string, peers *map[string]bool) {
			_ = retry.Do(
				func() error {
					peer, err := NewPeer(ctx, c.log, record, c.broadcast, c.cache, &c.config.TransactionFilters, c.metrics)
					if err != nil {
						return err
					}

					defer func() {
						c.mu.Lock()
						(*peers)[record] = false
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
					(*peers)[record] = true
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

	if c.peers == nil {
		return CoordinatorStatus{
			ConnectedPeers:    0,
			DisconnectedPeers: 0,
		}
	}

	connectedPeers := 0

	for _, connected := range *c.peers {
		if connected {
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
		c.log.WithFields(logrus.Fields{
			"connected_peers":    status.ConnectedPeers,
			"disconnected_peers": status.DisconnectedPeers,
		}).Info("source peer summary")
	}); err != nil {
		return err
	}

	cr.StartAsync()

	return nil
}
