package target

import (
	"errors"
	"testing"
	"time"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		config  *Config
		wantErr bool
	}{
		{
			name: "Valid P2P config",
			mode: "p2p",
			config: &Config{
				NodeRecords:   []string{"enr:-IS4Q..."},
				RetryInterval: 30 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "P2P mode with empty nodeRecords",
			mode: "p2p",
			config: &Config{
				RetryInterval: 30 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "Valid RPC config",
			mode: "rpc",
			config: &Config{
				RPCEndpoints:  []string{"http://localhost:8545"},
				RetryInterval: 30 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "RPC mode with empty rpcEndpoints",
			mode: "rpc",
			config: &Config{
				RetryInterval: 30 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "Invalid - zero retryInterval",
			mode: "p2p",
			config: &Config{
				NodeRecords: []string{"enr:-IS4Q..."},
			},
			wantErr: true,
		},
		{
			name: "Invalid - negative retryInterval",
			mode: "rpc",
			config: &Config{
				RPCEndpoints:  []string{"http://localhost:8545"},
				RetryInterval: -1 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "Unknown mode",
			mode: "unknown",
			config: &Config{
				RetryInterval: 30 * time.Second,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate(tt.mode)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	t.Run("unknown mode returns ErrUnknownMode", func(t *testing.T) {
		config := &Config{RetryInterval: 30 * time.Second}

		err := config.Validate("unknown")
		if !errors.Is(err, ErrUnknownMode) {
			t.Errorf("Validate() error = %v, want %v", err, ErrUnknownMode)
		}
	})
}
