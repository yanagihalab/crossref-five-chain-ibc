# Crossref Five-Chain IBC

Crossref Five-Chain IBC is a Cosmos SDK prototype for a cross-reference blockchain system. It implements a custom `x/crossref` module, exposes it as an IBC application module, and includes a Docker-based five-chain experiment driven by one Hermes relayer.

Japanese documentation is available in [README.ja.md](README.ja.md).

## What This Repository Contains

- `x/crossref`: Cosmos SDK module for domain registration, checkpoint storage, channel binding, cross-reference packet send/receive, ICS23 proof verification, and hysteresis signature verification.
- `proto/crossrefd/crossref/v1`: protobuf definitions for messages, queries, genesis state, packet data, and module types.
- `app`: application wiring for the `crossrefd` chain.
- `docker`: five isolated `crossrefd` chains plus one Hermes relayer.
- `docker/scripts/run-crossref-experiment.sh`: end-to-end experiment that opens a five-chain full mesh, submits checkpoints, collects ICS23 proofs, broadcasts packets, and verifies received cross-references.
- `docker/scripts/generate-topology.mjs`: generator for `N` chains and `M` relayer workers, including per-worker Hermes packet filters.
- `docker/scripts/run-matrix-test.sh`: static matrix test for 3-chain, 5-chain, and arbitrary `N/M` generated topologies.

## Architecture

The prototype models five domains:

- `chain-a`
- `chain-b`
- `chain-c`
- `chain-d`
- `chain-e`

Hermes opens pairwise `crossref/crossref` IBC channels for every unordered pair. This produces 10 pairwise channel connections and 20 directed cross-reference routes. Each source chain can submit its own checkpoint and broadcast that checkpoint to the other four chains.

The receiver validates:

1. the packet source domain matches the registered source chain;
2. the packet arrived through the authorized local channel;
3. the checkpoint hash is internally consistent;
4. the previous checkpoint hash matches the latest known checkpoint when applicable;
5. the source checkpoint exists on the source chain through an IBC light-client verified ICS23 store proof;
6. the hysteresis signature is valid when the source domain registered a hysteresis public key.

## Crossref Module

The module currently supports:

- `RegisterDomain`: register a domain ID, chain ID, validator set hash, metadata URI, and optional Ed25519 hysteresis public key.
- `BindDomainChannel`: bind a local domain and remote domain pair to an IBC port/channel.
- `SubmitCheckpoint`: store a local checkpoint after hash and optional hysteresis signature verification.
- `SendCrossReferencePacket`: send one checkpoint to a bound remote domain.
- `BroadcastCrossReferencePacket`: send one source checkpoint to all bound remote domains.
- `ReceiveCrossReferencePacket`: receive and verify a checkpoint packet, including ICS23 source-store proof validation.
- query endpoints for domains, channels, checkpoints, cross-references, and checkpoint proof export.

## Hysteresis Signature Verification

Domains may register an Ed25519 `hysteresis_public_key`.

When a domain has this key, both local checkpoint submission and IBC packet receive require `hysteresis_signature`. The signature is verified against the checkpoint hash. The checkpoint hash commits to the current block/app state and the previous checkpoint hash, so the checkpoint chain remains tamper-evident.

Domains without `hysteresis_public_key` are still accepted for local experiments and migration compatibility.

The five-chain Docker experiment registers these keys for all domains and sends
signed checkpoints, so the end-to-end IBC path exercises signature verification.

The module also supports an experimental threshold signature encoding in the existing `hysteresis_public_key` and `hysteresis_signature` byte fields. This keeps the wire format compatible while allowing `t-of-n` Ed25519 verification. `RegisterDomain` may be called again with the same `domain_id` and `chain_id` to rotate the hysteresis key. Receivers also reject replayed cross-reference records and stale checkpoint proofs.

## Requirements

- macOS or Linux shell
- Go toolchain compatible with the generated Cosmos SDK app
- Docker Desktop or Docker Engine with Docker Compose
- Internet access for Docker image pulls and Go dependency resolution when the module cache is empty

## Quick Start

From the repository root:

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ./build/crossrefd-linux-arm64 ./cmd/crossrefdd
docker compose -f docker/docker-compose.yml up -d --build
docker/scripts/run-crossref-experiment.sh
```

Success ends with:

```text
Five-chain cross-reference experiment passed.
```

For the detailed Docker experiment guide, see [docker/README.md](docker/README.md).

## Visualizer

The repository includes a browser-based visualizer for the crossref tests and
the five-chain broadcast route map:

```bash
open visualizer/crossref-test-visualizer.html
```

The visualizer shows:

- keeper and IBC behavior tests;
- the one-relayer network topology;
- `route-00` to `route-19` directed packet flow;
- source-chain checkpoint proof verification;
- animated packet movement through the Hermes relayer.

Run the visualizer smoke test from the repository root:

```bash
node visualizer/verify-visualizer.mjs
```

Generate visualizer JSON from an experiment log:

```bash
docker/scripts/run-crossref-experiment.sh | tee /tmp/crossref-experiment.log
node visualizer/generate-from-log.mjs /tmp/crossref-experiment.log visualizer/test-results.json
```

## Useful Commands

Run Go tests:

```bash
go test -count=1 ./...
```

Regenerate protobuf code:

```bash
make proto-gen
```

Start the five-chain Docker network:

```bash
docker compose -f docker/docker-compose.yml up -d --build
```

Start the same network with multiple relayer workers:

```bash
docker compose -f docker/docker-compose.yml up -d --build --scale relayer=3
```

Generate an arbitrary topology with route assignment across relayer workers:

```bash
node docker/scripts/generate-topology.mjs 6 4
docker compose -f docker/generated/docker-compose-6c-4r.yml config --quiet
```

Run the topology matrix smoke test:

```bash
RUN_DOCKER_MATRIX=0 MATRIX="3:2 5:3 6:4" docker/scripts/run-matrix-test.sh
```

Run the full experiment:

```bash
docker/scripts/run-crossref-experiment.sh
```

Run the experiment from a generated topology:

```bash
node docker/scripts/generate-topology.mjs 3 2
COMPOSE_FILE=docker/generated/docker-compose-3c-2r.yml \
TOPOLOGY_FILE=docker/generated/topology-3c-2r.json \
docker/scripts/run-crossref-experiment.sh
```

Check topology parsing without touching Docker:

```bash
DRY_RUN=1 TOPOLOGY_FILE=docker/generated/topology-3c-2r.json docker/scripts/run-crossref-experiment.sh
```

Stop and remove chain state:

```bash
docker compose -f docker/docker-compose.yml down -v
```

## Local Endpoints

| Chain | RPC | gRPC | REST |
| --- | --- | --- | --- |
| A | `http://localhost:26657` | `localhost:9090` | `http://localhost:1317` |
| B | `http://localhost:26667` | `localhost:9091` | `http://localhost:1318` |
| C | `http://localhost:26677` | `localhost:9092` | `http://localhost:1319` |
| D | `http://localhost:26687` | `localhost:9093` | `http://localhost:1320` |
| E | `http://localhost:26697` | `localhost:9094` | `http://localhost:1321` |

## Current Prototype Scope

This repository is an experimental implementation. It is intended for local verification of the cross-reference design, IBC application behavior, proof validation, and multi-chain routing. The current hardening layer covers ICS23 proof membership, proof freshness, replay rejection, key rotation, and experimental threshold signatures. Production work still needs operational key management, chain upgrade policy, full threat modeling, relayer operations, and a stricter hysteresis signature format if the exact paper definition `H(S_n-1)` must be represented separately from the checkpoint hash.
