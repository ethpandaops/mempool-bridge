package bridge

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/target"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

type Bridge struct {
	log *logrus.Logger
	Cfg Config

	s *source.Coordinator
	t *target.Coordinator

	metrics *Metrics
}

func New(log *logrus.Logger, conf *Config) *Bridge {
	if err := conf.Validate(); err != nil {
		log.Fatalf("invalid config: %s", err)
	}

	t, err := target.NewCoordinator(&conf.Target, log)
	if err != nil {
		log.Fatalf("failed to create target: %s", err)
	}

	b := &Bridge{
		Cfg:     *conf,
		log:     log,
		t:       t,
		metrics: NewMetrics("mempool_bridge"),
	}

	s, err := source.NewCoordinator(&conf.Source, b.broadcast, log)
	if err != nil {
		log.Fatalf("failed to create source: %s", err)
	}

	b.s = s

	return b
}

func (b *Bridge) Start(ctx context.Context) error {
	b.log.Infof("starting mempool bridge")

	if err := b.ServeMetrics(ctx); err != nil {
		return err
	}

	if err := b.t.Start(ctx); err != nil {
		return err
	}

	if err := b.s.Start(ctx); err != nil {
		return err
	}

	cancel := make(chan os.Signal, 1)
	signal.Notify(cancel, syscall.SIGTERM, syscall.SIGINT)

	sig := <-cancel
	b.log.Printf("Caught signal: %v", sig)

	b.log.Printf("Shutting down...")

	if err := b.s.Stop(ctx); err != nil {
		b.log.Printf("failed to stop source: %s", err)
	}

	if err := b.t.Stop(ctx); err != nil {
		b.log.Printf("failed to stop target: %s", err)
	}

	return nil
}

func (b *Bridge) ServeMetrics(ctx context.Context) error {
	go func() {
		server := &http.Server{
			Addr:              b.Cfg.MetricsAddr,
			ReadHeaderTimeout: 15 * time.Second,
		}

		server.Handler = promhttp.Handler()

		b.log.Infof("serving metrics at %s", b.Cfg.MetricsAddr)

		if err := server.ListenAndServe(); err != nil {
			b.log.Fatal(err)
		}
	}()

	return nil
}

func (b *Bridge) broadcast(ctx context.Context, transactions *mimicry.Transactions) error {
	if transactions == nil {
		return nil
	}

	length := len(*transactions)

	if length == 0 {
		return nil
	}

	b.metrics.AddTransactions(length)

	if err := b.t.SendTransactionsToPeers(ctx, transactions); err != nil {
		return err
	}

	return nil
}
