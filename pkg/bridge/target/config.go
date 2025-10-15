// Package target provides transaction target implementations for the bridge.
package target

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrNodeRecordsRequired is returned when nodeRecords is empty for p2p mode
	ErrNodeRecordsRequired = errors.New("nodeRecords is required for p2p mode")
	// ErrRPCEndpointsRequired is returned when rpcEndpoints is empty for rpc mode
	ErrRPCEndpointsRequired = errors.New("rpcEndpoints is required for rpc mode")
	// ErrUnknownMode is returned when an unknown mode is specified
	ErrUnknownMode = errors.New("unknown mode")
)

// Config holds the target configuration.
type Config struct {
	RetryInterval   time.Duration `yaml:"retryInterval" default:"60s"`
	NodeRecords     []string      `yaml:"nodeRecords"`
	RPCEndpoints    []string      `yaml:"rpcEndpoints"`
	SendConcurrency int           `yaml:"sendConcurrency" default:"10"`
}

// Validate validates the target configuration.
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
	default:
		return fmt.Errorf("%w: %s", ErrUnknownMode, modeStr)
	}

	return nil
}
