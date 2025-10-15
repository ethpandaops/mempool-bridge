package cache

import (
	"context"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/jellydator/ttlcache/v3"
	"github.com/prometheus/client_golang/prometheus"
)

// SentCache tracks transactions that have been successfully sent to targets
// to prevent duplicate forwarding across all source peers
type SentCache struct {
	Transaction *ttlcache.Cache[string, time.Time]

	metrics *Metrics
}

// NewSentCache creates a new sent transaction cache
func NewSentCache() *SentCache {
	return NewSentCacheWithRegisterer(prometheus.DefaultRegisterer)
}

// NewSentCacheWithRegisterer creates a new sent transaction cache with a custom registerer.
// Pass nil to skip metrics registration (useful for tests).
func NewSentCacheWithRegisterer(registerer prometheus.Registerer) *SentCache {
	return &SentCache{
		Transaction: ttlcache.New(
			ttlcache.WithTTL[string, time.Time](5 * time.Minute),
		),
		metrics: NewMetricsWithRegisterer("mempool_bridge_sent_cache", registerer),
	}
}

// newSentCacheWithMetrics creates a new sent transaction cache using existing metrics.
// This is used internally by SharedCache to avoid duplicate metric registration.
func newSentCacheWithMetrics(metrics *Metrics) *SentCache {
	return &SentCache{
		Transaction: ttlcache.New(
			ttlcache.WithTTL[string, time.Time](5 * time.Minute),
		),
		metrics: metrics,
	}
}

// Start begins the sent cache background processes
func (s *SentCache) Start(ctx context.Context) error {
	go s.Transaction.Start()

	return s.startCrons(ctx)
}

// Stop stops the sent cache
func (s *SentCache) Stop() {
	s.Transaction.Stop()
}

// startCrons starts periodic metrics updates
func (s *SentCache) startCrons(_ context.Context) error {
	c := gocron.NewScheduler(time.Local)

	if _, err := c.Every("5s").Do(func() {
		transactionMetrics := s.Transaction.Metrics()
		s.metrics.SetSharedInsertions(transactionMetrics.Insertions, "sent")
		s.metrics.SetSharedHits(transactionMetrics.Hits, "sent")
		s.metrics.SetSharedMisses(transactionMetrics.Misses, "sent")
		s.metrics.SetSharedEvictions(transactionMetrics.Evictions, "sent")
	}); err != nil {
		return err
	}

	c.StartAsync()

	return nil
}
