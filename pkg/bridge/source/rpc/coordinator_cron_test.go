package rpc

// Regression test for the coordinator's metrics/status scheduler leaking on
// Stop(). startCrons created a gocron.Scheduler as a local variable and
// never stored it, so Stop() (itself previously a no-op) had no way to
// reach it even if it tried.

import (
	"context"
	"testing"

	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source/cache"
	"github.com/sirupsen/logrus"
)

func TestCoordinatorStop_StopsScheduler(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	c := &Coordinator{
		log:     log,
		cache:   cache.NewSharedCacheWithRegisterer(nil),
		peers:   &map[string]bool{},
		metrics: source.NewMetricsWithRegisterer("test_coordinator_rpc_cron_stop", nil),
	}

	if err := c.startCrons(context.Background()); err != nil {
		t.Fatalf("startCrons: %v", err)
	}

	if c.cron == nil || !c.cron.IsRunning() {
		t.Fatal("expected the scheduler to be running after startCrons()")
	}

	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if c.cron.IsRunning() {
		t.Fatal("expected the scheduler to be stopped after Stop(), but it is still running")
	}
}
