package bridge

import (
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/source"
	"github.com/ethpandaops/mempool-bridge/pkg/bridge/target"
)

type Config struct {
	LoggingLevel string        `yaml:"logging" default:"info"`
	MetricsAddr  string        `yaml:"metricsAddr" default:":9090"`
	Source       source.Config `yaml:"source"`
	Target       target.Config `yaml:"target"`
}

func (c *Config) Validate() error {
	return nil
}
