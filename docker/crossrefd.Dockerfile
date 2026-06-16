FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates bash curl \
    && rm -rf /var/lib/apt/lists/*

COPY build/crossrefd-linux-arm64 /usr/local/bin/crossrefd
COPY docker/scripts /opt/crossref/scripts

RUN chmod +x /usr/local/bin/crossrefd /opt/crossref/scripts/*.sh

CMD ["crossrefd", "--help"]
