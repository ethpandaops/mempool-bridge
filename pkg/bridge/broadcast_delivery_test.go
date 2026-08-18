package bridge

// Regression tests for the mark-sent-before-send ordering in broadcast.
// broadcast used to commit the dedup mark before attempting delivery, so a
// failed send (or a send that reached zero connected peers, see
// target/{p2p,rpc}.ErrNoPeersAvailable) still permanently suppressed the
// transaction: the next observation of the same hash was dropped as an
// already-sent duplicate and never retried. Delivery is now confirmed
// before the mark is written.

import (
	"context"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/sirupsen/logrus"
)

var errSimulatedSendFailure = errors.New("simulated send failure")

func TestBroadcast_RetriesAfterSendFailure(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	b := &Bridge{
		log:       log,
		sentCache: mustCreateSentCache(t),
		metrics:   NewMetricsWithRegisterer("test_broadcast_retries_after_failure", nil),
	}

	ctx := context.Background()
	if err := b.sentCache.Start(ctx); err != nil {
		t.Fatalf("failed to start sentCache: %v", err)
	}
	defer b.sentCache.Stop()

	attempts := 0
	shouldFail := true

	b.t = &mockTargetCoordinator{
		sendFunc: func(_ context.Context, _ *mimicry.Transactions) error {
			attempts++

			if shouldFail {
				return errSimulatedSendFailure
			}

			return nil
		},
	}

	tx := types.NewTransaction(0, common.HexToAddress("0xabc"), nil, 21000, nil, nil)
	txs := mimicry.Transactions{tx}

	// First attempt fails. The transaction must not be marked as sent.
	if err := b.broadcast(ctx, &txs); !errors.Is(err, errSimulatedSendFailure) {
		t.Fatalf("expected the send error to propagate, got: %v", err)
	}

	if attempts != 1 {
		t.Fatalf("expected 1 send attempt, got %d", attempts)
	}

	if exists := b.sentCache.Transaction.Get(tx.Hash().String()); exists != nil {
		t.Fatalf("transaction was marked sent despite the send failing")
	}

	// A retry of the same transaction must actually attempt delivery again,
	// not get silently swallowed as a duplicate.
	shouldFail = false

	if err := b.broadcast(ctx, &txs); err != nil {
		t.Fatalf("expected the retry to succeed, got: %v", err)
	}

	if attempts != 2 {
		t.Fatalf("expected the retry to reach the target (2 attempts total), got %d", attempts)
	}

	if exists := b.sentCache.Transaction.Get(tx.Hash().String()); exists == nil {
		t.Fatalf("expected transaction to be marked sent after a successful delivery")
	}

	// Now that delivery succeeded, a further broadcast of the same
	// transaction should be filtered as a duplicate rather than resent.
	if err := b.broadcast(ctx, &txs); err != nil {
		t.Fatalf("expected the duplicate broadcast to be filtered out silently, got: %v", err)
	}

	if attempts != 2 {
		t.Fatalf("expected no further send attempt for an already-delivered transaction, got %d", attempts)
	}
}

func TestBroadcast_DoesNotMarkSentWhenDeliveryErrors(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	b := &Bridge{
		log:       log,
		sentCache: mustCreateSentCache(t),
		metrics:   NewMetricsWithRegisterer("test_broadcast_no_mark_on_error", nil),
	}

	ctx := context.Background()
	if err := b.sentCache.Start(ctx); err != nil {
		t.Fatalf("failed to start sentCache: %v", err)
	}
	defer b.sentCache.Stop()

	// Models a target coordinator delivering to zero connected peers, which
	// target/{p2p,rpc}.Coordinator.SendTransactionsToPeers now reports as an
	// explicit error rather than silent success.
	b.t = &mockTargetCoordinator{
		sendFunc: func(_ context.Context, _ *mimicry.Transactions) error {
			return errSimulatedSendFailure
		},
	}

	tx := types.NewTransaction(1, common.HexToAddress("0xdef"), nil, 21000, nil, nil)
	txs := mimicry.Transactions{tx}

	if err := b.broadcast(ctx, &txs); err == nil {
		t.Fatalf("expected broadcast to surface the delivery error")
	}

	if exists := b.sentCache.Transaction.Get(tx.Hash().String()); exists != nil {
		t.Fatalf("transaction was marked sent despite zero successful delivery")
	}
}
