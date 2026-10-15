FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl jq \
    && rm -rf /var/lib/apt/lists/*
COPY crossrefd /usr/local/bin/crossrefd
COPY start-node.sh /opt/start-node.sh
RUN chmod 0755 /usr/local/bin/crossrefd
ENTRYPOINT ["/bin/sh", "/opt/start-node.sh"]
