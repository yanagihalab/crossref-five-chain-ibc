# 2026-10-14 Validator/Relayer補助ソフトの32件対応

対象は2026-10-14の対応前ソースに対する
govulncheck v1.9.0 / Go 1.26.4の32件のsymbol到達可能性報告。
静的到達は悪用成功や侵害の確認ではない。保存・復号・DB整合性検査とは別である。
この変更は次の新規ネットワーク用。研究の凍結ソース、5保存済みバイナリ、旧DB・鍵・署名状態は保持する。

## 対応一覧

バージョンは今回選択した系列での対応。各IDの最終検査結果は
`deploy/recovery/security-remediation-20261014.json` へ記録する。
修正版一覧だけで合格にせず、ソースと実Linux配置用バイナリを再検査する。

| GO-ID | 対象 | 対応 |
|---|---|---|
| GO-2026-6617 | HTTP/2 HPACK競合 | Go 1.26.9、x/net 0.60.0 |
| GO-2026-6615 | ログBatchProcessor CPU消費 | OpenTelemetry sdk/log 0.22.0 |
| GO-2026-6613 | CONNECT成功後の解釈ずれ | Go 1.26.9 |
| GO-2026-6612 | HTTP/2受信枠二重返却 | Go 1.26.9、x/net 0.60.0 |
| GO-2026-6611 | HTTP/2 window変更CPU消費 | Go 1.26.9、x/net 0.60.0 |
| GO-2026-6610 | HTTP/2不正ヘッダー応答密輸 | Go 1.26.9、x/net 0.60.0 |
| GO-2026-6609 | 大量Range解析 | Go 1.26.9 |
| GO-2026-6608 | multipart/MIMEメモリ上限回避 | Go 1.26.9 |
| GO-2026-6607 | ECH不正参照メモリ消費 | Go 1.26.9 |
| GO-2026-6605 | CONNECT拒否後の接続汚染 | Go 1.26.9 |
| GO-2026-6603 | HTTP/2 Trailerメモリ消費 | Go 1.26.9、x/net 0.60.0 |
| GO-2026-6600 | template yield文脈 | Go 1.26.9 |
| GO-2026-6599 | template連続式文脈 | Go 1.26.9 |
| GO-2026-6508 | ログgRPCの環境変数TLS設定無視 | OTLPログgRPC 0.22.0 |
| GO-2026-6505 | exporter設定のログ露出 | OTLP trace各1.46.0、sdk 1.46.0 |
| GO-2026-6443 | xDS Host欠落panic | gRPC 1.83.2。1.83.1への更新では未解消だったことも保持 |
| GO-2026-6348 | gRPC DATA細分化OOM | gRPC 1.83.2 |
| GO-2026-6218 | 相対URL二次時間 | Go 1.26.9 |
| GO-2026-6165 | DTLS ECDHE_PSK解析panic | dtls/v3 3.1.5 |
| GO-2026-6099 | WebTransport未知capsule | webtransport-go 0.13.0 |
| GO-2026-6091 | template正規表現文脈 | Go 1.26.9 |
| GO-2026-6090 | TLS KeyUpdate CPU消費 | Go 1.26.9 |
| GO-2026-6089 | 平文HTTP/2 timeout | Go 1.26.9 |
| GO-2026-6061 | xDS認可/Rapid Reset | gRPC 1.83.2 |
| GO-2026-5972 | ASN.1再帰 | Go 1.26.9 |
| GO-2026-5970 | 不正UTF-8 norm無限ループ | x/text 0.42.0 |
| GO-2026-5932 | 保守終了OpenPGP | SDK armor importをProtonMail/go-crypto 1.5.2へ移行。旧packageをruntime graphから除去 |
| GO-2026-5856 | ECH PSK識別子露出 | Go 1.26.9 |
| GO-2026-5158 | baggage上限欠落 | OpenTelemetry 1.46.0 |
| GO-2026-5026 | Punycodeの認可差 | Go 1.26.9 |
| GO-2026-4508 | Ethereum P2PメモリDoS | go-ethereum 1.17.0 |
| GO-2026-4479 | DTLS v2 GCM nonce | libp2p 0.50.0/STUN v3 3.1.7へ移行し、dtls/v2をruntime graphから除去 |

公式情報は `https://pkg.go.dev/vuln/<GO-ID>` にある。
gRPC等の修正範囲は系列により非単調であるため、単に最初の修正版以上であることだけでは判断しない。

## SDKの限定変更と検査

上流Cosmos SDK 0.54.3のmodule zip checksum、go.mod checksum、全2439ファイルのSHAを検証する。
変更は `crypto/armor.go` のimport 1か所だけ。暗号化、KDF、consensus、codec等の処理は変更しない。
生成物は `.security-deps/cosmos-sdk` に置き、元module cacheを編集しない。
毎回全ファイルを比較し、認めたimport変更以外の差異、symlink/hardlink、未知の上流SHAは拒否する。
Goのlocal replaceのため、未準備のcloneで直接buildすると失敗する。最初に `make security-deps` を実行する。

local replaceでは上流version metadataによる脆弱性検索が欠けるため、
`check-sdk-advisories.mjs` は上流0.54.3の公式DB照会を独立に実施する。
既知のGO-2023-1821/1881はx/crisisへのmodule報告で、本repoがそのpackageをimportしないことを確認する。
新しいSDK報告、対象packageのimport、旧OpenPGP/DTLS v2の再導入は合格にしない。
SDKの既知問題をversion消失により隠して「ゼロ」と扱わない。

互換性テストは新しく生成した合成鍵のexport/import、誤password拒否、破損armor拒否を検証する。
既存のValidator/Relayer鍵を読んで再生成・再署名しない。

## 検証と限界

```bash
make security-deps
go vet -mod=readonly -p 2 ./...
go test -mod=readonly -p 2 ./...
node --test --test-concurrency=2 deploy/network/test-*.mjs deploy/recovery/test-*.mjs docker/scripts/test-multi-relayer-adaptation.mjs
make govulncheck
mkdir -p build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -p 2 -o build/crossrefd-linux-amd64 ./cmd/crossrefdd
make govulncheck-binary BINARY=build/crossrefd-linux-amd64
```

JSONモードのexit0は脆弱性合格ではない。symbol findingを実際に数え、text-modeのexitも確認する。
実Linuxバイナリの検査は行うが、Rust製Hermes本体、停止済みVPSの旧OS全体、
無断で旧チェーンをupgradeした場合の安全性を保証するものではない。
module-onlyの残件は別記し、脆弱packageの再導入を拒否する。
既存lintの未解消指摘は32件とは別であり、CI全greenの断定はしない。

公開物はソース・合成fixture・汎用手順だけであり、実運用のinventory、DB、鍵、
パスワード、NAS共有先、providerアカウント、運用receiptは含めない。
非公開の研究履歴を公開Git履歴へmergeしない。
この公開版の起動手順は [Validator・Relayer起動](NODE-STARTUP.ja.md) を参照する。
