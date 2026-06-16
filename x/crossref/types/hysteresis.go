package types

import (
	"crypto/ed25519"

	errorsmod "cosmossdk.io/errors"
)

// HysteresisSignBytes returns the canonical bytes signed by a domain CCN for a
// checkpoint. The checkpoint hash already commits to the previous checkpoint
// hash and current block state while intentionally excluding the signature.
func HysteresisSignBytes(checkpoint Checkpoint) []byte {
	if len(checkpoint.CheckpointHash) > 0 {
		return checkpoint.CheckpointHash
	}
	return ComputeCheckpointHash(checkpoint)
}

// VerifyHysteresisSignature validates a checkpoint signature when the domain
// has registered an Ed25519 hysteresis public key. Domains without a registered
// key remain accepted for local experiments and migration compatibility.
func VerifyHysteresisSignature(domain DomainInfo, checkpoint Checkpoint) error {
	if len(domain.HysteresisPublicKey) == 0 {
		return nil
	}
	if len(checkpoint.HysteresisSignature) == 0 {
		return errorsmod.Wrapf(ErrHysteresisSignatureRequired, "domain=%s height=%d", checkpoint.DomainId, checkpoint.Height)
	}
	if len(domain.HysteresisPublicKey) != ed25519.PublicKeySize {
		return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "domain=%s invalid public key length=%d", domain.DomainId, len(domain.HysteresisPublicKey))
	}
	if !ed25519.Verify(ed25519.PublicKey(domain.HysteresisPublicKey), HysteresisSignBytes(checkpoint), checkpoint.HysteresisSignature) {
		return errorsmod.Wrapf(ErrHysteresisSignatureInvalid, "domain=%s height=%d", checkpoint.DomainId, checkpoint.Height)
	}
	return nil
}
