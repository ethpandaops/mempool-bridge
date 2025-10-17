// Package rpc provides RPC-based transaction target functionality.
package rpc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/target"
	"github.com/go-co-op/gocron"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

var (
	// ErrConfigRequired is returned when config is nil
	ErrConfigRequired = errors.New("config is required")
	// ErrRPCEndpointsRequired is returned when rpcEndpoints is empty
	ErrRPCEndpointsRequired = errors.New("rpcEndpoints is required")
)

// Coordinator manages multiple RPC peer connections for sending transactions
type Coordinator struct {
	config *target.Config

	log logrus.FieldLogger

	peers *map[string]*Peer

	metrics *target.Metrics

	// Transaction summary counters for periodic logging
	txCounters     map[string]int
	txCountersLock sync.Mutex

	mu sync.Mutex
}

// CoordinatorStatus represents the status of the RPC coordinator
type CoordinatorStatus struct {
	ConnectedPeers    int
	DisconnectedPeers int
}

// NewCoordinator creates a new RPC target coordinator
func NewCoordinator(config *target.Config, log logrus.FieldLogger) (*Coordinator, error) {
	if config == nil {
		return nil, ErrConfigRequired
	}

	if len(config.RPCEndpoints) == 0 {
		return nil, ErrRPCEndpointsRequired
	}

	return &Coordinator{
		config:     config,
		log:        log.WithField("component", "target-rpc"),
		peers:      &map[string]*Peer{},
		metrics:    target.NewMetrics("mempool_bridge_target"),
		txCounters: make(map[string]int),
	}, nil
}

// Start begins the RPC coordinator
func (c *Coordinator) Start(ctx context.Context) error {
	for _, rpcEndpoint := range c.config.RPCEndpoints {
		c.mu.Lock()
		(*c.peers)[rpcEndpoint] = nil
		c.mu.Unlock()

		go func(endpoint string, peers *map[string]*Peer) {
			_ = retry.Do(
				func() error {
					peer, err := NewPeer(ctx, c.log, endpoint, c.config.SendConcurrency)
					if err != nil {
						return err
					}

					defer func() {
						c.mu.Lock()
						(*peers)[endpoint] = nil
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
					(*peers)[endpoint] = peer
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
					c.log.WithError(err).Error("target RPC peer failed, will retry")

					return c.config.RetryInterval
				}),
			)
		}(rpcEndpoint, c.peers)
	}

	return c.startCrons(ctx)
}

// Stop stops the RPC coordinator
func (c *Coordinator) Stop(_ context.Context) error {
	return nil
}

// status returns the current status of the coordinator
func (c *Coordinator) status(_ context.Context) CoordinatorStatus {
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

	// Log transaction statistics every 30s
	if _, err := cr.Every("30s").Do(func() {
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
				c.log.WithFields(txFields).Info("transactions accepted by targets")
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

// SendTransactionsToPeers sends transactions to all connected RPC peers
func (c *Coordinator) SendTransactionsToPeers(ctx context.Context, transactions *mimicry.Transactions) error {
	if transactions == nil || len(*transactions) == 0 {
		return nil
	}

	// Debug level log for detailed troubleshooting if needed
	c.log.WithField("count", len(*transactions)).Debug("sending transactions to RPC peers")

	errg := &errgroup.Group{}

	c.mu.Lock()
	peers := *c.peers
	c.mu.Unlock()

	for _, peer := range peers {
		if peer != nil {
			p := peer // Create a copy of the loop variable to avoid race conditions

			errg.Go(func() error {
				result, err := p.SendTransactions(ctx, transactions)

				status := "success"
				if err != nil {
					status = "failure"
				}

				c.metrics.AddTransactions(len(*transactions), status)

				// Track individual transaction types
				for _, tx := range *transactions {
					c.metrics.AddTransactionByType(tx.Type(), status)
				}

				// Count only truly accepted transactions (not AlreadyKnown)
				if result != nil && len(result.AcceptedByType) > 0 {
					c.txCountersLock.Lock()
					for txType, count := range result.AcceptedByType {
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

						c.txCounters[typeStr] += count
					}
					c.txCountersLock.Unlock()
				}

				return err
			})
		}
	}

	return errg.Wait()
}
