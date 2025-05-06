package target

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	peersTotal        *prometheus.GaugeVec
	transactionsTotal *prometheus.CounterVec
	// Transaction type counters
	transactionsByType *prometheus.CounterVec
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
		transactionsByType: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "transactions_by_type_total",
			Help:      "Total number of transactions by type",
		}, []string{"type", "status"}),
	}

	prometheus.MustRegister(m.peersTotal)
	prometheus.MustRegister(m.transactionsTotal)
	prometheus.MustRegister(m.transactionsByType)

	return m
}

func (m *Metrics) SetPeers(count int, status string) {
	m.peersTotal.WithLabelValues(status).Set(float64(count))
}

func (m *Metrics) AddTransactions(count int, status string) {
	m.transactionsTotal.WithLabelValues(status).Add(float64(count))
}

func (m *Metrics) AddTransactionByType(txType uint8, status string) {
	m.transactionsByType.WithLabelValues(getTransactionTypeString(int(txType)), status).Inc()
}

// getTransactionTypeString converts transaction type to a string representation
func getTransactionTypeString(txType interface{}) string {
	var typeStr string

	switch v := txType.(type) {
	case byte:
		switch v {
		case 0x00: // LegacyTxType
			typeStr = "legacy"
		case 0x01: // AccessListTxType
			typeStr = "access_list"
		case 0x02: // DynamicFeeTxType
			typeStr = "dynamic_fee"
		case 0x03: // BlobTxType
			typeStr = "blob"
		case 0x04: // SetCodeTxType
			typeStr = "set_code"
		default:
			typeStr = "unknown"
		}
	case int:
		switch v {
		case 0x00: // LegacyTxType
			typeStr = "legacy"
		case 0x01: // AccessListTxType
			typeStr = "access_list"
		case 0x02: // DynamicFeeTxType
			typeStr = "dynamic_fee"
		case 0x03: // BlobTxType
			typeStr = "blob"
		case 0x04: // SetCodeTxType
			typeStr = "set_code"
		default:
			typeStr = "unknown"
		}
	default:
		typeStr = "unknown"
	}

	return typeStr
}
