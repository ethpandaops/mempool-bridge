package bridge

import "github.com/prometheus/client_golang/prometheus"

// Metrics provides Prometheus metrics for the bridge.
type Metrics struct {
	transactionsTotal *prometheus.CounterVec
}

// NewMetrics creates a new Metrics instance.
func NewMetrics(namespace string) *Metrics {
	return NewMetricsWithRegisterer(namespace, prometheus.DefaultRegisterer)
}

// NewMetricsWithRegisterer creates a new Metrics instance with a custom registerer.
// Pass nil to skip metrics registration (useful for tests).
func NewMetricsWithRegisterer(namespace string, registerer prometheus.Registerer) *Metrics {
	m := &Metrics{
		transactionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "source_transactions_total",
			Help:      "Total number of transactions total",
		}, []string{}),
	}

	if registerer != nil {
		registerer.MustRegister(m.transactionsTotal)
	}

	return m
}

// AddTransactions increments the transaction counter by the given count.
func (m *Metrics) AddTransactions(count int) {
	m.transactionsTotal.WithLabelValues().Add(float64(count))
}
