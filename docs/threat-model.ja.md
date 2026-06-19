# Crossref Threat Model and Accountability

## 保護対象

- domain 登録と chain-id binding
- hysteresis public key と key epoch
- checkpoint state chain `H(S_n-1) -> H(S_n)`
- IBC channel binding と packet delivery
- source checkpoint ICS23 proof
- consensus proof envelope
- relayer route assignment

## 脅威と対策

| 脅威 | 対策 |
| --- | --- |
| 悪意ある domain 登録 | 初回登録時に domain admin を保存し、更新時は同じ admin を要求する |
| 不正な署名鍵置換 | `key_epoch` は単調増加であり、rollback を拒否する |
| 不正な hysteresis signature | `previous_state_hash` と `state_hash` を含む厳密な署名 bytes を検証する |
| checkpoint chain の fork | 最新 `state_hash` と次の `previous_state_hash` の一致を要求する |
| replay packet | local / remote / height ごとの cross-reference record を一意にする |
| 不正な source checkpoint | bound channel client に対して ICS23 membership proof を検証する |
| stale proof | proof height が `checkpoint_proof_max_lag` 内であることを要求する |
| 不正な consensus evidence | 任意の consensus proof envelope が block / app / validator / state hash と一致することを検証する |
| relayer worker outage | `relayer-controller.mjs` が unhealthy worker を検知し route を再配置する |
| proof spam / DoS | params に proof spam window / failure threshold を持たせ、failure を accountability event として記録する |

## Accountability Event

module は以下のような拒否 packet を `AccountabilityEvent` として保存する。

- `invalid_source_checkpoint_proof`
- `invalid_consensus_proof`
- `invalid_checkpoint_chain`
- `invalid_hysteresis_signature`
- `replay_packet`

これは slashing そのものではない。production chain では governance、slashing 相当の sanction、relayer reputation、off-chain incident response に接続できる設計である。

## 残る production extension

現在の consensus proof は upgrade 可能な envelope である。完全な light-client 級 checkpoint proof では、CometBFT commit signature、validator set transition、trusted header ancestry を検証するか、source chain の IBC light client state を直接利用する必要がある。
