package rpc

// Regression test for a nil-tx panic in txpool_content parsing. A JSON
// `null` in place of a transaction object (a non-conformant or momentarily
// inconsistent RPC backend) decodes to a nil *types.Transaction, and calling
// tx.Hash() on it panics. pollTxPool now skips nil entries instead of
// dereferencing them.
//
// Run: go test -run TestPollTxPool_IgnoresNilTransactionEntries -v ./pkg/bridge/source/rpc/...

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/sirupsen/logrus"
)

func newFakeTxpoolServerWithNullTx(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "txpool_content":
			// A `null` in place of a transaction object.
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"pending":{"0x0000000000000000000000000000000000000000":{"0":null}},"queued":{}}}`,
				string(req.ID))
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":null}`, string(req.ID))
		}
	}))

	t.Cleanup(srv.Close)

	return srv
}

func TestPollTxPool_IgnoresNilTransactionEntries(t *testing.T) {
	srv := newFakeTxpoolServerWithNullTx(t)

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sharedCache := cache.NewSharedCacheWithRegisterer(nil)
	metrics := source.NewMetricsWithRegisterer("test_nil_tx", nil)

	handler := func(_ context.Context, _ *mimicry.Transactions) error { return nil }

	peer, err := NewPeer(context.Background(), log, srv.URL, handler, sharedCache, nil, metrics, 20*time.Millisecond, &atomic.Bool{})
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	respCh, err := peer.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case gotErr := <-respCh:
		t.Fatalf("expected the poller to keep running past a null tx entry, got: %v", gotErr)
	case <-time.After(300 * time.Millisecond):
		// No panic and no polling error surfaced across several poll
		// cycles against a response that previously crashed every time.
	}
}
