package source

import (
	"errors"
	"time"
)

type Config struct {
	RetryInterval      time.Duration           `yaml:"retryInterval" default:"60s"`
	NodeRecords        []string                `yaml:"nodeRecords"`
	TransactionFilters TransactionFilterConfig `yaml:"transactionFilters"`
}

type TransactionFilterConfig struct {
	To   []string `yaml:"to"`
	From []string `yaml:"from"`
}

func (c *Config) Validate() error {
	if len(c.NodeRecords) == 0 {
		return errors.New("nodeRecords is required")
	}

	return nil
}
