package source

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Config struct {
	RetryInterval      time.Duration           `yaml:"retryInterval" default:"60s"`
	NodeRecords        []string                `yaml:"nodeRecords"`
	RPCEndpoints       []string                `yaml:"rpcEndpoints"`
	TransactionFilters TransactionFilterConfig `yaml:"transactionFilters"`
}

type TransactionFilterConfig struct {
	To   []string `yaml:"to"`
	From []string `yaml:"from"`
}

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
			return errors.New("nodeRecords is required for p2p mode")
		}
	case "rpc":
		if len(c.RPCEndpoints) == 0 {
			return errors.New("rpcEndpoints is required for rpc mode")
		}

		// Validate RPC endpoints format
		for _, endpoint := range c.RPCEndpoints {
			if !strings.HasPrefix(endpoint, "ws://") && !strings.HasPrefix(endpoint, "wss://") {
				return fmt.Errorf("rpc endpoint %s must use WebSocket protocol (ws:// or wss://)", endpoint)
			}
		}
	default:
		return fmt.Errorf("unknown mode: %s", modeStr)
	}

	return nil
}
