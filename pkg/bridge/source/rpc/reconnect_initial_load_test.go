package rpc

// Regression test for the initial-load suppression re-triggering on every
// reconnect. pollTxPool's first poll of any Peer instance treats the whole
// pool as already-seen and forwards nothing, which is the right behavior for
// a genuine first connection. The coordinator constructs a brand new Peer on
// every retry.Do iteration, so without sharing initialLoadComplete across
// those instances, this suppression fired again on every reconnect, and any
// transaction that arrived while disconnected was silently dropped instead
// of forwarded once the connection came back.
//
// This drives two successive Peer instances against a fake endpoint, the
// way the coordinator's retry loop does, sharing one initialLoadComplete
// flag between them (as the coordinator now does): the first poll should
// suppress everything already pending, and a transaction that only shows up
// starting with the second poll should be forwarded.
//
// Run: go test -run TestReconnect_ForwardsTransactionsThatArriveDuringOutage -v ./pkg/bridge/source/rpc/...

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/sirupsen/logrus"
)

// txAJSON and txBJSON are two distinct well-formed legacy transactions,
// distinguished by nonce/recipient so they hash differently. txA models
// whatever was already pending at the genuine first connection; txB models
// a transaction that only appears in the pool starting from the second poll
// (e.g. submitted while the endpoint was disconnected).
const (
	txAJSON = `{
		"nonce":"0x0",
		"blockHash":null,
		"blockNumber":null,
		"transactionIndex":null,
		"from":"0x0000000000000000000000000000000000000000",
		"to":"0x0000000000000000000000000000000000000001",
		"value":"0x0",
		"gas":"0x5208",
		"gasPrice":"0x3b9aca00",
		"input":"0x",
		"v":"0x1b",
		"r":"0x88ff6cf0fefd94db46111149ae4bfc179e9b94721fffd821d38d16464b3f71d",
		"s":"0x45e0aff800961cfce805daef7016b9b675c137a6a41a548f7b60a3484c06a33",
		"type":"0x0"
	}`
	txBJSON = `{
		"nonce":"0x1",
		"blockHash":null,
		"blockNumber":null,
		"transactionIndex":null,
		"from":"0x0000000000000000000000000000000000000000",
		"to":"0x0000000000000000000000000000000000000002",
		"value":"0x0",
		"gas":"0x5208",
		"gasPrice":"0x3b9aca00",
		"input":"0x",
		"v":"0x1b",
		"r":"0x88ff6cf0fefd94db46111149ae4bfc179e9b94721fffd821d38d16464b3f71d",
		"s":"0x45e0aff800961cfce805daef7016b9b675c137a6a41a548f7b60a3484c06a33",
		"type":"0x0"
	}`
)

// newReconnectFakeServer serves txpool_content (txA only on the first call,
// txA+txB from the second call onward) and eth_getTransactionByHash (looked
// up by hash from hashToJSON) over real JSON-RPC.
func newReconnectFakeServer(t *testing.T, hashToJSON map[string]string) *httptest.Server {
	t.Helper()

	var calls int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "txpool_content":
			callIdx := atomic.AddInt64(&calls, 1)

			pending := fmt.Sprintf(`{"0":%s}`, txAJSON)
			if callIdx >= 2 {
				pending = fmt.Sprintf(`{"0":%s,"1":%s}`, txAJSON, txBJSON)
			}

			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"pending":{"0x0000000000000000000000000000000000000000":%s},"queued":{}}}`,
				string(req.ID), pending)
		case "eth_getTransactionByHash":
			var hash string
			if len(req.Params) > 0 {
				_ = json.Unmarshal(req.Params[0], &hash)
			}

			txJSON, ok := hashToJSON[strings.ToLower(hash)]
			if !ok {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":null}`, string(req.ID))
				return
			}

			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, string(req.ID), txJSON)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":null}`, string(req.ID))
		}
	}))

	t.Cleanup(srv.Close)

	return srv
}

func TestReconnect_ForwardsTransactionsThatArriveDuringOutage(t *testing.T) {
	var txA, txB types.Transaction
	if err := json.Unmarshal([]byte(txAJSON), &txA); err != nil {
		t.Fatalf("decode txA fixture: %v", err)
	}

	if err := json.Unmarshal([]byte(txBJSON), &txB); err != nil {
		t.Fatalf("decode txB fixture: %v", err)
	}

	hashToJSON := map[string]string{
		strings.ToLower(txA.Hash().String()): txAJSON,
		strings.ToLower(txB.Hash().String()): txBJSON,
	}

	srv := newReconnectFakeServer(t, hashToJSON)

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	sharedCache := cache.NewSharedCacheWithRegisterer(nil)
	metrics := source.NewMetricsWithRegisterer("test_reconnect_forwarding", nil)

	var forwardedHashes []string

	handler := func(_ context.Context, txs *mimicry.Transactions) error {
		if txs != nil {
			for _, tx := range *txs {
				forwardedHashes = append(forwardedHashes, tx.Hash().String())
			}
		}

		return nil
	}

	ctx := context.Background()

	// initialLoadComplete is shared across both Peer instances below, the
	// same way the coordinator's startPollingPeer shares it across
	// retry.Do iterations for one endpoint.
	initialLoadComplete := &atomic.Bool{}

	// --- Connection #1: the genuine first connection. ---
	peer1, err := NewPeer(ctx, log, srv.URL, handler, sharedCache, nil, metrics, time.Second, initialLoadComplete)
	if err != nil {
		t.Fatalf("NewPeer #1: %v", err)
	}

	peer1.rpcClient, err = rpc.DialContext(ctx, srv.URL)
	if err != nil {
		t.Fatalf("dial #1: %v", err)
	}

	if err := peer1.pollTxPool(ctx); err != nil {
		t.Fatalf("pollTxPool #1: %v", err)
	}

	if len(forwardedHashes) != 0 {
		t.Fatalf("connection #1 (genuine initial load) should forward nothing, forwarded %v", forwardedHashes)
	}

	// --- Connection #2: a reconnect, per coordinator.go's retry.Do this is
	// a brand new Peer instance, but it shares initialLoadComplete. txB has
	// appeared in the pool since connection #1 (simulating a transaction
	// submitted during the outage). ---
	peer2, err := NewPeer(ctx, log, srv.URL, handler, sharedCache, nil, metrics, time.Second, initialLoadComplete)
	if err != nil {
		t.Fatalf("NewPeer #2: %v", err)
	}

	peer2.rpcClient, err = rpc.DialContext(ctx, srv.URL)
	if err != nil {
		t.Fatalf("dial #2: %v", err)
	}

	peer2.ethClient = ethclient.NewClient(peer2.rpcClient)

	if err := peer2.pollTxPool(ctx); err != nil {
		t.Fatalf("pollTxPool #2: %v", err)
	}

	if len(forwardedHashes) != 1 {
		t.Fatalf("expected exactly txB to be forwarded after reconnect, forwarded %v", forwardedHashes)
	}

	if got, want := forwardedHashes[0], txB.Hash().String(); got != want {
		t.Fatalf("expected txB (%s) to be forwarded, got %s", want, got)
	}
}
