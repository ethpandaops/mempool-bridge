package rpc

import (
	"context"
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/sirupsen/logrus"
)

// Peer represents an RPC connection to an Ethereum node for sending transactions
type Peer struct {
	log logrus.FieldLogger

	rpcEndpoint string
	ethClient   *ethclient.Client

	ready bool
}

// NewPeer creates a new RPC target peer
func NewPeer(ctx context.Context, log logrus.FieldLogger, rpcEndpoint string) (*Peer, error) {
	p := Peer{
		log:         log.WithField("rpc_endpoint", rpcEndpoint),
		rpcEndpoint: rpcEndpoint,
		ready:       false,
	}

	return &p, nil
}

// Start initializes the RPC connection
func (p *Peer) Start(ctx context.Context) (<-chan error, error) {
	response := make(chan error, 1)

	// Connect to RPC endpoint
	client, err := ethclient.DialContext(ctx, p.rpcEndpoint)
	if err != nil {
		return nil, err
	}

	p.ethClient = client

	// Test connection by getting chain ID
	_, err = p.ethClient.ChainID(ctx)
	if err != nil {
		p.log.WithError(err).Error("failed to connect to RPC endpoint")
		return nil, err
	}

	p.ready = true
	p.log.Info("connected to RPC endpoint")

	// Monitor connection in background
	go func() {
		<-ctx.Done()
		p.ready = false
		response <- ctx.Err()
	}()

	return response, nil
}

// Stop closes the RPC connection
func (p *Peer) Stop(ctx context.Context) error {
	if p.ethClient != nil {
		p.ethClient.Close()
	}

	return nil
}

// SendTransactions sends transactions via RPC
func (p *Peer) SendTransactions(ctx context.Context, transactions *mimicry.Transactions) error {
	if !p.ready {
		p.log.Debug("peer is not ready")

		return nil
	}

	if transactions == nil {
		p.log.Debug("transactions is nil")

		return nil
	}

	successCount := 0
	errorCount := 0

	for _, tx := range *transactions {
		if err := p.ethClient.SendTransaction(ctx, tx); err != nil {
			// Log but don't fail - some errors are expected (duplicate tx, nonce too low, etc.)
			errStr := err.Error()

			// Check for common expected errors
			if strings.Contains(errStr, "already known") ||
				strings.Contains(errStr, "replacement transaction underpriced") ||
				strings.Contains(errStr, "nonce too low") {
				p.log.WithFields(logrus.Fields{
					"tx_hash": tx.Hash().String(),
					"error":   errStr,
				}).Debug("transaction rejected (expected)")
			} else {
				p.log.WithFields(logrus.Fields{
					"tx_hash": tx.Hash().String(),
					"error":   errStr,
				}).Warn("failed to send transaction")
			}

			errorCount++
		} else {
			p.log.WithFields(logrus.Fields{
				"tx_hash": tx.Hash().String(),
				"tx_type": tx.Type(),
			}).Debug("sent transaction")
			successCount++
		}
	}

	if successCount > 0 || errorCount > 0 {
		p.log.WithFields(logrus.Fields{
			"success": successCount,
			"errors":  errorCount,
			"total":   len(*transactions),
		}).Debug("transaction batch complete")
	}

	// Return error only if all transactions failed
	if errorCount > 0 && successCount == 0 {
		return errors.New("all transactions failed to send")
	}

	return nil
}
