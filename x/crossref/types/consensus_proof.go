package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	tmtypes "github.com/cometbft/cometbft/types"
)

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

type CometBFTConsensusProofEnvelope struct {
	Version      string                `json:"version"`
	ChainID      string                `json:"chain_id"`
	SignedHeader *tmtypes.SignedHeader `json:"signed_header"`
	ValidatorSet *tmtypes.ValidatorSet `json:"validator_set"`
}

type VerifiedCometBFTConsensusProof struct {
	ChainID              string
	Height               uint64
	BlockHash            []byte
	AppHash              []byte
	ValidatorSetHash     []byte
	NextValidatorSetHash []byte
	BlockTimeUnix        int64
}

func VerifyCometBFTConsensusProof(domain DomainInfo, checkpoint Checkpoint) error {
	_, err := VerifyCometBFTConsensusProofHeader(domain, checkpoint)
	return err
}

func VerifyCometBFTConsensusProofHeader(domain DomainInfo, checkpoint Checkpoint) (VerifiedCometBFTConsensusProof, error) {
	var envelope CometBFTConsensusProofEnvelope
	if err := json.Unmarshal(checkpoint.ConsensusProof, &envelope); err != nil {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: unsupported consensus proof encoding", ErrConsensusProofInvalid)
	}
	if envelope.Version != "cometbft-signed-header-v1" {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: unsupported consensus proof version %q", ErrConsensusProofInvalid, envelope.Version)
	}
	if envelope.ChainID == "" {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: chain_id is required", ErrConsensusProofInvalid)
	}
	if domain.ChainId != "" && envelope.ChainID != domain.ChainId {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: proof chain_id=%s registered_chain_id=%s", ErrConsensusProofInvalid, envelope.ChainID, domain.ChainId)
	}
	if envelope.SignedHeader == nil || envelope.SignedHeader.Header == nil || envelope.SignedHeader.Commit == nil {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: signed_header is required", ErrConsensusProofInvalid)
	}
	if envelope.ValidatorSet == nil || envelope.ValidatorSet.IsNilOrEmpty() {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: validator_set is required", ErrConsensusProofInvalid)
	}
	if err := envelope.SignedHeader.ValidateBasic(envelope.ChainID); err != nil {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: invalid signed_header: %v", ErrConsensusProofInvalid, err)
	}
	if err := envelope.ValidatorSet.ValidateBasic(); err != nil {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: invalid validator_set: %v", ErrConsensusProofInvalid, err)
	}
	header := envelope.SignedHeader.Header
	if header.Height != int64(checkpoint.Height) {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: header height=%d checkpoint height=%d", ErrConsensusProofInvalid, header.Height, checkpoint.Height)
	}
	if checkpoint.ConsensusProofRevisionHeight != 0 && uint64(header.Height) != checkpoint.ConsensusProofRevisionHeight {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: proof revision height=%d header height=%d", ErrConsensusProofInvalid, checkpoint.ConsensusProofRevisionHeight, header.Height)
	}
	if !bytes.Equal(header.Hash(), checkpoint.BlockHash) {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: block hash mismatch", ErrConsensusProofInvalid)
	}
	if !bytes.Equal(header.AppHash, checkpoint.AppHash) {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: app hash mismatch", ErrConsensusProofInvalid)
	}
	validatorSetHash := envelope.ValidatorSet.Hash()
	if !bytes.Equal(header.ValidatorsHash, validatorSetHash) {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: validator set hash mismatch", ErrConsensusProofInvalid)
	}
	if len(checkpoint.ValidatorSetHash) > 0 && !bytes.Equal(checkpoint.ValidatorSetHash, validatorSetHash) {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: checkpoint validator set hash mismatch", ErrConsensusProofInvalid)
	}
	if err := envelope.ValidatorSet.VerifyCommitLight(envelope.ChainID, envelope.SignedHeader.Commit.BlockID, header.Height, envelope.SignedHeader.Commit); err != nil {
		return VerifiedCometBFTConsensusProof{}, fmt.Errorf("%w: invalid commit signatures: %v", ErrConsensusProofInvalid, err)
	}
	return VerifiedCometBFTConsensusProof{
		ChainID:              envelope.ChainID,
		Height:               uint64(header.Height),
		BlockHash:            header.Hash(),
		AppHash:              header.AppHash,
		ValidatorSetHash:     validatorSetHash,
		NextValidatorSetHash: header.NextValidatorsHash,
		BlockTimeUnix:        header.Time.Unix(),
	}, nil
}
