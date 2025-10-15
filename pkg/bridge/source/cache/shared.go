package cache

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/go-co-op/gocron"
	"github.com/jellydator/ttlcache/v3"
)

// SharedCache provides a shared transaction cache across peers.
type SharedCache struct {
	Transaction *ttlcache.Cache[string, *types.Transaction]

	metrics *Metrics
}

// NewSharedCache creates a new SharedCache instance.
func NewSharedCache() *SharedCache {
	return &SharedCache{
		Transaction: ttlcache.New(
			ttlcache.WithTTL[string, *types.Transaction](5 * time.Minute),
		),
		metrics: NewMetrics("mempool_bridge_source_cache"),
	}
}

// Start begins the cache background processes.
func (d *SharedCache) Start(ctx context.Context) error {
	go d.Transaction.Start()

	return d.startCrons(ctx)
}

func (d *SharedCache) startCrons(_ context.Context) error {
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
