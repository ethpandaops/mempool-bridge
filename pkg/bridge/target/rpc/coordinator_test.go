package rpc

// Regression test for NewCoordinator skipping config validation entirely
// (it used to only check RPCEndpoints inline instead of calling
// config.Validate), which meant an invalid RetryInterval was never caught
// for RPC target mode.

import (
	"testing"
	"time"

	"github.com/ethpandaops/mempool-bridge/pkg/bridge/target"
	"github.com/sirupsen/logrus"
)

func TestNewCoordinator_RejectsInvalidRetryInterval(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := &target.Config{
		RPCEndpoints: []string{"http://localhost:8545"},
	}

	if _, err := NewCoordinator(cfg, log); err == nil {
		t.Fatal("expected NewCoordinator to reject a zero RetryInterval")
	}
}

func TestNewCoordinator_AcceptsValidConfig(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := &target.Config{
		RPCEndpoints:  []string{"http://localhost:8545"},
		RetryInterval: 30 * time.Second,
	}

	if _, err := NewCoordinator(cfg, log); err != nil {
		t.Fatalf("expected a valid config to be accepted, got: %v", err)
	}
}
