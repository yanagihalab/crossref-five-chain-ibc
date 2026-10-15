# Validator and Relayer Startup

The complete [Japanese startup guide](NODE-STARTUP.ja.md) covers prerequisites, exact commands,
12 Validators on independent hosts, one or three dedicated Relayers, and offline restore auditing.
See the [32-advisory remediation record](SECURITY-REMEDIATION-20261014.ja.md) and
[machine-readable results](../deploy/recovery/security-remediation-20261014.json).

Use Go 1.26.9 or newer and Node 22. Before building:

```bash
make security-deps
go vet -mod=readonly -p 2 ./...
go test -mod=readonly -p 2 ./...
make govulncheck
```

`make security-deps` verifies the upstream SDK and prepares its single maintained armor import
change in ignored `.security-deps`. Source and deployed Linux binaries must both pass security checks.
Module-only advisories remain separately documented; this is not a Rust Hermes security audit.

For a **new** network, use `generate-inventory.mjs`, review your own new hosts, then the guarded
`start-network.mjs plan`, `prepare-fresh`, deployment dry-run, deployment and `start-fresh` commands
in the full guide. Three-Relayer operation additionally requires independent funded identities,
manually reviewed R2/R3 deployments and exclusive packet filters. Example addresses are not live targets.

For a **retained** chain, never use `init`, `prepare-fresh` or unsafe reset. Restore the database,
genesis, keys and signing state together from one verified snapshot. Verify that every other copy
with the same signing identity is stopped. `audit-home` is read-only, but is not full database
verification or authorization to upgrade old consensus state.

Generated homes, mnemonics, test keyrings, SSH keys and real inventories must stay outside Git.
This public source distribution does not contain operational data, private repository history,
provider accounts, backup locations or credentials. Tests use synthetic fixtures and mock SSH;
they do not claim a live consensus or packet-delivery test on newly provisioned hosts.
