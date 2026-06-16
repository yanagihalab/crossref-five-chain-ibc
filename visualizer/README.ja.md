# Crossref Test Visualizer

このディレクトリには、crossref module と 5 チェーン IBC 実験を確認するための static HTML visualizer と test data を置いている。

## ファイル

- `crossref-test-visualizer.html`: route と packet flow を animation で確認する browser UI。
- `test-results.json`: `./x/crossref/...` の `go test -json` 出力。
- `verify-visualizer.mjs`: visualizer logic を確認する Node smoke test。

## 開く

repository root から実行する。

```bash
open visualizer/crossref-test-visualizer.html
```

## 検証

repository root から実行する。

```bash
node visualizer/verify-visualizer.mjs
```

## Test Data 更新

repository root から実行する。

```bash
GOCACHE="$(pwd)/.gocache" go test -json -count=1 ./x/crossref/... > visualizer/test-results.json
```

