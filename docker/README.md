# Crossref Two-Chain Docker Experiment

This starts two isolated `crossrefd` chains and a Hermes relayer that opens a
`crossref/channel-0` IBC channel between them.

Build the local daemon first:

```bash
go build -o ./build/crossrefd ./cmd/crossrefdd
```

Start the experiment network:

```bash
docker compose -f docker/docker-compose.yml up --build
```

In another terminal, send a real cross-reference packet:

```bash
docker/scripts/run-crossref-experiment.sh
```

Useful endpoints:

- Chain A RPC: `http://localhost:26657`
- Chain A gRPC: `localhost:9090`
- Chain A REST: `http://localhost:1317`
- Chain B RPC: `http://localhost:26667`
- Chain B gRPC: `localhost:9091`
- Chain B REST: `http://localhost:1318`

Reset all chain state:

```bash
docker compose -f docker/docker-compose.yml down -v
```
