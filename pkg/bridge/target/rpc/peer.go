package rpc

import (
	"context"
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/sirupsen/logrus"
)

var (
	// ErrAllTransactionsFailed is returned when all transactions fail to send
	ErrAllTransactionsFailed = errors.New("all transactions failed to send")
)

// Peer represents an RPC connection to an Ethereum node for sending transactions
type Peer struct {
	log logrus.FieldLogger

	rpcEndpoint     string
	ethClient       *ethclient.Client
	sendConcurrency int

	ready bool
}

// NewPeer creates a new RPC target peer
func NewPeer(_ context.Context, log logrus.FieldLogger, rpcEndpoint string, sendConcurrency int) (*Peer, error) {
	// Default to 10 if not set or invalid
	if sendConcurrency <= 0 {
		sendConcurrency = 10
	}

	p := Peer{
		log:             log.WithField("rpc_endpoint", rpcEndpoint),
		rpcEndpoint:     rpcEndpoint,
		sendConcurrency: sendConcurrency,
		ready:           false,
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
func (p *Peer) Stop(_ context.Context) error {
	if p.ethClient != nil {
		p.ethClient.Close()
	}

	return nil
}

// SendResult contains the results of sending transactions
type SendResult struct {
	AcceptedByType map[uint8]int // Count of truly accepted transactions by type (not AlreadyKnown)
	TotalSent      int
}

// SendTransactions sends transactions via RPC concurrently
func (p *Peer) SendTransactions(ctx context.Context, transactions *mimicry.Transactions) (*SendResult, error) {
	result := &SendResult{
		AcceptedByType: make(map[uint8]int),
		TotalSent:      0,
	}

	if !p.ready {
		p.log.Debug("peer is not ready")
		return result, nil
	}

	if transactions == nil {
		p.log.Debug("transactions is nil")
		return result, nil
	}

	// Send transactions concurrently with semaphore for rate limiting
	type sendResult struct {
		txHash  string
		txType  uint8
		err     error
	}

	results := make(chan sendResult, len(*transactions))
	sem := make(chan struct{}, p.sendConcurrency)

	for _, tx := range *transactions {
		tx := tx // Capture loop variable
		go func() {
			sem <- struct{}{}        // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			err := p.ethClient.SendTransaction(ctx, tx)

			results <- sendResult{
				txHash: tx.Hash().String(),
				txType: tx.Type(),
				err:    err,
			}
		}()
	}

	// Collect results
	successCount := 0
	errorCount := 0
	acceptedCount := 0 // Truly accepted (not AlreadyKnown)

	for i := 0; i < len(*transactions); i++ {
		res := <-results

		if res.err == nil {
			p.log.WithFields(logrus.Fields{
				"tx_hash": res.txHash,
				"tx_type": res.txType,
			}).Debug("sent transaction")
			successCount++
			acceptedCount++
			result.AcceptedByType[res.txType]++
			continue
		}

		// Handle error cases
		errStr := res.err.Error()

		switch {
		case strings.Contains(errStr, "already known"), strings.Contains(errStr, "AlreadyKnown"):
			// AlreadyKnown means the target already has the transaction - count as success but not as accepted
			p.log.WithFields(logrus.Fields{
				"tx_hash": res.txHash,
				"tx_type": res.txType,
			}).Debug("transaction already known by target (success)")
			successCount++
		case strings.Contains(errStr, "replacement transaction underpriced"), strings.Contains(errStr, "nonce too low"):
			// Other expected errors - log at debug but count as error
			p.log.WithFields(logrus.Fields{
				"tx_hash": res.txHash,
				"error":   errStr,
			}).Debug("transaction rejected (expected)")
			errorCount++
		default:
			// Unexpected errors - log at warn
			p.log.WithFields(logrus.Fields{
				"tx_hash": res.txHash,
				"error":   errStr,
			}).Warn("failed to send transaction")
			errorCount++
		}
	}

	result.TotalSent = len(*transactions)

	if successCount > 0 || errorCount > 0 {
		p.log.WithFields(logrus.Fields{
			"success":  successCount,
			"accepted": acceptedCount,
			"errors":   errorCount,
			"total":    len(*transactions),
		}).Debug("transaction batch complete")
	}

	// Return error only if all transactions failed
	if errorCount > 0 && successCount == 0 {
		return result, ErrAllTransactionsFailed
	}

	return result, nil
}
