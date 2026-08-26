package cache

// Regression test for the shared cache's own metrics scheduler leaking on
// Stop(), and for Stop() actually stopping the composed SentCache's
// scheduler too (previously nothing tracked either gocron.Scheduler, so
// Stop() had nothing to call Stop() on and both kept running for the life
// of the process regardless of whether the cache itself was stopped).

import (
	"context"
	"testing"
)

func TestSharedCache_StopStopsSchedulers(t *testing.T) {
	c := NewSharedCacheWithRegisterer(nil)
	ctx := context.Background()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if c.cron == nil || !c.cron.IsRunning() {
		t.Fatal("expected the shared cache's metrics scheduler to be running after Start()")
	}

	if c.Sent.cron == nil || !c.Sent.cron.IsRunning() {
		t.Fatal("expected the composed SentCache's metrics scheduler to be running after Start()")
	}

	c.Stop()

	if c.cron.IsRunning() {
		t.Fatal("expected the shared cache's metrics scheduler to be stopped after Stop(), but it is still running")
	}

	if c.Sent.cron.IsRunning() {
		t.Fatal("expected the composed SentCache's metrics scheduler to be stopped after Stop(), but it is still running")
	}
}
