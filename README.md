# Mempool Bridge

Bridge mempool transactions between execution layer nodes using either devp2p (P2P) or JSON-RPC protocols.

## Features

- **Dual Mode Operation**: Support for both P2P (devp2p) and RPC (JSON-RPC) modes
- **P2P Mode**: Direct peer-to-peer communication using ENR/enode records
- **RPC Mode**: WebSocket subscriptions and HTTP/WebSocket transaction sending
- **Transaction Filtering**: Optional filtering by to/from addresses
- **Metrics**: Prometheus metrics for monitoring
- **Automatic Retry**: Configurable retry intervals for failed connections

## Getting Started

### Download a release
Download the latest release from the [Releases page](https://github.com/ethpandaops/mempool-bridge/releases). Extract and run with:
```
./mempool-bridge --config your-config.yaml
```

### Docker
Available as a docker image at [ethpandaops/mempool-bridge](https://hub.docker.com/r/ethpandaops/mempool-bridge/tags)

#### Images
- `latest` - distroless, multiarch
- `latest-debian` - debian, multiarch
- `$version` - distroless, multiarch, pinned to a release (i.e. `0.4.0`)
- `$version-debian` - debian, multiarch, pinned to a release (i.e. `0.4.0-debian`)

### Kubernetes via Helm
[Read more](https://github.com/ethpandaops/ethereum-helm-charts/tree/master/charts/mempool-bridge)
```
helm repo add ethereum-helm-charts https://ethpandaops.github.io/ethereum-helm-charts

helm install mempool-bridge ethereum-helm-charts/mempool-bridge -f your_values.yaml
```

## Configuration

### Modes

Mempool Bridge supports two operation modes:

#### P2P Mode (devp2p)
Uses Ethereum's devp2p protocol for direct peer-to-peer communication.

**Source**: Connects to nodes via ENR/enode records and listens for:
- `OnNewPooledTransactionHashes`: Receives transaction hashes
- `OnTransactions`: Receives full transactions
- Fetches transaction details via `GetPooledTransactions`

**Target**: Sends transactions directly to peers via devp2p

**Example configuration**: See `example_config_p2p.yaml`

#### RPC Mode (JSON-RPC)
Uses JSON-RPC for transaction bridging via WebSocket subscriptions and HTTP/WebSocket endpoints.

**Source**:
- Connects to WebSocket RPC endpoints (ws:// or wss://)
- Subscribes to `newPendingTransactions` via `eth_subscribe`
- Fetches transaction details via `eth_getTransactionByHash`

**Target**:
- Connects to HTTP or WebSocket RPC endpoints
- Sends transactions via `eth_sendRawTransaction`

**Example configuration**: See `example_config_rpc.yaml`

### Configuration Files

Three example configurations are provided:

1. **example_config_p2p.yaml**: P2P mode configuration
2. **example_config_rpc.yaml**: RPC mode configuration
3. **example_config_mixed.yaml**: Shows both P2P and RPC configurations (only one mode active at a time)

### Key Configuration Options

```yaml
logging: "info"              # Log level: panic, fatal, warn, info, debug, trace
metricsAddr: ":9090"         # Prometheus metrics endpoint
mode: "p2p"                  # Operation mode: "p2p" or "rpc"

source:
  retryInterval: 30s         # Retry interval for failed connections

  # P2P mode: ENR/enode records
  nodeRecords:
    - enr:-IS4Q...
    - enode://dd47aff...

  # RPC mode: WebSocket endpoints (ws:// or wss:// required for subscriptions)
  rpcEndpoints:
    - ws://localhost:8546

  # Optional: Filter transactions by to/from addresses
  transactionFilters:
    to:
      - 0x0000000000000000000000000000000000000000
    from:
      - 0x2222222222222222222222222222222222222222

target:
  retryInterval: 30s

  # P2P mode: ENR/enode records
  nodeRecords:
    - enr:-IS4Q...

  # RPC mode: HTTP or WebSocket endpoints
  rpcEndpoints:
    - http://localhost:8545
```

### Mode Selection

Set the `mode` field to choose between P2P and RPC:
- `mode: "p2p"` - Uses `nodeRecords` for both source and target
- `mode: "rpc"` - Uses `rpcEndpoints` for both source and target

**Note**: Source RPC endpoints must use WebSocket protocol (ws:// or wss://) for real-time transaction subscriptions. Target RPC endpoints can use either HTTP or WebSocket.

## Architecture

### P2P Mode Flow
```
Source Nodes (ENR/enode) → devp2p → Source Coordinator → Bridge → Target Coordinator → devp2p → Target Nodes
```

### RPC Mode Flow
```
Source Nodes (WebSocket) → eth_subscribe → Source Coordinator → Bridge → Target Coordinator → eth_sendRawTransaction → Target Nodes
```

## Metrics

Prometheus metrics are exposed on the configured `metricsAddr` (default: `:9090`).

Key metrics include:
- Connection status (connected/disconnected peers)
- Transaction counts by type (legacy, access_list, dynamic_fee, blob, set_code)
- Success/failure rates
- Transaction processing rates

## Contact

Andrew - [@savid](https://twitter.com/Savid)

Sam - [@samcmau](https://twitter.com/samcmau)
