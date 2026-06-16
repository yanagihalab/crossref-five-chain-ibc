# Crossref 5チェーン Docker 実験

このディレクトリでは、5本の独立した `crossrefd` チェーンと、1つの Hermes relayer を起動します。
Hermes は次の組み合わせで `crossref` IBC channel の full mesh を開きます。

- `chain-a <-> chain-b`
- `chain-a <-> chain-c`
- `chain-a <-> chain-d`
- `chain-a <-> chain-e`
- `chain-b <-> chain-c`
- `chain-b <-> chain-d`
- `chain-b <-> chain-e`
- `chain-c <-> chain-d`
- `chain-c <-> chain-e`
- `chain-d <-> chain-e`

結果として、10本の双方向 IBC channel ペアと、20方向の cross-reference 経路ができます。

## 前提条件

- コマンドは `docker/` の中ではなく、リポジトリ root から実行します。
- Docker 起動前に Linux ARM64 版 binary を build します。
- Docker Desktop または Docker Engine を起動しておきます。
- macOS で Docker が project directory を読めない場合は、Docker file sharing で許可された場所に repository を置くか、親 folder への access を許可してください。

## 実験の流れ

この実験は大まかに次を行います。

1. 5本の独立した single-validator chain を起動する。
2. 1つの Hermes relayer container を起動する。
3. `crossref/crossref` IBC channel の full mesh を作成する。
4. すべての chain に全 domain を登録する。
5. 各 directed domain pair を local IBC channel に bind する。
6. 各 source chain で checkpoint を1つ登録する。
7. source-chain checkpoint proof を ICS23 proof として export する。
8. destination light client を proof height まで更新する。
9. 各 checkpoint を他の4チェーンへ broadcast する。
10. destination chain を query し、受信済み cross-reference を確認する。

## 経路の通し番号

IBC の channel ID はチェーンごとのローカルIDです。そのため、複数のチェーンがそれぞれ `channel-0` を持ちます。
ログや visualizer で追いやすくするため、この実験では20方向すべての経路に `route-00` から `route-19` までの通し番号を割り当てています。

| Route | 方向 | 実ローカル endpoint | 実 counterparty endpoint |
| --- | --- | --- | --- |
| `route-00` | `chain-a -> chain-b` | `chain-a/channel-0` | `chain-b/channel-0` |
| `route-01` | `chain-b -> chain-a` | `chain-b/channel-0` | `chain-a/channel-0` |
| `route-02` | `chain-a -> chain-c` | `chain-a/channel-1` | `chain-c/channel-0` |
| `route-03` | `chain-c -> chain-a` | `chain-c/channel-0` | `chain-a/channel-1` |
| `route-04` | `chain-a -> chain-d` | `chain-a/channel-2` | `chain-d/channel-0` |
| `route-05` | `chain-d -> chain-a` | `chain-d/channel-0` | `chain-a/channel-2` |
| `route-06` | `chain-a -> chain-e` | `chain-a/channel-3` | `chain-e/channel-0` |
| `route-07` | `chain-e -> chain-a` | `chain-e/channel-0` | `chain-a/channel-3` |
| `route-08` | `chain-b -> chain-c` | `chain-b/channel-1` | `chain-c/channel-1` |
| `route-09` | `chain-c -> chain-b` | `chain-c/channel-1` | `chain-b/channel-1` |
| `route-10` | `chain-b -> chain-d` | `chain-b/channel-2` | `chain-d/channel-1` |
| `route-11` | `chain-d -> chain-b` | `chain-d/channel-1` | `chain-b/channel-2` |
| `route-12` | `chain-b -> chain-e` | `chain-b/channel-3` | `chain-e/channel-1` |
| `route-13` | `chain-e -> chain-b` | `chain-e/channel-1` | `chain-b/channel-3` |
| `route-14` | `chain-c -> chain-d` | `chain-c/channel-2` | `chain-d/channel-2` |
| `route-15` | `chain-d -> chain-c` | `chain-d/channel-2` | `chain-c/channel-2` |
| `route-16` | `chain-c -> chain-e` | `chain-c/channel-3` | `chain-e/channel-2` |
| `route-17` | `chain-e -> chain-c` | `chain-e/channel-2` | `chain-c/channel-3` |
| `route-18` | `chain-d -> chain-e` | `chain-d/channel-3` | `chain-e/channel-3` |
| `route-19` | `chain-e -> chain-d` | `chain-e/channel-3` | `chain-d/channel-3` |

## ビルド

Docker image 内で使う Linux ARM64 版の daemon をビルドします。

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ./build/crossrefd-linux-arm64 ./cmd/crossrefdd
```

Intel/AMD Linux Docker host で動かす場合は `linux/amd64` で build し、Dockerfile または出力 path を合わせて変更してください。

## 起動

5本のチェーンと relayer を起動します。

```bash
docker compose -f docker/docker-compose.yml up -d --build
```

relayer コンテナは `setup-ibc.sh` を実行します。
このスクリプトは全チェーンの起動を待ち、Hermes key を読み込み、`crossref` channel の full mesh を作成します。

container の状態を確認します。

```bash
docker compose -f docker/docker-compose.yml ps
```

relayer log を確認します。

```bash
docker compose -f docker/docker-compose.yml logs -f relayer
```

## 実験の実行

5チェーンの end-to-end 実験を実行します。

```bash
docker/scripts/run-crossref-experiment.sh
```

このスクリプトは次を確認します。

1. `route-00` から `route-19` までの通し番号表を表示する。
2. 20方向すべての `crossref` channel endpoint が存在するまで待つ。
3. 5つの domain を全5チェーンに登録する。
4. すべての directed domain/channel route を bind する。
5. 各 source chain に checkpoint を1つ登録する。
6. `query crossref checkpoint-proof` で各 source checkpoint の ICS23 store proof を取得する。
7. destination 側 light client を source proof height まで更新する。
8. 各 source checkpoint packet を他の4チェーンへ broadcast する。
9. 各 destination chain が、他の4つの source chain の cross-reference を保存していることを確認する。

成功時は最後に次のメッセージが表示されます。

```text
Five-chain cross-reference experiment passed.
```

## Hysteresis Signature 検証

domain は Ed25519 の `hysteresis_public_key` を登録できます。
この公開鍵が設定されているdomainでは、`SubmitCheckpoint` と IBC packet 受信の両方で `hysteresis_signature` が必須になり、checkpoint hash に対する署名として検証されます。
公開鍵が未登録のdomainは、ローカル実験や移行互換性のため従来通り受け入れます。

## Query 例

chain A の checkpoint を確認します。

```bash
docker compose -f docker/docker-compose.yml exec -T chain-a \
  crossrefd --home /var/crossref query crossref checkpoint chain-a 1 --output json
```

chain B に保存された chain A の cross-reference を確認します。

```bash
docker compose -f docker/docker-compose.yml exec -T chain-b \
  crossrefd --home /var/crossref query crossref cross-reference chain-b chain-a 1 --output json
```

chain A から ICS23 checkpoint proof を export します。

```bash
docker compose -f docker/docker-compose.yml exec -T chain-a \
  crossrefd --home /var/crossref query crossref checkpoint-proof chain-a 1 --output json
```

## エンドポイント

| Chain | RPC | gRPC | REST |
| --- | --- | --- | --- |
| A | `http://localhost:26657` | `localhost:9090` | `http://localhost:1317` |
| B | `http://localhost:26667` | `localhost:9091` | `http://localhost:1318` |
| C | `http://localhost:26677` | `localhost:9092` | `http://localhost:1319` |
| D | `http://localhost:26687` | `localhost:9093` | `http://localhost:1320` |
| E | `http://localhost:26697` | `localhost:9094` | `http://localhost:1321` |

## リセット

コンテナを停止し、全チェーンの状態を削除します。

```bash
docker compose -f docker/docker-compose.yml down -v
```

## 注意点

`run-crossref-experiment.sh` は、`setup-ibc.sh` の channel 作成順序によって生成される channel ID と client ID を前提にしています。
full mesh の channel 作成順序を変更した場合は、実験スクリプト内の `channel_id()` と `client_for_source()` も更新してください。

実験スクリプトは、成功後も Docker network を起動したままにします。
これにより、追加の query 実行や visualizer による確認を続けられます。

## トラブルシュート

- `operation not permitted`: macOS の file permission または Docker file sharing が access を妨げている可能性があります。repository を Docker が共有できる folder に置き、現在の user が読める状態にしてください。
- `docker: unknown command: docker compose`: Docker Compose v2 を install するか、Compose v2 同梱の Docker Desktop を使ってください。
- channel が作成されない: `docker compose -f docker/docker-compose.yml logs -f relayer` で relayer log を確認してください。
- channel 作成順序を変えた後に experiment が失敗する: `docker/scripts/run-crossref-experiment.sh` の `channel_id()` と `client_for_source()` を更新してください。
