# Crossref Production Deployment

This directory contains the production-oriented scaffolding for running
`crossrefd` beyond the local Docker experiment.

## Scope

- multi-validator topology with explicit chain IDs
- persistent node homes and secret files
- remote host inventory
- Prometheus scraping template
- backup and restore scripts for validator/full-node data
- operator notes for key rotation, route controller, and upgrades

The files are templates. Review addresses, peer lists, secrets, and ports before
using them on public infrastructure.

## Files

- `inventory.example.json`: remote host and chain-id inventory
- `docker-compose.validator.yml`: validator/full-node service template
- `prometheus.yml`: scrape config for CometBFT, app, and relayer controller
- `backup-crossrefd.sh`: tar/zstd backup for persistent homes
- `restore-crossrefd.sh`: guarded restore helper

## Chain ID Policy

Use stable chain IDs per environment:

- `crossref-dev-N` for disposable development networks
- `crossref-staging-N` for rehearsal networks
- `crossref-main-N` for production domains

Never reuse a production chain ID for a reset network.

## Secrets

Production deployments must keep these outside the repository:

- validator mnemonics
- priv-validator keys
- node keys
- relayer mnemonics
- domain hysteresis signing keys
- threshold signing participant keys

Mount them as read-only files or provision them through the target secret
manager. Do not pass production mnemonics through shell history.

## Monitoring

Export and alert on:

- CometBFT block height and peer count
- validator missed blocks and jail state
- IBC packet backlog per route
- relayer-controller disabled worker count
- route rebalance events
- checkpoint proof failures and accountability events

## Backup

Back up validator homes before upgrades, key rotation, and chain-id migration.
Use the helper scripts as a baseline and verify restores on staging.
