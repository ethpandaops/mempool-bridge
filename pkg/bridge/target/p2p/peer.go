package p2p

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
)

// Peer represents a P2P target connection to a single node.
type Peer struct {
	log logrus.FieldLogger

	nodeRecord string

	client *mimicry.Client

	ready bool
}

// NewPeer creates a new P2P target peer.
func NewPeer(ctx context.Context, log logrus.FieldLogger, nodeRecord string) (*Peer, error) {
	client, err := mimicry.New(ctx, log, nodeRecord, "mempool-bridge")
	if err != nil {
		return nil, err
	}

	p := Peer{
		log:        log.WithField("node_record", nodeRecord),
		nodeRecord: nodeRecord,
		client:     client,
		ready:      false,
	}

	return &p, nil
}

// Start starts the peer connection.
func (p *Peer) Start(ctx context.Context) (<-chan error, error) {
	response := make(chan error)

	p.client.OnStatus(ctx, func(_ context.Context, _ *mimicry.Status) error {
		p.ready = true

		return nil
	})

	p.client.OnDisconnect(ctx, func(_ context.Context, reason *mimicry.Disconnect) error {
		p.ready = false
		str := "unknown"

		if reason != nil {
			str = reason.Reason.String()
		}

		p.log.WithFields(logrus.Fields{
			"reason": str,
		}).Debug("disconnected from client")

		response <- fmt.Errorf("%w (reason %s)", ErrPeerDisconnected, str)

		return nil
	})

	p.log.Debug("attempting to connect to client")

	err := p.client.Start(ctx)
	if err != nil {
		p.log.WithError(err).Debug("failed to dial client")

		return nil, err
	}

	return response, nil
}

// Stop stops the peer and cleans up resources.
func (p *Peer) Stop(ctx context.Context) error {
	if p.client != nil {
		if err := p.client.Stop(ctx); err != nil {
			return err
		}
	}

	return nil
}

// SendTransactions sends transactions to the peer.
func (p *Peer) SendTransactions(ctx context.Context, transactions *mimicry.Transactions) error {
	if !p.ready {
		p.log.Debug("peer is not ready")

		return nil
	}

	if transactions == nil {
		p.log.Debug("transactions is nil")

		return nil
	}

	err := p.client.Transactions(ctx, transactions)

	if err != nil {
		p.log.WithError(err).Debug("failed to send transactions")
	} else {
		p.log.WithField("count", len(*transactions)).Debug("sent transactions")
	}

	return err
}
