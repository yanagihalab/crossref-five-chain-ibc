package types

import "crypto/sha256"

// ComputeConsensusProofDigest is the deterministic evidence envelope currently
// accepted by the module when require_consensus_proof is enabled. It is not a
// replacement for an IBC light client; it is a consensus-level commitment hook
// that binds block/app/validator-set/state hashes and proof height so production
// deployments can require and later upgrade this field without another packet
// format change.
func ComputeConsensusProofDigest(c Checkpoint) []byte {
	h := sha256.New()
	writeString(h, "crossref-consensus-proof-v1")
	writeString(h, c.DomainId)
	writeUint64(h, c.Height)
	writeBytes(h, c.BlockHash)
	writeBytes(h, c.AppHash)
	writeBytes(h, c.ValidatorSetHash)
	writeBytes(h, c.StateHash)
	writeUint64(h, c.ConsensusProofRevisionNumber)
	writeUint64(h, c.ConsensusProofRevisionHeight)
	return h.Sum(nil)
}
