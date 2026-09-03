package p2p

// Regression test for SendTransactionsToPeers silently returning success
// when there are zero connected peers to deliver to (all peer map entries
// nil, so no errgroup goroutine ever runs and errg.Wait() returns nil). This
// left broadcast (pkg/bridge) unable to tell a real delivery apart from
// delivering to nobody, so it marked transactions as sent either way.

import (
	"context"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/target"
	"github.com/sirupsen/logrus"
)

func TestSendTransactionsToPeers_ZeroConnectedPeersIsAnError(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	c := &Coordinator{
		log:        log,
		peers:      &map[string]*Peer{"peerA": nil, "peerB": nil},
		metrics:    target.NewMetrics("mempool_bridge_target_test_zero_peers"),
		txCounters: make(map[string]int),
	}

	tx := types.NewTransaction(0, common.HexToAddress("0x1234"), nil, 21000, nil, nil)
	txs := mimicry.Transactions{tx}

	err := c.SendTransactionsToPeers(context.Background(), &txs)
	if !errors.Is(err, ErrNoPeersAvailable) {
		t.Fatalf("expected ErrNoPeersAvailable when no peers are connected, got: %v", err)
	}
}
