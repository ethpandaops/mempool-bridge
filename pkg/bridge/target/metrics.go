package target

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	peersTotal        *prometheus.GaugeVec
	transactionsTotal *prometheus.CounterVec
}

func NewMetrics(namespace string) *Metrics {
	m := &Metrics{
		peersTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "peers_total",
			Help:      "Total number of peers",
		}, []string{"status"}),
		transactionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "transactions_total",
			Help:      "Total number of transactions total",
		}, []string{"status"}),
	}

	prometheus.MustRegister(m.peersTotal)
	prometheus.MustRegister(m.transactionsTotal)

	return m
}

func (m *Metrics) SetPeers(count int, status string) {
	m.peersTotal.WithLabelValues(status).Set(float64(count))
}

func (m *Metrics) AddTransactions(count int, status string) {
	m.transactionsTotal.WithLabelValues(status).Add(float64(count))
}
