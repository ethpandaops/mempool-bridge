# Mempool Bridge

Bridge mempool transactions between execution layer nodes.

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

## Contact

Andrew - [@savid](https://twitter.com/Savid)

Sam - [@samcmau](https://twitter.com/samcmau)
