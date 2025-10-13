package target

import (
	"errors"
	"fmt"
	"time"
)

type Config struct {
	RetryInterval time.Duration `yaml:"retryInterval" default:"60s"`
	NodeRecords   []string      `yaml:"nodeRecords"`
	RPCEndpoints  []string      `yaml:"rpcEndpoints"`
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
	default:
		return fmt.Errorf("unknown mode: %s", modeStr)
	}

	return nil
}
