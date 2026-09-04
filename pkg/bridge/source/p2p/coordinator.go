// Package p2p provides P2P-based transaction source functionality.
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

var (
	// ErrConfigRequired is returned when config is nil
	ErrConfigRequired = errors.New("config is required")
	// ErrPeerDisconnected is returned when peer disconnects
	ErrPeerDisconnected = errors.New("disconnected from peer")
	// ErrCoordinatorStopped is returned when the coordinator is shutting down
	ErrCoordinatorStopped = errors.New("coordinator stopped")
)

// Coordinator manages multiple P2P source peers.
type Coordinator struct {
	config *source.Config

	broadcast func(ctx context.Context, transactions *mimicry.Transactions) error

	log logrus.FieldLogger

	cache *cache.SharedCache
	peers *map[string]bool

	metrics *source.Metrics

	mu sync.Mutex

	// stopCtx is cancelled by Stop to unblock every per-peer goroutine and
	// abort any in-progress retry wait; wg tracks those goroutines so Stop
	// can wait for their cleanup to actually finish before returning.
	stopCtx    context.Context
	stopCancel context.CancelFunc
	wg         sync.WaitGroup
}

// CoordinatorStatus tracks peer connection status.
type CoordinatorStatus struct {
	ConnectedPeers    int
	DisconnectedPeers int
}

// NewCoordinator creates a new P2P coordinator.
func NewCoordinator(config *source.Config, broadcast func(ctx context.Context, transactions *mimicry.Transactions) error, log logrus.FieldLogger) (*Coordinator, error) {
	if config == nil {
		return nil, ErrConfigRequired
	}

	if err := config.Validate("p2p"); err != nil {
		return nil, err
	}

	stopCtx, stopCancel := context.WithCancel(context.Background())

	return &Coordinator{
		config:     config,
		broadcast:  broadcast,
		log:        log.WithField("component", "source-p2p"),
		cache:      cache.NewSharedCache(),
		peers:      &map[string]bool{},
		metrics:    source.NewMetrics("mempool_bridge_source"),
		stopCtx:    stopCtx,
		stopCancel: stopCancel,
	}, nil
}

// Start starts all configured P2P source peers.
func (c *Coordinator) Start(ctx context.Context) error {
	for _, nodeRecord := range c.config.NodeRecords {
		c.mu.Lock()
		(*c.peers)[nodeRecord] = false
		c.mu.Unlock()

		c.wg.Add(1)

		go func(record string, peers *map[string]bool) {
			defer c.wg.Done()

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
							// Use a fresh context rather than ctx above: by
							// the time this runs during a real shutdown,
							// ctx is already cancelled, which must not be
							// allowed to cut short peer.Stop's own cleanup
							// (its batch processors' drain-wait selects on
							// this context too).
							if err = peer.Stop(context.Background()); err != nil {
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

					select {
					case response := <-disconnect:
						return response
					case <-c.stopCtx.Done():
						return retry.Unrecoverable(ErrCoordinatorStopped)
					}
				},
				retry.Attempts(0),
				retry.Context(c.stopCtx),
				retry.DelayType(func(_ uint, err error, _ *retry.Config) time.Duration {
					c.log.WithError(err).Debug("peer failed")

					return c.config.RetryInterval
				}),
			)
		}(nodeRecord, c.peers)
	}

	return c.startCrons(ctx)
}

// Stop stops the coordinator and all peers, waiting for each peer's cleanup
// to finish or for ctx to expire, whichever comes first.
func (c *Coordinator) Stop(ctx context.Context) error {
	c.stopCancel()

	done := make(chan struct{})

	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

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
