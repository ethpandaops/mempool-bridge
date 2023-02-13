FROM gcr.io/distroless/static-debian11:latest
COPY mempool-bridge* /mempool-bridge
ENTRYPOINT ["/mempool-bridge"]
