package rpc

// Regression test for missing panic recovery in the per-tx send goroutine.
// Before this fix, a panic there (e.g. a nil client) would both crash the
// whole process and, even if it somehow didn't, leave the collector loop in
// SendTransactions blocked forever waiting on a result that was never
// sent. A nil p.ethClient reliably panics inside the ethclient method
// itself, which is exactly the kind of failure the recover is meant to
// contain.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func TestSendTransactions_RecoversFromPanic(t *testing.T) {
	log, hook := logrustest.NewNullLogger()

	p := &Peer{
		log:             log,
		ready:           true,
		sendConcurrency: 1,
		ethClient:       nil,
	}

	tx := types.NewTransaction(0, common.HexToAddress("0x1234"), nil, 21000, nil, nil)
	txs := mimicry.Transactions{tx}

	done := make(chan error, 1)

	go func() {
		_, err := p.SendTransactions(context.Background(), &txs)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrAllTransactionsFailed) {
			t.Fatalf("SendTransactions error = %v, want %v", err, ErrAllTransactionsFailed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTransactions hung, the collector never received a result for the panicking send")
	}

	found := false

	for _, entry := range hook.AllEntries() {
		if entry.Message == "panic sending transaction" {
			found = true
		}
	}

	if !found {
		t.Fatal("expected the panic to be logged")
	}
}
