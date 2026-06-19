package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

var thresholdMagic = []byte("crxmsig1")

type thresholdResult struct {
	PublicKey         string `json:"public_key"`
	StateHash         string `json:"state_hash"`
	PreviousStateHash string `json:"previous_state_hash,omitempty"`
	CheckpointHash    string `json:"checkpoint_hash"`
	ConsensusProof    string `json:"consensus_proof,omitempty"`
	Signature         string `json:"signature"`
}

func main() {
	if len(os.Args) < 10 || len(os.Args) > 13 {
		fatalf("usage: go run docker/scripts/hysteresis-threshold-sign <domain-id> <height> <block-hash-b64> <app-hash-b64> <block-time-unix> <threshold> <key-count> <signer-count> <seed-prefix> [previous-checkpoint-hash-b64] [previous-state-hash-b64] [key-epoch]")
	}

	domainID := os.Args[1]
	height, err := strconv.ParseUint(os.Args[2], 10, 64)
	if err != nil {
		fatalf("invalid height: %v", err)
	}
	blockHash, err := base64.StdEncoding.DecodeString(os.Args[3])
	if err != nil {
		fatalf("invalid block hash base64: %v", err)
	}
	appHash, err := base64.StdEncoding.DecodeString(os.Args[4])
	if err != nil {
		fatalf("invalid app hash base64: %v", err)
	}
	blockTimeUnix, err := strconv.ParseInt(os.Args[5], 10, 64)
	if err != nil {
		fatalf("invalid block time: %v", err)
	}
	threshold, err := strconv.Atoi(os.Args[6])
	if err != nil {
		fatalf("invalid threshold: %v", err)
	}
	keyCount, err := strconv.Atoi(os.Args[7])
	if err != nil {
		fatalf("invalid key count: %v", err)
	}
	signerCount, err := strconv.Atoi(os.Args[8])
	if err != nil {
		fatalf("invalid signer count: %v", err)
	}
	if threshold <= 0 || keyCount <= 0 || threshold > keyCount || signerCount < 0 || signerCount > keyCount {
		fatalf("invalid threshold parameters: threshold=%d key_count=%d signer_count=%d", threshold, keyCount, signerCount)
	}
	seedPrefix := os.Args[9]

	var previousCheckpointHash []byte
	if len(os.Args) >= 11 && os.Args[10] != "" {
		previousCheckpointHash, err = base64.StdEncoding.DecodeString(os.Args[10])
		if err != nil {
			fatalf("invalid previous checkpoint hash base64: %v", err)
		}
	}
	var previousStateHash []byte
	if len(os.Args) >= 12 && os.Args[11] != "" {
		previousStateHash, err = base64.StdEncoding.DecodeString(os.Args[11])
		if err != nil {
			fatalf("invalid previous state hash base64: %v", err)
		}
	}
	var keyEpoch uint64 = 1
	if len(os.Args) == 13 && os.Args[12] != "" {
		keyEpoch, err = strconv.ParseUint(os.Args[12], 10, 64)
		if err != nil {
			fatalf("invalid key epoch: %v", err)
		}
	}

	privateKeys := make([]ed25519.PrivateKey, keyCount)
	publicKeys := make([]ed25519.PublicKey, keyCount)
	for i := range keyCount {
		seed := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", seedPrefix, i)))
		privateKeys[i] = ed25519.NewKeyFromSeed(seed[:])
		publicKeys[i] = privateKeys[i].Public().(ed25519.PublicKey)
	}

	stateHash := computeStateHash(domainID, height, blockHash, appHash, nil, blockTimeUnix)
	checkpointHash := computeCheckpointHash(domainID, height, blockHash, appHash, nil, previousCheckpointHash, blockTimeUnix)
	consensusProof := computeConsensusProof(domainID, height, blockHash, appHash, nil, stateHash, 1, height)
	signature := encodeThresholdSignature(privateKeys, signerCount, hysteresisSignBytes(domainID, height, keyEpoch, previousStateHash, stateHash))

	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(thresholdResult{
		PublicKey:         base64.StdEncoding.EncodeToString(encodeThresholdPublicKey(threshold, publicKeys)),
		StateHash:         base64.StdEncoding.EncodeToString(stateHash),
		PreviousStateHash: base64.StdEncoding.EncodeToString(previousStateHash),
		CheckpointHash:    base64.StdEncoding.EncodeToString(checkpointHash),
		ConsensusProof:    base64.StdEncoding.EncodeToString(consensusProof),
		Signature:         base64.StdEncoding.EncodeToString(signature),
	}); err != nil {
		fatalf("encode result: %v", err)
	}
}

func encodeThresholdPublicKey(threshold int, publicKeys []ed25519.PublicKey) []byte {
	out := make([]byte, 0, len(thresholdMagic)+2+len(publicKeys)*ed25519.PublicKeySize)
	out = append(out, thresholdMagic...)
	out = append(out, byte(threshold), byte(len(publicKeys)))
	for _, publicKey := range publicKeys {
		out = append(out, publicKey...)
	}
	return out
}

func encodeThresholdSignature(privateKeys []ed25519.PrivateKey, signerCount int, signBytes []byte) []byte {
	out := make([]byte, 0, len(thresholdMagic)+1+signerCount*(1+ed25519.SignatureSize))
	out = append(out, thresholdMagic...)
	out = append(out, byte(signerCount))
	for i := range signerCount {
		out = append(out, byte(i))
		out = append(out, ed25519.Sign(privateKeys[i], signBytes)...)
	}
	return out
}

func computeCheckpointHash(domainID string, height uint64, blockHash, appHash, validatorSetHash, previousCheckpointHash []byte, blockTimeUnix int64) []byte {
	h := sha256.New()
	writeBytes(h, []byte(domainID))
	writeUint64(h, height)
	writeBytes(h, blockHash)
	writeBytes(h, appHash)
	writeBytes(h, validatorSetHash)
	writeBytes(h, previousCheckpointHash)
	writeUint64(h, uint64(blockTimeUnix))
	return h.Sum(nil)
}

func computeStateHash(domainID string, height uint64, blockHash, appHash, validatorSetHash []byte, blockTimeUnix int64) []byte {
	h := sha256.New()
	writeBytes(h, []byte("crossref-state-v1"))
	writeBytes(h, []byte(domainID))
	writeUint64(h, height)
	writeBytes(h, blockHash)
	writeBytes(h, appHash)
	writeBytes(h, validatorSetHash)
	writeUint64(h, uint64(blockTimeUnix))
	return h.Sum(nil)
}

func computeConsensusProof(domainID string, height uint64, blockHash, appHash, validatorSetHash, stateHash []byte, revisionNumber, revisionHeight uint64) []byte {
	h := sha256.New()
	writeBytes(h, []byte("crossref-consensus-proof-v1"))
	writeBytes(h, []byte(domainID))
	writeUint64(h, height)
	writeBytes(h, blockHash)
	writeBytes(h, appHash)
	writeBytes(h, validatorSetHash)
	writeBytes(h, stateHash)
	writeUint64(h, revisionNumber)
	writeUint64(h, revisionHeight)
	return h.Sum(nil)
}

func hysteresisSignBytes(domainID string, height, keyEpoch uint64, previousStateHash, stateHash []byte) []byte {
	out := make([]byte, 0, 128+len(domainID)+len(previousStateHash)+len(stateHash))
	out = append(out, []byte("crossref-hysteresis-v1")...)
	out = append(out, uint64Bytes(uint64(len(domainID)))...)
	out = append(out, []byte(domainID)...)
	out = append(out, uint64Bytes(height)...)
	out = append(out, uint64Bytes(keyEpoch)...)
	out = append(out, uint64Bytes(uint64(len(previousStateHash)))...)
	out = append(out, previousStateHash...)
	out = append(out, uint64Bytes(uint64(len(stateHash)))...)
	out = append(out, stateHash...)
	return out
}

func writeBytes(h interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(value)
}

func writeUint64(h interface{ Write([]byte) (int, error) }, value uint64) {
	_, _ = h.Write(uint64Bytes(value))
}

func uint64Bytes(value uint64) []byte {
	var bz [8]byte
	binary.BigEndian.PutUint64(bz[:], value)
	return bz[:]
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
