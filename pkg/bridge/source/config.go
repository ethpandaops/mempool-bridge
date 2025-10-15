// Package source provides transaction source implementations for the bridge.
package source

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrNodeRecordsRequired is returned when nodeRecords is empty for p2p mode
	ErrNodeRecordsRequired = errors.New("nodeRecords is required for p2p mode")
	// ErrRPCEndpointsRequired is returned when rpcEndpoints is empty for rpc mode
	ErrRPCEndpointsRequired = errors.New("rpcEndpoints is required for rpc mode")
	// ErrInvalidPollingInterval is returned when pollingInterval is too short
	ErrInvalidPollingInterval = errors.New("pollingInterval must be at least 1 second for HTTP endpoints")
	// ErrUnsupportedProtocol is returned when an RPC endpoint uses an unsupported protocol
	ErrUnsupportedProtocol = errors.New("rpc endpoint must use a supported protocol (ws://, wss://, http://, https://)")
	// ErrUnknownMode is returned when an unknown mode is specified
	ErrUnknownMode = errors.New("unknown mode")
)

// Config holds the source configuration.
type Config struct {
	RetryInterval      time.Duration           `yaml:"retryInterval" default:"60s"`
	NodeRecords        []string                `yaml:"nodeRecords"`
	RPCEndpoints       []string                `yaml:"rpcEndpoints"`
	PollingInterval    time.Duration           `yaml:"pollingInterval" default:"10s"`
	TransactionFilters TransactionFilterConfig `yaml:"transactionFilters"`
}

// TransactionFilterConfig defines transaction filtering rules.
type TransactionFilterConfig struct {
	To   []string `yaml:"to"`
	From []string `yaml:"from"`
}

// Validate validates the source configuration.
func (c *Config) Validate(mode any) error {
	modeStr, ok := mode.(string)
	if !ok {
		// Try to get the string representation for the Mode type
		if m, ok := mode.(fmt.Stringer); ok {
			modeStr = m.String()
		} else {
			modeStr = fmt.Sprintf("%v", mode)
		}
	}

	switch modeStr {
	case "p2p":
		if len(c.NodeRecords) == 0 {
			return ErrNodeRecordsRequired
		}
	case "rpc":
		if len(c.RPCEndpoints) == 0 {
			return ErrRPCEndpointsRequired
		}

		// Validate RPC endpoints format - now supports both WebSocket and HTTP
		hasHTTP := false

		for _, endpoint := range c.RPCEndpoints {
			lowered := strings.ToLower(endpoint)

			switch {
			case strings.HasPrefix(lowered, "ws://"), strings.HasPrefix(lowered, "wss://"):
				continue // WebSocket endpoint - valid
			case strings.HasPrefix(lowered, "http://"), strings.HasPrefix(lowered, "https://"):
				hasHTTP = true
			default:
				return fmt.Errorf("%w: %s", ErrUnsupportedProtocol, endpoint)
			}
		}

		// Validate polling interval if HTTP endpoints are present
		if hasHTTP {
			// HTTP endpoints will use polling mode
			if c.PollingInterval < 1*time.Second {
				return ErrInvalidPollingInterval
			}
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnknownMode, modeStr)
	}

	return nil
}
