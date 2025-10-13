# RPC Mode Implementation Summary

## Overview

Successfully extended the mempool-bridge application to support **two distinct modes** for bridging Ethereum mempool transactions:

1. **P2P Mode** (existing): Uses devp2p protocol with ENR/enode records
2. **RPC Mode** (new): Uses JSON-RPC with WebSocket subscriptions and HTTP/WebSocket endpoints

## Implementation Architecture

### Mode Selection
- Added `mode` field to configuration: `"p2p"` or `"rpc"`
- Factory pattern in bridge to instantiate appropriate coordinators
- Interface-based design for polymorphic coordinator handling

### RPC Source Implementation

**Location**: `pkg/bridge/source/rpc/`

**Files**:
- `peer.go`: RPC source peer implementation
- `coordinator.go`: RPC source coordinator

**Functionality**:
1. Connects to Ethereum nodes via WebSocket (`ws://` or `wss://`)
2. Subscribes to `newPendingTransactions` using `eth_subscribe`
3. Receives transaction hashes in real-time
4. Fetches full transaction data via `eth_getTransactionByHash`
5. Applies transaction filters (to/from addresses)
6. Forwards to broadcast handler
7. Separate queues for normal and blob transactions (priority handling)

**Key Features**:
- Automatic reconnection with retry logic
- Duplicate transaction detection via shared cache
- Metrics tracking for transaction types
- Graceful shutdown handling

### RPC Target Implementation

**Location**: `pkg/bridge/target/rpc/`

**Files**:
- `peer.go`: RPC target peer implementation
- `coordinator.go`: RPC target coordinator

**Functionality**:
1. Connects to Ethereum nodes via HTTP or WebSocket
2. Encodes transactions to RLP format (handled by ethclient)
3. Sends via `eth_sendRawTransaction` (via ethclient.SendTransaction)
4. Handles expected errors (already known, nonce too low, etc.)
5. Tracks success/failure metrics
6. Logs transaction status

**Error Handling**:
- Gracefully handles "already known" transactions
- Logs but doesn't fail on duplicate/underpriced transactions
- Only returns error if ALL transactions fail

### Configuration Changes

**Files Modified**:
- `pkg/bridge/config.go`: Added `Mode` type and `mode` field
- `pkg/bridge/source/config.go`: Added `RPCEndpoints` field and mode-aware validation
- `pkg/bridge/target/config.go`: Added `RPCEndpoints` field and mode-aware validation

**New Fields**:
```go
type Config struct {
    Mode         Mode          `yaml:"mode" default:"p2p"`
    // ... other fields
}

type SourceConfig struct {
    RPCEndpoints []string `yaml:"rpcEndpoints"`
    // ... other fields
}

type TargetConfig struct {
    RPCEndpoints []string `yaml:"rpcEndpoints"`
    // ... other fields
}
```

### Bridge Updates

**File**: `pkg/bridge/bridge.go`

**Changes**:
1. Added `SourceCoordinator` and `TargetCoordinator` interfaces
2. Updated `Bridge` struct to use interfaces instead of concrete types
3. Added factory functions:
   - `createSourceCoordinator(mode, config, broadcast, log)`
   - `createTargetCoordinator(mode, config, log)`
4. Mode-based coordinator instantiation

### P2P Mode (Preserved)

**No Breaking Changes**: All existing P2P functionality remains intact
- P2P coordinators updated to pass mode to validation
- Log component names updated for clarity (`source-p2p`, `target-p2p`)

## Configuration Examples

### Example 1: RPC Mode Only (`example_config_rpc.yaml`)
```yaml
mode: "rpc"

source:
  rpcEndpoints:
    - ws://localhost:8546
    - ws://node2.example.com:8546

target:
  rpcEndpoints:
    - http://localhost:8545
    - http://node2.example.com:8545
```

### Example 2: P2P Mode (Default `example_config.yaml`)
```yaml
mode: "p2p"  # or omit for default

source:
  nodeRecords:
    - enr:-IS4Q...
    - enode://dd47aff...

target:
  nodeRecords:
    - enr:-IS4Q...
```

### Example 3: Mixed Config (`example_config_mixed.yaml`)
Shows both P2P and RPC configurations with toggle via `mode` field

## Usage

### Running in RPC Mode
```bash
./mempool-bridge --config example_config_rpc.yaml
```

### Running in P2P Mode
```bash
./mempool-bridge --config example_config.yaml
```

## Technical Details

### Dependencies
- `github.com/ethereum/go-ethereum/ethclient`: Ethereum client for RPC
- `github.com/ethereum/go-ethereum/rpc`: Low-level RPC client for subscriptions

### WebSocket Requirements
- **Source**: MUST use WebSocket (`ws://` or `wss://`) for real-time subscriptions
- **Target**: Can use HTTP or WebSocket (`http://`, `https://`, `ws://`, `wss://`)

### Transaction Flow

**RPC Source Flow**:
```
WebSocket Connection
    ↓
eth_subscribe("newPendingTransactions")
    ↓
Receive Transaction Hash
    ↓
eth_getTransactionByHash
    ↓
Apply Filters
    ↓
Broadcast to Target
```

**RPC Target Flow**:
```
Receive Transactions
    ↓
ethclient.SendTransaction (handles RLP encoding)
    ↓
eth_sendRawTransaction
    ↓
Track Success/Failure
```

### Metrics

Both P2P and RPC modes expose the same metrics:
- `mempool_bridge_source_peers`: Connected/disconnected source peers
- `mempool_bridge_source_transactions_received`: Transactions received by type
- `mempool_bridge_target_peers`: Connected/disconnected target peers
- `mempool_bridge_target_transactions_sent`: Transactions sent by status and type
- `mempool_bridge_transactions`: Total transactions bridged

## Code Quality

### Standards Compliance
- Follows ethPandaOps Go standards
- Uses `logrus.FieldLogger` for logging
- Context propagation throughout
- Proper error handling and wrapping
- Interface-based design
- Concurrent-safe with proper mutex usage

### File Structure
```
pkg/bridge/
├── bridge.go                    # Main bridge with factory pattern
├── config.go                    # Mode configuration
├── source/
│   ├── coordinator.go          # P2P source coordinator
│   ├── peer.go                 # P2P source peer
│   ├── config.go               # Source config with mode validation
│   └── rpc/
│       ├── coordinator.go      # RPC source coordinator
│       └── peer.go             # RPC source peer
└── target/
    ├── coordinator.go          # P2P target coordinator
    ├── peer.go                 # P2P target peer
    ├── config.go               # Target config with mode validation
    └── rpc/
        ├── coordinator.go      # RPC target coordinator
        └── peer.go             # RPC target peer
```

### Testing Recommendations

1. **Unit Tests**: Test individual peer and coordinator functions
2. **Integration Tests**: Test end-to-end transaction bridging
3. **RPC Mode Testing**:
   - Test with local Geth/Reth nodes
   - Test WebSocket subscription handling
   - Test reconnection logic
   - Test transaction filtering
   - Test error handling (duplicate txs, nonce issues)
4. **P2P Mode Testing**: Ensure backward compatibility

### Performance Considerations

- Batch processing for normal transactions (up to 50,000)
- Single-transaction processing for blob transactions (priority)
- Configurable timeouts and queue sizes
- Shared cache to prevent duplicate transaction processing
- Concurrent sending to multiple target nodes via errgroup

## Future Enhancements

Potential improvements:
1. Hybrid mode: Support mixing P2P and RPC sources/targets
2. Advanced filtering: Gas price, transaction type, size filters
3. Rate limiting: Control transaction sending rate
4. Transaction priority: Configurable prioritization rules
5. Health checks: HTTP endpoints for liveness/readiness probes
6. Transaction replay protection: Enhanced deduplication strategies

## Troubleshooting

### Common Issues

**Issue**: "rpc endpoint must use WebSocket protocol"
- **Solution**: Ensure source RPC endpoints use `ws://` or `wss://`

**Issue**: "failed to subscribe to pending transactions"
- **Solution**: Verify node has WebSocket RPC enabled and `--ws.api=eth` flag

**Issue**: "transaction rejected (expected)"
- **Note**: This is normal for duplicate or underpriced transactions

**Issue**: Connection keeps disconnecting
- **Solution**: Check node RPC limits, increase `retryInterval` if needed

### Debug Mode
Set `logging: "debug"` in config for detailed transaction flow logs

## Summary Statistics

- **New Files Created**: 4 (2 coordinators, 2 peers for RPC mode)
- **Files Modified**: 5 (bridge, configs, P2P coordinators)
- **Example Configs**: 3 (P2P, RPC, Mixed)
- **Total Go Files**: 19 in bridge package
- **Build Status**: ✅ Successful
- **Backward Compatibility**: ✅ Preserved

## Completion Status

✅ RPC source peer with WebSocket subscriptions
✅ RPC source coordinator with retry logic
✅ RPC target peer with transaction sending
✅ RPC target coordinator with parallel sending
✅ Configuration mode selection
✅ Factory pattern bridge integration
✅ Example configurations
✅ Comprehensive documentation
✅ Successful compilation
✅ Metrics support
✅ Error handling
✅ Logging integration

**The implementation is complete and ready for testing!**
