package rpc

// Regression test for a data race on Peer.ready. Its ctx.Done() monitor
// goroutine in Start only ever fires once the ctx passed through the
// coordinator can actually be cancelled (graceful shutdown), at which point
// it writes ready concurrently with SendTransactions reading it. This
// drives both sides concurrently and requires a clean run under the race
// detector.
//
// transactions is nil here so a call that observes ready=true still returns
// immediately without touching p.ethClient, keeping the test focused on the
// ready field itself.
//
// Run: go test -race -run TestPeerReady_ConcurrentAccess ./pkg/bridge/target/rpc/...

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestPeerReady_ConcurrentAccess(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	p := &Peer{log: log}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	stop := make(chan struct{})

	// Writer mimics Start's ctx.Done() monitor goroutine toggling readiness.
	wg.Add(1)

	go func() {
		defer wg.Done()

		for {
			select {
			case <-stop:
				return
			default:
			}

			p.ready.Store(true)
			p.ready.Store(false)
		}
	}()

	// Readers mimic concurrent SendTransactionsToPeers callers.
	for i := 0; i < 4; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				_, _ = p.SendTransactions(ctx, nil)
			}
		}()
	}

	<-ctx.Done()
	close(stop)
	wg.Wait()
}
