package bridge

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	transactionsTotal *prometheus.CounterVec
}

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

func (m *Metrics) AddTransactions(count int) {
	m.transactionsTotal.WithLabelValues().Add(float64(count))
}
