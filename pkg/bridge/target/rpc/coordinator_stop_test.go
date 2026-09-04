package rpc

// Regression tests for graceful shutdown. Coordinator.Stop used to be a
// no-op: it never cancelled anything, so the per-peer goroutine parked on
// <-disconnect in startPollingPeer/Start never woke up, its cleanup
// (peer.Stop) never ran, and Stop returned immediately regardless of
// whether any peer had actually stopped.
//
// These tests drive the exact blocking shape of that goroutine (without a
// real network dial) against the real stopCtx/wg fields Stop now uses.

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestCoordinatorStop_UnblocksParkedGoroutineAndWaits(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	stopCtx, stopCancel := context.WithCancel(context.Background())

	c := &Coordinator{
		log:        log,
		peers:      &map[string]*Peer{},
		stopCtx:    stopCtx,
		stopCancel: stopCancel,
	}

	cleanupRan := make(chan struct{})
	disconnect := make(chan error)

	c.wg.Add(1)

	go func() {
		defer c.wg.Done()
		defer close(cleanupRan)

		select {
		case <-disconnect:
		case <-c.stopCtx.Done():
		}
	}()

	// Give the goroutine a moment to actually park on the select.
	time.Sleep(50 * time.Millisecond)

	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() returned an error: %v", err)
	}

	select {
	case <-cleanupRan:
	default:
		t.Fatal("expected the parked goroutine's cleanup to have run by the time Stop() returned")
	}
}

func TestCoordinatorStop_ReturnsWhenCtxExpires(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	stopCtx, stopCancel := context.WithCancel(context.Background())

	c := &Coordinator{
		log:        log,
		peers:      &map[string]*Peer{},
		stopCtx:    stopCtx,
		stopCancel: stopCancel,
	}

	// Simulates a peer whose cleanup hangs and never finishes.
	c.wg.Add(1)

	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) }) // let the goroutine exit after the test

	go func() {
		defer c.wg.Done()
		<-stuck
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()

	err := c.Stop(ctx)
	if err == nil {
		t.Fatal("expected Stop() to return an error once ctx expired")
	}

	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Fatalf("Stop() took %v to return, expected it to be bounded by ctx's timeout", elapsed)
	}
}
