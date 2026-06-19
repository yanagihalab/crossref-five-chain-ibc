package types

import (
	"crypto/sha256"
	"encoding/binary"
)

func ComputeCheckpointHash(c Checkpoint) []byte {
	h := sha256.New()
	writeString(h, c.DomainId)
	writeUint64(h, c.Height)
	writeBytes(h, c.BlockHash)
	writeBytes(h, c.AppHash)
	writeBytes(h, c.ValidatorSetHash)
	writeBytes(h, c.PreviousCheckpointHash)
	writeInt64(h, c.BlockTimeUnix)
	return h.Sum(nil)
}

// ComputeCheckpointStateHash returns H(S_n), the paper-level state commitment
// for a checkpoint. It deliberately excludes the hysteresis chain fields
// (previous_state_hash, checkpoint_hash, signatures, and proofs) so H(S_n-1)
// can be validated independently from the checkpoint store key/hash.
func ComputeCheckpointStateHash(c Checkpoint) []byte {
	h := sha256.New()
	writeString(h, "crossref-state-v1")
	writeString(h, c.DomainId)
	writeUint64(h, c.Height)
	writeBytes(h, c.BlockHash)
	writeBytes(h, c.AppHash)
	writeBytes(h, c.ValidatorSetHash)
	writeInt64(h, c.BlockTimeUnix)
	return h.Sum(nil)
}

// NormalizeCheckpointHashes fills derived hashes without weakening caller
// supplied commitments. It returns an error when an explicit state hash or
// checkpoint hash does not match the canonical encoding.
func NormalizeCheckpointHashes(c *Checkpoint) error {
	expectedStateHash := ComputeCheckpointStateHash(*c)
	if len(c.StateHash) == 0 {
		c.StateHash = expectedStateHash
	} else if !equalBytes(c.StateHash, expectedStateHash) {
		return ErrStateHashMismatch
	}

	expectedCheckpointHash := ComputeCheckpointHash(*c)
	if len(c.CheckpointHash) == 0 {
		c.CheckpointHash = expectedCheckpointHash
	} else if !equalBytes(c.CheckpointHash, expectedCheckpointHash) {
		return ErrCheckpointHashMismatch
	}
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func writeString(h interface{ Write([]byte) (int, error) }, value string) {
	writeBytes(h, []byte(value))
}

func writeBytes(h interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(value)
}

func writeUint64(h interface{ Write([]byte) (int, error) }, value uint64) {
	var bz [8]byte
	binary.BigEndian.PutUint64(bz[:], value)
	_, _ = h.Write(bz[:])
}

func writeInt64(h interface{ Write([]byte) (int, error) }, value int64) {
	writeUint64(h, uint64(value))
}
