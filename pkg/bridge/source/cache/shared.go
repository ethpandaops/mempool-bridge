package cache

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/go-co-op/gocron"
	"github.com/savid/ttlcache/v3"
)

type SharedCache struct {
	Transaction *ttlcache.Cache[string, *types.Transaction]

	metrics *Metrics
}

func NewSharedCache() *SharedCache {
	return &SharedCache{
		Transaction: ttlcache.New(
			ttlcache.WithTTL[string, *types.Transaction](5 * time.Minute),
		),
		metrics: NewMetrics("mempool_bridge_source_cache"),
	}
}

func (d *SharedCache) Start(ctx context.Context) error {
	go d.Transaction.Start()

	if err := d.startCrons(ctx); err != nil {
		return err
	}

	return nil
}

func (d *SharedCache) startCrons(ctx context.Context) error {
	c := gocron.NewScheduler(time.Local)

	if _, err := c.Every("5s").Do(func() {
		transactionMetrics := d.Transaction.Metrics()
		d.metrics.SetSharedInsertions(transactionMetrics.Insertions, "transaction")
		d.metrics.SetSharedHits(transactionMetrics.Hits, "transaction")
		d.metrics.SetSharedMisses(transactionMetrics.Misses, "transaction")
		d.metrics.SetSharedEvictions(transactionMetrics.Evictions, "transaction")
	}); err != nil {
		return err
	}

	c.StartAsync()

	return nil
}
