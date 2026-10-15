#!/bin/sh
set -eu
HOME_DIR=/var/crossref
for file in config/genesis.json config/priv_validator_key.json config/node_key.json data/priv_validator_state.json; do
  test -s "$HOME_DIR/$file" || { echo "Missing prepared home: $file; refusing automatic initialization" >&2; exit 1; }
done
jq -e --arg chain "$CHAIN_ID" '.chain_id == $chain' "$HOME_DIR/config/genesis.json" >/dev/null
exec crossrefd start --home "$HOME_DIR" --minimum-gas-prices "0${DENOM}" \
  --rpc.laddr tcp://0.0.0.0:26657 --p2p.laddr tcp://0.0.0.0:26656 \
  --grpc.address 0.0.0.0:9090 --api.enable --api.address tcp://0.0.0.0:1317
