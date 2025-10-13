package bridge

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethpandaops/ethcore/pkg/execution/mimicry"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	sourcep2p "github.com/ethpandaops/mempool-bridge/pkg/bridge/source/p2p"
	sourcerpc "github.com/ethpandaops/mempool-bridge/pkg/bridge/source/rpc"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/target"
	targetp2p "github.com/ethpandaops/mempool-bridge/pkg/bridge/target/p2p"
	targetrpc "github.com/ethpandaops/mempool-bridge/pkg/bridge/target/rpc"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// SourceCoordinator defines the interface for source coordinators
type SourceCoordinator interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// TargetCoordinator defines the interface for target coordinators
type TargetCoordinator interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	SendTransactionsToPeers(ctx context.Context, transactions *mimicry.Transactions) error
}

type Bridge struct {
	log *logrus.Logger
	Cfg Config

	s SourceCoordinator
	t TargetCoordinator

	metrics *Metrics
}

func New(log *logrus.Logger, conf *Config) *Bridge {
	if err := conf.Validate(); err != nil {
		log.Fatalf("invalid config: %s", err)
	}

	b := &Bridge{
		Cfg:     *conf,
		log:     log,
		metrics: NewMetrics("mempool_bridge"),
	}

	// Create target coordinator based on mode
	t, err := createTargetCoordinator(conf.Mode, &conf.Target, log)
	if err != nil {
		log.Fatalf("failed to create target: %s", err)
	}

	b.t = t

	// Create source coordinator based on mode
	s, err := createSourceCoordinator(conf.Mode, &conf.Source, b.broadcast, log)
	if err != nil {
		log.Fatalf("failed to create source: %s", err)
	}

	b.s = s

	log.WithField("mode", conf.Mode).Info("initialized bridge")

	return b
}

// createTargetCoordinator creates a target coordinator based on the mode
func createTargetCoordinator(mode Mode, config *target.Config, log *logrus.Logger) (TargetCoordinator, error) {
	switch mode {
	case ModeP2P:
		return targetp2p.NewCoordinator(config, log)
	case ModeRPC:
		return targetrpc.NewCoordinator(config, log)
	default:
		return nil, fmt.Errorf("unknown mode: %s", mode)
	}
}

// createSourceCoordinator creates a source coordinator based on the mode
func createSourceCoordinator(mode Mode, config *source.Config, broadcast func(ctx context.Context, transactions *mimicry.Transactions) error, log *logrus.Logger) (SourceCoordinator, error) {
	switch mode {
	case ModeP2P:
		return sourcep2p.NewCoordinator(config, broadcast, log)
	case ModeRPC:
		return sourcerpc.NewCoordinator(config, broadcast, log)
	default:
		return nil, fmt.Errorf("unknown mode: %s", mode)
	}
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
