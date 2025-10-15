// Package rpc provides RPC-based transaction source functionality.
package rpc

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

var (
	// ErrConfigRequired is returned when config is nil
	ErrConfigRequired = errors.New("config is required")
	// ErrRPCEndpointsRequired is returned when rpcEndpoints is empty
	ErrRPCEndpointsRequired = errors.New("rpcEndpoints is required")
)

// Coordinator manages multiple RPC peer connections for sourcing transactions
type Coordinator struct {
	config *source.Config

	broadcast func(ctx context.Context, transactions *mimicry.Transactions) error

	log logrus.FieldLogger

	cache *cache.SharedCache
	peers *map[string]bool

	metrics *source.Metrics

	mu sync.Mutex
}

// CoordinatorStatus represents the status of the RPC coordinator
type CoordinatorStatus struct {
	ConnectedPeers    int
	DisconnectedPeers int
}

// NewCoordinator creates a new RPC source coordinator
func NewCoordinator(config *source.Config, broadcast func(ctx context.Context, transactions *mimicry.Transactions) error, log logrus.FieldLogger) (*Coordinator, error) {
	if config == nil {
		return nil, ErrConfigRequired
	}

	if len(config.RPCEndpoints) == 0 {
		return nil, ErrRPCEndpointsRequired
	}

	return &Coordinator{
		config:    config,
		broadcast: broadcast,
		log:       log.WithField("component", "source-rpc"),
		cache:     cache.NewSharedCache(),
		peers:     &map[string]bool{},
		metrics:   source.NewMetrics("mempool_bridge_source"),
	}, nil
}

// Start begins the RPC coordinator
func (c *Coordinator) Start(ctx context.Context) error {
	// Start shared cache
	if err := c.cache.Start(ctx); err != nil {
		return err
	}

	// Start each endpoint using polling mode (txpool_content)
	for _, rpcEndpoint := range c.config.RPCEndpoints {
		c.mu.Lock()
		(*c.peers)[rpcEndpoint] = false
		c.mu.Unlock()

		c.startPollingPeer(ctx, rpcEndpoint)
	}

	return c.startCrons(ctx)
}

// startPollingPeer starts an HTTP polling peer
func (c *Coordinator) startPollingPeer(ctx context.Context, endpoint string) {
	go func(endpoint string, peers *map[string]bool) {
		_ = retry.Do(
			func() error {
				peer, err := NewPeer(
					ctx,
					c.log,
					endpoint,
					c.broadcast,
					c.cache,
					&c.config.TransactionFilters,
					c.metrics,
					c.config.PollingInterval,
				)
				if err != nil {
					return err
				}

				defer func() {
					c.mu.Lock()
					(*peers)[endpoint] = false
					c.mu.Unlock()

					if peer != nil {
						if err = peer.Stop(ctx); err != nil {
							c.log.WithError(err).Warn("failed to stop polling peer")
						}
					}
				}()

				disconnect, err := peer.Start(ctx)
				if err != nil {
					return err
				}

				c.mu.Lock()
				(*peers)[endpoint] = true
				c.mu.Unlock()

				// Wait for disconnect signal (only if Start succeeded)
				if disconnect != nil {
					response := <-disconnect
					return response
				}

				return nil
			},
			retry.Attempts(0),
			retry.DelayType(func(_ uint, err error, _ *retry.Config) time.Duration {
				c.log.WithError(err).Error("RPC polling peer failed, will retry")

				return c.config.RetryInterval
			}),
		)
	}(endpoint, c.peers)
}

// Stop stops the RPC coordinator
func (c *Coordinator) Stop(_ context.Context) error {
	return nil
}

// status returns the current status of the coordinator
func (c *Coordinator) status(_ context.Context) CoordinatorStatus {
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

// startCrons starts periodic status logging and metrics updates
func (c *Coordinator) startCrons(ctx context.Context) error {
	cr := gocron.NewScheduler(time.Local)

	if _, err := cr.Every("5s").Do(func() {
		status := c.status(ctx)
		c.metrics.SetPeers(status.ConnectedPeers, "connected")
		c.metrics.SetPeers(status.DisconnectedPeers, "disconnected")
	}); err != nil {
		return err
	}


	cr.StartAsync()

	return nil
}
