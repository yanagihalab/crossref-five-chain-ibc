package types

import (
	"crypto/ed25519"
	"encoding/binary"

	errorsmod "cosmossdk.io/errors"
)

var hysteresisThresholdMagic = []byte("crxmsig1")

// HysteresisSignBytes returns the paper-strict canonical bytes signed by a
// domain CCN. It commits to H(S_n-1) separately from checkpoint_hash and to the
// current H(S_n), which keeps the hysteresis signature chain independent from
// the store key/hash used for IBC membership proofs.
func HysteresisSignBytes(checkpoint Checkpoint) []byte {
	stateHash := checkpoint.StateHash
	if len(stateHash) == 0 {
		stateHash = ComputeCheckpointStateHash(checkpoint)
	}

	out := make([]byte, 0, 128+len(checkpoint.DomainId)+len(checkpoint.PreviousStateHash)+len(stateHash))
	out = append(out, []byte("crossref-hysteresis-v1")...)
	out = append(out, uint64ToBigEndian(uint64(len(checkpoint.DomainId)))...)
	out = append(out, []byte(checkpoint.DomainId)...)
	out = append(out, uint64ToBigEndian(checkpoint.Height)...)
	out = append(out, uint64ToBigEndian(checkpoint.KeyEpoch)...)
	out = append(out, uint64ToBigEndian(uint64(len(checkpoint.PreviousStateHash)))...)
	out = append(out, checkpoint.PreviousStateHash...)
	out = append(out, uint64ToBigEndian(uint64(len(stateHash)))...)
	out = append(out, stateHash...)
	return out
}

// VerifyHysteresisSignature validates a checkpoint signature when the domain
// has registered an Ed25519 hysteresis public key. Domains without a registered
// key remain accepted for local experiments and migration compatibility.
func VerifyHysteresisSignature(domain DomainInfo, checkpoint Checkpoint) error {
	if len(domain.HysteresisPublicKey) == 0 {
		return nil
	}
	if domain.KeyEpoch != 0 && checkpoint.KeyEpoch != domain.KeyEpoch {
		return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "domain=%s checkpoint_key_epoch=%d domain_key_epoch=%d", checkpoint.DomainId, checkpoint.KeyEpoch, domain.KeyEpoch)
	}
	if len(checkpoint.HysteresisSignature) == 0 {
		return errorsmod.Wrapf(ErrHysteresisSignatureRequired, "domain=%s height=%d", checkpoint.DomainId, checkpoint.Height)
	}
	if isThresholdHysteresisKey(domain.HysteresisPublicKey) {
		return verifyThresholdHysteresisSignature(domain, checkpoint)
	}
	if len(domain.HysteresisPublicKey) != ed25519.PublicKeySize {
		return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "domain=%s invalid public key length=%d", domain.DomainId, len(domain.HysteresisPublicKey))
	}
	if !ed25519.Verify(ed25519.PublicKey(domain.HysteresisPublicKey), HysteresisSignBytes(checkpoint), checkpoint.HysteresisSignature) {
		return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "domain=%s height=%d", checkpoint.DomainId, checkpoint.Height)
	}
	return nil
}

// EncodeThresholdHysteresisPublicKey encodes an ordered Ed25519 validator key
// set with a threshold. It keeps the existing DomainInfo field wire-compatible
// while allowing experiments with multi-signature checkpoint authorization.
func EncodeThresholdHysteresisPublicKey(threshold uint8, publicKeys ...ed25519.PublicKey) []byte {
	out := make([]byte, 0, len(hysteresisThresholdMagic)+2+len(publicKeys)*ed25519.PublicKeySize)
	out = append(out, hysteresisThresholdMagic...)
	out = append(out, threshold, uint8(len(publicKeys)))
	for _, key := range publicKeys {
		out = append(out, key...)
	}
	return out
}

// EncodeThresholdHysteresisSignature encodes indexed Ed25519 signatures for a
// threshold public key set. Each signature entry is (key_index, signature).
func EncodeThresholdHysteresisSignature(indexedSignatures map[uint8][]byte) []byte {
	out := make([]byte, 0, len(hysteresisThresholdMagic)+2+len(indexedSignatures)*(1+ed25519.SignatureSize))
	out = append(out, hysteresisThresholdMagic...)
	out = append(out, uint8(len(indexedSignatures)))
	for i := uint8(0); i < 255; i++ {
		sig, ok := indexedSignatures[i]
		if !ok {
			continue
		}
		out = append(out, i)
		out = append(out, sig...)
	}
	return out
}

func isThresholdHysteresisKey(publicKey []byte) bool {
	return len(publicKey) >= len(hysteresisThresholdMagic)+2 &&
		string(publicKey[:len(hysteresisThresholdMagic)]) == string(hysteresisThresholdMagic)
}

func verifyThresholdHysteresisSignature(domain DomainInfo, checkpoint Checkpoint) error {
	keyBz := domain.HysteresisPublicKey
	if !isThresholdHysteresisKey(keyBz) {
		return errorsmod.Wrap(ErrHysteresisSignatureInvalid, "invalid threshold key encoding")
	}
	header := len(hysteresisThresholdMagic)
	threshold := int(keyBz[header])
	keyCount := int(keyBz[header+1])
	keysStart := header + 2
	if threshold <= 0 || keyCount <= 0 || threshold > keyCount {
		return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "domain=%s invalid threshold=%d key_count=%d", domain.DomainId, threshold, keyCount)
	}
	if len(keyBz) != keysStart+keyCount*ed25519.PublicKeySize {
		return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "domain=%s invalid threshold key length=%d", domain.DomainId, len(keyBz))
	}

	sigBz := checkpoint.HysteresisSignature
	if len(sigBz) < len(hysteresisThresholdMagic)+1 ||
		string(sigBz[:len(hysteresisThresholdMagic)]) != string(hysteresisThresholdMagic) {
		return errorsmod.Wrap(ErrHysteresisSignatureInvalid, "invalid threshold signature encoding")
	}
	sigCount := int(sigBz[len(hysteresisThresholdMagic)])
	offset := len(hysteresisThresholdMagic) + 1
	if len(sigBz) != offset+sigCount*(1+ed25519.SignatureSize) {
		return errorsmod.Wrap(ErrHysteresisSignatureInvalid, "invalid threshold signature length")
	}

	seen := make(map[uint8]struct{}, sigCount)
	valid := 0
	signBytes := HysteresisSignBytes(checkpoint)
	for i := 0; i < sigCount; i++ {
		index := sigBz[offset]
		offset++
		signature := sigBz[offset : offset+ed25519.SignatureSize]
		offset += ed25519.SignatureSize
		if int(index) >= keyCount {
			return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "signature index out of range=%d", index)
		}
		if _, duplicate := seen[index]; duplicate {
			return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "duplicate signature index=%d", index)
		}
		seen[index] = struct{}{}
		keyStart := keysStart + int(index)*ed25519.PublicKeySize
		if ed25519.Verify(ed25519.PublicKey(keyBz[keyStart:keyStart+ed25519.PublicKeySize]), signBytes, signature) {
			valid++
		}
	}
	if valid < threshold {
		return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "domain=%s valid_signatures=%d threshold=%d", domain.DomainId, valid, threshold)
	}
	return nil
}

func uint64ToBigEndian(v uint64) []byte {
	var out [8]byte
	binary.BigEndian.PutUint64(out[:], v)
	return out[:]
}
