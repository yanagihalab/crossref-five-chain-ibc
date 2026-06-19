# Crossref Threat Model and Accountability

## Assets

- domain registration and chain-id binding
- hysteresis public keys and key epochs
- checkpoint state chain `H(S_n-1) -> H(S_n)`
- IBC channel bindings and packet delivery
- source checkpoint ICS23 proofs
- consensus proof envelope
- relayer route assignments

## Threats and Controls

| Threat | Control |
| --- | --- |
| Malicious domain registration | domain admin is stored on first registration; updates require the same admin |
| Unauthorized signing key replacement | `key_epoch` is monotonic and cannot roll back; admin mismatch is rejected |
| Invalid hysteresis signature | signature verifies strict `previous_state_hash` and `state_hash` bytes |
| Forked checkpoint chain | latest `state_hash` must equal next `previous_state_hash` |
| Replay packet | cross-reference records are unique per local/remote/height |
| Invalid source checkpoint | ICS23 membership proof is verified against the bound channel client |
| Stale proof | proof height must be inside `checkpoint_proof_max_lag` |
| Invalid consensus evidence | optional consensus proof envelope must match block/app/validator/state hashes |
| Relayer worker outage | `relayer-controller.mjs` detects unhealthy workers and rebalances routes |
| Proof spam / DoS | params expose proof spam window/failure thresholds; accountability events record failures |

## Accountability Events

The module stores `AccountabilityEvent` records for rejected IBC packets such as:

- `invalid_source_checkpoint_proof`
- `invalid_consensus_proof`
- `invalid_checkpoint_chain`
- `invalid_hysteresis_signature`
- `replay_packet`

These records are intentionally separate from slashing. A production chain can
connect them to governance, slashing-equivalent sanctions, relayer reputation,
or off-chain incident response.

## Remaining Production Extensions

The current consensus proof is an upgradeable envelope. Full light-client-grade
checkpoint proof should verify CometBFT commit signatures, validator-set
transitions, and trusted header ancestry, or rely directly on the IBC light
client state for the source chain.
