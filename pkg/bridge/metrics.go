package bridge

import "github.com/prometheus/client_golang/prometheus"

// Metrics provides Prometheus metrics for the bridge.
type Metrics struct {
	transactionsTotal *prometheus.CounterVec
}

// NewMetrics creates a new Metrics instance.
func NewMetrics(namespace string) *Metrics {
	m := &Metrics{
		transactionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "source_transactions_total",
			Help:      "Total number of transactions total",
		}, []string{}),
	}

	prometheus.MustRegister(m.transactionsTotal)

	return m
}

// AddTransactions increments the transaction counter by the given count.
func (m *Metrics) AddTransactions(count int) {
	m.transactionsTotal.WithLabelValues().Add(float64(count))
}
