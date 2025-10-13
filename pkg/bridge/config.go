package bridge

import (
	"errors"

	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/target"
)

// Mode defines the bridge operation mode
type Mode string

const (
	// ModeP2P uses devp2p protocol for transaction bridging
	ModeP2P Mode = "p2p"
	// ModeRPC uses JSON-RPC for transaction bridging
	ModeRPC Mode = "rpc"
)

type Config struct {
	LoggingLevel string        `yaml:"logging" default:"info"`
	MetricsAddr  string        `yaml:"metricsAddr" default:":9090"`
	Mode         Mode          `yaml:"mode" default:"p2p"`
	Source       source.Config `yaml:"source"`
	Target       target.Config `yaml:"target"`
}

func (c *Config) Validate() error {
	if c.Mode != ModeP2P && c.Mode != ModeRPC {
		return errors.New("mode must be either 'p2p' or 'rpc'")
	}

	if err := c.Source.Validate(c.Mode); err != nil {
		return err
	}

	if err := c.Target.Validate(c.Mode); err != nil {
		return err
	}

	return nil
}
