FROM debian:latest
COPY mempool-bridge* /mempool-bridge
ENTRYPOINT ["/mempool-bridge"]
