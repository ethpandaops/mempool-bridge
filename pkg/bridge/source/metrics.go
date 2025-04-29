package source

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	peersTotal *prometheus.GaugeVec

	// Transaction hash type counters
	newTxHashesTotal *prometheus.CounterVec

	// Transaction response type counters
	receivedTxTotal *prometheus.CounterVec
}

func NewMetrics(namespace string) *Metrics {
	m := &Metrics{
		peersTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "peers_total",
			Help:      "Total number of peers",
		}, []string{"status"}),

		newTxHashesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "new_tx_hashes_total",
			Help:      "Total number of new transaction hashes by type",
		}, []string{"type"}),

		receivedTxTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "received_tx_total",
			Help:      "Total number of received transactions by type",
		}, []string{"type"}),
	}

	prometheus.MustRegister(m.peersTotal)
	prometheus.MustRegister(m.newTxHashesTotal)
	prometheus.MustRegister(m.receivedTxTotal)

	return m
}

func (m *Metrics) SetPeers(count int, status string) {
	m.peersTotal.WithLabelValues(status).Set(float64(count))
}

func (m *Metrics) IncNewTxHashesCount(txType byte) {
	m.newTxHashesTotal.WithLabelValues(getTransactionTypeString(txType)).Inc()
}

func (m *Metrics) IncReceivedTxCount(txType byte) {
	m.receivedTxTotal.WithLabelValues(getTransactionTypeString(int(txType))).Inc()
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
