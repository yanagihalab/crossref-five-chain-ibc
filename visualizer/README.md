# Crossref Test Visualizer

This directory contains a static HTML visualizer and test data for the crossref
module and five-chain IBC experiment.

## Files

- `crossref-test-visualizer.html`: browser UI with animated route and packet flow.
- `test-results.json`: captured `go test -json` output for `./x/crossref/...`.
- `verify-visualizer.mjs`: Node-based smoke test for the visualizer logic.

## Open

From the repository root:

```bash
open visualizer/crossref-test-visualizer.html
```

## Verify

From the repository root:

```bash
node visualizer/verify-visualizer.mjs
```

## Refresh Test Data

From the repository root:

```bash
GOCACHE="$(pwd)/.gocache" go test -json -count=1 ./x/crossref/... > visualizer/test-results.json
```

