package bridge

import (
	"errors"

	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/target"
)

var (
	// ErrInvalidMode is returned when an invalid mode is specified
	ErrInvalidMode = errors.New("mode must be either 'p2p' or 'rpc'")
)

// Mode defines the bridge operation mode
type Mode string

const (
	// ModeP2P uses devp2p protocol for transaction bridging
	ModeP2P Mode = "p2p"
	// ModeRPC uses JSON-RPC for transaction bridging
	ModeRPC Mode = "rpc"
)

// Config holds the bridge configuration.
type Config struct {
	LoggingLevel string        `yaml:"logging" default:"info"`
	MetricsAddr  string        `yaml:"metricsAddr" default:":9090"`
	Mode         Mode          `yaml:"mode" default:"p2p"`
	Source       source.Config `yaml:"source"`
	Target       target.Config `yaml:"target"`
}

// Validate validates the bridge configuration.
func (c *Config) Validate() error {
	if c.Mode != ModeP2P && c.Mode != ModeRPC {
		return ErrInvalidMode
	}

	if err := c.Source.Validate(c.Mode); err != nil {
		return err
	}

	return c.Target.Validate(c.Mode)
}
