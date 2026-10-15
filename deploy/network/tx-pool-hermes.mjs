import assert from "node:assert/strict";
import { normalizePoolWork } from "./tx-pool.mjs";

export const TX_POOL_HERMES_AUDIT = {
  version: "1.10.5", commit: "be0be09d930f337de1f277e6eec65826a3a5ea2a",
  source: "https://github.com/informalsystems/hermes/blob/be0be09d930f337de1f277e6eec65826a3a5ea2a/crates/relayer-cli/src/commands/tx/packet.rs",
  deploymentReady: false,
  reason: "Stock Hermes start and packet CLI do not persist the full signed transaction hash set in the pool before sending",
};

export const TX_POOL_RPC_AUDIT = {
  version: TX_POOL_HERMES_AUDIT.version, commit: TX_POOL_HERMES_AUDIT.commit,
  senderSource: "https://github.com/informalsystems/hermes/blob/be0be09d930f337de1f277e6eec65826a3a5ea2a/crates/relayer/src/chain/cosmos/tx.rs",
  adapter: "tx-pool-rpc.mjs", kind: "experimental-prebroadcast-http-gate",
  deploymentStrategy: "tx-pool", packetOutcomes: "independent exact hash and packet event queries",
  trustBoundary: "Cooperative non-Byzantine Hermes processes; upstream WebSocket event subscriptions are not a hostile-process security boundary",
};

export function plannedHermesPacketCommand(rawWork) {
  const work = normalizePoolWork(rawWork);
  const ack = work.action === "ack";
  return ["tx", ack ? "packet-ack" : "packet-recv",
    "--src-chain", ack ? work.destinationChain : work.sourceChain,
    "--dst-chain", ack ? work.sourceChain : work.destinationChain,
    "--src-port", ack ? work.destinationPort : work.sourcePort,
    "--src-channel", ack ? work.destinationChannel : work.sourceChannel,
    "--packet-sequences", work.sequence];
}

export function assertTxPoolVpsReady() {
  assert.equal(TX_POOL_HERMES_AUDIT.deploymentReady, true,
    "Tx pool VPS deployment is gated: a pre-broadcast Hermes sender hook and chain evidence adapter must be verified first; stock Hermes must not bypass the pool");
}
