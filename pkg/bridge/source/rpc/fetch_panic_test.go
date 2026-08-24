package rpc

// Regression test for missing panic recovery in the per-hash fetch
// goroutine. Before this fix, a panic there (e.g. a nil client) would both
// crash the whole process and, even if it somehow didn't, leave the
// collector loop in fetchAndProcessTransactions blocked forever waiting on
// a result that was never sent. A nil p.ethClient reliably panics inside
// the ethclient method itself (it dereferences a receiver field), which is
// exactly the kind of failure the recover is meant to contain.

import (
	"context"
	"testing"
	"time"

	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func TestFetchAndProcessTransactions_RecoversFromPanic(t *testing.T) {
	log, hook := logrustest.NewNullLogger()

	p := &Peer{
		log:          log,
		seenTxHashes: make(map[string]bool),
		ethClient:    nil,
	}

	done := make(chan error, 1)

	go func() {
		done <- p.fetchAndProcessTransactions(context.Background(), []string{"0xabc"})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("fetchAndProcessTransactions: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetchAndProcessTransactions hung, the collector never received a result for the panicking fetch")
	}

	found := false

	for _, entry := range hook.AllEntries() {
		if entry.Message == "panic fetching transaction" {
			found = true
		}
	}

	if !found {
		t.Fatal("expected the panic to be logged")
	}
}
