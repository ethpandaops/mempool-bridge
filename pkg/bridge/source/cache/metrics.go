package cache

import "github.com/prometheus/client_golang/prometheus"

// Metrics provides Prometheus metrics for cache operations.
type Metrics struct {
	sharedInsertionsTotal *prometheus.GaugeVec
	sharedHitsTotal       *prometheus.GaugeVec
	sharedMissesTotal     *prometheus.GaugeVec
	sharedEvictionsTotal  *prometheus.GaugeVec
	registerer            prometheus.Registerer
}

// NewMetrics creates a new Metrics instance.
func NewMetrics(namespace string) *Metrics {
	return NewMetricsWithRegisterer(namespace, prometheus.DefaultRegisterer)
}

// NewMetricsWithRegisterer creates a new Metrics instance with a custom registerer.
func NewMetricsWithRegisterer(namespace string, registerer prometheus.Registerer) *Metrics {
	m := &Metrics{
		sharedInsertionsTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "shared_insertions_total",
			Help:      "Total number of shared insertions",
		}, []string{"store"}),
		sharedHitsTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "shared_hits_total",
			Help:      "Total number of shared hits",
		}, []string{"store"}),
		sharedMissesTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "shared_misses_total",
			Help:      "Total number of shared misses",
		}, []string{"store"}),
		sharedEvictionsTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "shared_evictions_total",
			Help:      "Total number of shared evictions",
		}, []string{"store"}),
		registerer: registerer,
	}

	// Only register metrics if a registerer is provided
	if registerer != nil {
		registerer.MustRegister(m.sharedInsertionsTotal)
		registerer.MustRegister(m.sharedHitsTotal)
		registerer.MustRegister(m.sharedMissesTotal)
		registerer.MustRegister(m.sharedEvictionsTotal)
	}

	return m
}

// SetSharedInsertions sets the shared insertions metric.
func (m *Metrics) SetSharedInsertions(count uint64, store string) {
	m.sharedInsertionsTotal.WithLabelValues(store).Set(float64(count))
}

// SetSharedHits sets the shared hits metric.
func (m *Metrics) SetSharedHits(count uint64, store string) {
	m.sharedHitsTotal.WithLabelValues(store).Set(float64(count))
}

// SetSharedMisses sets the shared misses metric.
func (m *Metrics) SetSharedMisses(count uint64, store string) {
	m.sharedMissesTotal.WithLabelValues(store).Set(float64(count))
}

// SetSharedEvictions sets the shared evictions metric.
func (m *Metrics) SetSharedEvictions(count uint64, store string) {
	m.sharedEvictionsTotal.WithLabelValues(store).Set(float64(count))
}
