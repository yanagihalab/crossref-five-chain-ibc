# Validator・Relayer起動手順

この公開版はGoノード、署名・照会用補助コード、32件の脆弱性到達可能性への対応、
新規起動gateを含む。鍵・DB・署名状態・実運用inventory・パスワード・保存先は含まない。
[32件の対応記録](SECURITY-REMEDIATION-20261014.ja.md) と
[機械可読記録](../deploy/recovery/security-remediation-20261014.json) を併せて確認する。
静的検査合格は、侵害されていないことやproduction運用の保証ではない。

## 最初に選ぶ

- **新しい実験**: 新しいchain ID・genesis・鍵を作り、Block 1から開始する。
- **保存チェーンの継続**: 同一snapshotのDB・genesis・鍵・`priv_validator_state.json`を一緒に復元する。
  `prepare-fresh`、`init`、unsafe-reset、署名状態の巻き戻しを使わない。
- **解析だけ**: バックアップ原本をreadonlyにし、別コピー上で解析する。ノードやHermesを起動しない。

## 取得・検査・build

Go 1.26.9以上、Node 22、make、Git、SSH/SCPを使う。新しいLinuxホストにはDocker Engineと
Compose、時刻同期、確認済みSSH host key、閉じたLAN/VPN、十分な容量を用意する。
RPC/gRPC/P2P/telemetryを無条件でインターネットへ公開しない。

```bash
git clone https://github.com/yanagihalab/crossref-five-chain-ibc.git
cd crossref-five-chain-ibc
make security-deps
go vet -mod=readonly -p 2 ./...
go test -mod=readonly -p 2 ./...
node --test --test-concurrency=2 deploy/network/test-*.mjs deploy/recovery/test-*.mjs
make govulncheck
mkdir -p build
go build -mod=readonly -p 2 -o build/crossrefdd ./cmd/crossrefdd
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -p 2 \
  -o build/crossrefd-linux-amd64 ./cmd/crossrefdd
make govulncheck-binary BINARY=build/crossrefd-linux-amd64
```

`make security-deps` は上流SDKのchecksumと全ファイルを検証し、armor importだけを
保守実装へ変更する。生成された `.security-deps` はGitへ追加しない。
未準備のcloneで直接buildするとlocal replaceが解決できない。
arm64ホストには同様に `GOARCH=arm64` と対応する出力名を使う。
起動gateはソースと各配置用バイナリを再検査し、配置先のSHAも照合する。
module-only報告は対応記録で別記する。Rust製Hermes本体や旧OS全体を検査したという意味ではない。
新しい脆弱性が報告された場合は検査を抑制せず、起動を止めて再評価する。

署名・照会補助プログラムが必要な場合は同じソースからbuildする。

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -p 2 -o build/history-signer-linux-amd64 ./cmd/history-signer
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -p 2 -o build/history-query-linux-amd64 ./cmd/history-query
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -p 2 -o build/tx-pool-decode-linux-amd64 ./cmd/tx-pool-decode
```

## 新規3チェーン・12 Validator・1 Relayer

1 Validator = 1 Server、各チェーン4台。Relayerは別の専用ホストにする。
同一Mac上の4プロセスを4台の独立した故障ドメインと扱わない。
新ホストの用意やproviderの契約・削除をこのツールは行わない。

```bash
umask 077
node deploy/network/generate-inventory.mjs --chains 3 --faults 1 \
  --network fresh-study --output deploy/network/generated/study/inventory.json
```

生成した13台のSSH接続先・identityFile・bindAddress・endpoint・architecture・failureDomainを、
実際に確認した**新しいホスト**へ変更する。bindAddress/endpointはLAN/VPNを使う。
identityFileは鍵のパスであり、鍵内容をJSONへ書かない。
例のIPや過去の接続先は使わない。`CROSSREF_ADMIN_PROXY` は解除する。
署名鍵のある既存ホスト、他サービスのあるホストを割り当てない。

```bash
INV=deploy/network/generated/study/inventory.json
OUT=deploy/network/generated/study
node deploy/recovery/start-network.mjs plan --inventory "$INV" --output-dir "$OUT"
node deploy/recovery/start-network.mjs prepare-fresh \
  --inventory "$INV" --output-dir "$OUT" --binary build/crossrefdd \
  --confirm-new-network fresh-study
node deploy/network/orchestrator.mjs deploy \
  --inventory "$INV" --output-dir "$OUT" --dry-run
```

`prepare-fresh` は新しい秘密鍵・test keyring・mnemonicをローカルに生成する。
**これらのgeneratedディレクトリを公開・コミットしない。** test keyringは研究専用で、
実資産を入れない。Gitのignoreは漏えい防止の完全な保証ではない。
dry-runのホスト・専用remote root・転送先をレビューする。
全新ホストでDocker/Compose/NTP/LAN/VPN/権限と既存サービス不在を確認した後だけ実行する。

```bash
node deploy/network/orchestrator.mjs deploy --inventory "$INV" --output-dir "$OUT"
node deploy/recovery/start-network.mjs start-fresh \
  --inventory "$INV" --output-dir "$OUT" --confirm-new-network fresh-study
```

配置済みの空home・genesis・鍵・署名状態・バイナリSHAを全ホストで照合してから
Validatorを起動する。block進行、同期、実OPEN channelの確認後にRelayerを起動する。
失敗時は後続Relayerを起動せず、部分起動を自動停止・巻き戻し・再試行しない。
ログの最後の完了段階と実プロセスを調べ、同じ署名鍵を二重起動しない。

## 3 Relayerへ拡張する場合

3台はValidatorと重ならない専用ホストにし、独立した3つの資金済みアカウントを使う。
`deploy/network/multi-relayer.example.json` をignoredの
`deploy/network/multi-relayer.json` へコピーして新ホストの情報を設定する。
例の `192.0.2.*` は文書用アドレスであり、そのまま接続しない。
R1はinventoryの専用Relayerと同じホストにする。

**初回prepareの前に** `plan` と `prepare-fresh` に
`--multi-relayer-config deploy/network/multi-relayer.json` を追加する。
prepareは独立3アカウントをgenesisで資金化し、
`prepared/relayer.mnemonic`・`relayer-2.mnemonic`・`relayer-3.mnemonic` を生成する。
既存の1 Relayer用prepared世代を上書きして3台用に変更しない。

標準deployが配置するのは12 ValidatorとR1である。R2/R3は、起動せずに次を別途配置する。

1. 新しい専用workspaceへCompose、Hermes設定、scripts、空keyringディレクトリを置く。
   R1と同じ検証済みHermes image digestをpinし、Composeのproject/workspace/serviceを設定JSONと一致させる。
2. 各自のmnemonicだけを安全なSSH/SCP経路で転送し、対応するHermesアカウントとしてimportする。
   R1の鍵をR2/R3へコピーしない。秘密をargvやログへ直接書かない。
3. 新しい各チェーンのRPC/gRPC、client、OPEN channel/connection/counterparty、funding、
   sequenceを確認する。`packet_filter`の担当は3台で排他的にし、全対象channelを一度ずつ覆う。
4. Compose、keyring、権限、担当、停止中の監督プロセスを実確認した後だけ、
   各 `hostConfigurationVerified` と `hermes.ready` をtrueにする。
   フラグだけを変更して未配置ホストを検証済みにしない。

```bash
node deploy/network/manage-relayers.mjs list --config deploy/network/multi-relayer.json
node deploy/network/manage-relayers.mjs check --config deploy/network/multi-relayer.json
node deploy/recovery/start-network.mjs start-fresh \
  --inventory "$INV" --output-dir "$OUT" --confirm-new-network fresh-study \
  --multi-relayer-config deploy/network/multi-relayer.json
node deploy/network/manage-relayers.mjs status --config deploy/network/multi-relayer.json
```

このlauncherは3台の事前checkを行うが、R2/R3の自動配備や排他packet_filterの自動証明はしない。
実配備の個別確認を省略しない。過去のcontroller/run-once記録を解除したり、旧Txを自動再送しない。

## 保存チェーンを継続する場合

1. 同じValidator鍵を持つ元ノードと全コピー、Hermes、controller/watchdog/poolの停止を確認する。
   IP変更だけでは二重署名防止にならない。
2. 検証済みバックアップから同一snapshotの**最新running home**を別の空targetへ復元する。
   原本repository/imageへbackup、prune、unlock、書換えをしない。
3. DB全量検証、停止時height/block hash/app hash、genesis、鍵、署名状態の一致を確認する。
   height 0のprepared templateは最新DBの代わりではない。
4. 元の検証済みmanifestからcheckpoint JSONを別途作る。復元後ファイルから期待値を自己生成しない。
5. 新ホストのOS/UID/GID/ACL/xattr、時刻、容量、binary SHA、network設定を確認する。
   旧 `/etc`・SSH/firewall・netplanを新OSへ丸ごと適用しない。

```bash
node deploy/recovery/start-network.mjs audit-home \
  --home "$RESTORED_RUNNING_HOME" --expected-state "$RETAINED_CHECKPOINT_JSON"
```

checkpointは `chainId`、10進文字列の`signingHeight`、相対名からSHA256への
`fileSHA256`を持つ。対象6ファイルは `config/genesis.json`、`config/priv_validator_key.json`、
`config/node_key.json`、`config/config.toml`、`config/app.toml`、`data/priv_validator_state.json`。
鍵内容はcheckpointに入れない。監査はreadonlyであり、DB全量検証を代替しない。

継続チェーンの起動はこのfresh launcherの対象外で、互換性と二重署名防止を個別審査する。
更新済みソースを旧DBへ無断適用しない。Validatorの同期と同一高さのhash、署名状態の
単調増加を確認してからHermesを起動する。期限切れ/凍結IBC clientを自動再作成しない。

## 検証範囲

公開用テストは合成fixtureとmock SSHを使う。新VPSでの実consensusやHermes実配送、
Rust製Hermesの脆弱性検査、旧データのupgrade互換性を実証したという意味ではない。
既存lint指摘は脆弱性対応と別に残る。全CIが常にgreenとは主張しない。
