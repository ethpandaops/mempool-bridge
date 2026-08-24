package p2p

// Regression tests for missing panic recovery in the detached export
// goroutines. Before this fix, a panic in either goroutine (e.g. a nil
// client, a malformed response) would crash the whole process, unlike
// source/rpc's polling loop which already recovers. A nil p.client
// reliably panics inside the mimicry client's own method (it locks a
// mutex field on the receiver), which is exactly the kind of failure the
// recover is meant to contain to this one peer instead of the process.

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func waitForLogEntry(t *testing.T, hook *logrustest.Hook, message string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, entry := range hook.AllEntries() {
			if entry.Message == message {
				return
			}
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected a log entry with message %q, but it never appeared", message)
}

func TestExportNormalTransactions_RecoversFromPanic(t *testing.T) {
	log, hook := logrustest.NewNullLogger()

	p := &Peer{
		log:         log,
		sharedCache: cache.NewSharedCacheWithRegisterer(nil),
		client:      nil,
	}

	hash := common.HexToHash("0x1")

	if err := p.ExportNormalTransactions(context.Background(), []*common.Hash{&hash}); err != nil {
		t.Fatalf("ExportNormalTransactions: %v", err)
	}

	waitForLogEntry(t, hook, "panic exporting normal transactions")
}

func TestExportBlobTransactions_RecoversFromPanic(t *testing.T) {
	log, hook := logrustest.NewNullLogger()

	p := &Peer{
		log:         log,
		sharedCache: cache.NewSharedCacheWithRegisterer(nil),
		client:      nil,
	}

	hash := common.HexToHash("0x1")

	if err := p.ExportBlobTransactions(context.Background(), []*common.Hash{&hash}); err != nil {
		t.Fatalf("ExportBlobTransactions: %v", err)
	}

	waitForLogEntry(t, hook, "panic exporting blob transactions")
}
