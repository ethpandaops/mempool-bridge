package source

import (
	"testing"
	"time"
)

func TestConfig_Validate_P2P(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "Valid P2P config",
			config: &Config{
				NodeRecords: []string{"enr:-IS4Q..."},
			},
			wantErr: false,
		},
		{
			name: "P2P mode with empty nodeRecords",
			config: &Config{
				NodeRecords: []string{},
			},
			wantErr: true,
		},
		{
			name:    "P2P mode with nil nodeRecords",
			config:  &Config{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate("p2p")
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_Validate_RPC(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "Valid RPC config with WebSocket",
			config: &Config{
				RPCEndpoints: []string{"ws://localhost:8546"},
			},
			wantErr: false,
		},
		{
			name: "Valid RPC config with HTTP",
			config: &Config{
				RPCEndpoints:    []string{"http://localhost:8545"},
				PollingInterval: 10 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "Valid RPC config with mixed endpoints",
			config: &Config{
				RPCEndpoints: []string{
					"ws://localhost:8546",
					"http://localhost:8545",
				},
				PollingInterval: 10 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "Invalid - empty endpoints",
			config: &Config{
				RPCEndpoints: []string{},
			},
			wantErr: true,
		},
		{
			name: "Invalid - HTTP endpoint with short polling interval",
			config: &Config{
				RPCEndpoints:    []string{"http://localhost:8545"},
				PollingInterval: 500 * time.Millisecond,
			},
			wantErr: true,
		},
		{
			name: "Invalid - unsupported protocol",
			config: &Config{
				RPCEndpoints: []string{"ftp://localhost:21"},
			},
			wantErr: true,
		},
		{
			name: "Invalid - no protocol",
			config: &Config{
				RPCEndpoints: []string{"localhost:8545"},
			},
			wantErr: true,
		},
		{
			name: "Valid - uppercase protocols",
			config: &Config{
				RPCEndpoints: []string{
					"WS://localhost:8546",
					"HTTP://localhost:8545",
				},
				PollingInterval: 10 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "Valid - secure protocols",
			config: &Config{
				RPCEndpoints: []string{
					"wss://node.example.com:8546",
					"https://node.example.com:8545",
				},
				PollingInterval: 10 * time.Second,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate("rpc")
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_Validate_UnknownMode(t *testing.T) {
	config := &Config{
		NodeRecords: []string{"enr:-IS4Q..."},
	}

	err := config.Validate("unknown")
	if err == nil {
		t.Error("Validate() with unknown mode should return error")
	}
}

func TestTransactionFilterConfig(t *testing.T) {
	filter := TransactionFilterConfig{
		To:   []string{"0x1234567890123456789012345678901234567890"},
		From: []string{"0x0987654321098765432109876543210987654321"},
	}

	if len(filter.To) != 1 {
		t.Errorf("To filter length = %d, want 1", len(filter.To))
	}

	if len(filter.From) != 1 {
		t.Errorf("From filter length = %d, want 1", len(filter.From))
	}
}
