# Crossref 本番デプロイメント

このディレクトリは、ローカル Docker 実験を越えて `crossrefd` を運用するための本番向け雛形である。

## 対象範囲

- 明示的な chain ID を持つ複数 validator 構成
- 永続化された node home と secret file
- remote host inventory
- Prometheus scrape 設定
- validator / full-node データの backup / restore script
- key rotation、route controller、upgrade に関する運用メモ

ここに含まれるファイルは雛形である。公開インフラで利用する前に、address、peer list、secret、port を必ず確認する必要がある。

## ファイル

- `inventory.example.json`: remote host と chain-id の inventory である
- `docker-compose.validator.yml`: validator / full-node service の雛形である
- `prometheus.yml`: CometBFT、app、relayer controller の scrape 設定である
- `backup-crossrefd.sh`: persistent home を tar/zstd で保存する script である
- `restore-crossrefd.sh`: restore 用の保護付き helper である

## Chain ID 方針

環境ごとに安定した chain ID を使う。

- 使い捨て開発 network は `crossref-dev-N`
- rehearsal network は `crossref-staging-N`
- production domain は `crossref-main-N`

production chain ID を reset network に再利用してはならない。

## Secrets

本番では以下を repository 外で管理する。

- validator mnemonic
- priv-validator key
- node key
- relayer mnemonic
- domain hysteresis signing key
- threshold signing participant key

secret manager または read-only mount により注入する。production mnemonic を shell history に残してはならない。

## Monitoring

以下を export / alert 対象にする。

- CometBFT block height と peer count
- validator missed blocks と jail 状態
- route ごとの IBC packet backlog
- relayer-controller の disabled worker count
- route rebalance event
- checkpoint proof failure と accountability event

## Backup

upgrade、key rotation、chain-id migration の前には validator home を backup する。helper script は基礎として利用し、staging で restore を検証する必要がある。
