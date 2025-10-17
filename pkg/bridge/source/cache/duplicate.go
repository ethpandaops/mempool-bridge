// Package cache provides caching mechanisms for transaction deduplication and tracking.
package cache

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// DuplicateCache tracks duplicate transactions within a time window.
type DuplicateCache struct {
	Transaction *ttlcache.Cache[string, time.Time]
}

// NewDuplicateCache creates a new DuplicateCache instance.
func NewDuplicateCache() *DuplicateCache {
	return &DuplicateCache{
		Transaction: ttlcache.New(
			ttlcache.WithTTL[string, time.Time](5 * time.Minute),
		),
	}
}

// Start begins the cache background processes.
func (d *DuplicateCache) Start() {
	go d.Transaction.Start()
}

// Stop stops the cache.
func (d *DuplicateCache) Stop() {
	d.Transaction.Stop()
}
