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
