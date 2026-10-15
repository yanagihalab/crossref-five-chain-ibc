package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/crossref/crossrefd/x/crossref/types"
)

func (k Keeper) VerifyConsensusProof(ctx context.Context, checkpoint types.Checkpoint) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if !params.RequireConsensusProof && len(checkpoint.ConsensusProof) == 0 {
		return nil
	}
	if len(checkpoint.ConsensusProof) == 0 || checkpoint.ConsensusProofRevisionHeight == 0 {
		return errorsmod.Wrap(types.ErrConsensusProofRequired, "consensus_proof and consensus_proof_revision_height are required")
	}
	if len(checkpoint.StateHash) == 0 {
		checkpoint.StateHash = types.ComputeCheckpointStateHash(checkpoint)
	}
	expected := types.ComputeConsensusProofDigest(checkpoint)
	if !bytes.Equal(checkpoint.ConsensusProof, expected) {
		domain, found, err := k.GetDomain(ctx, checkpoint.DomainId)
		if err != nil {
			return err
		}
		if !found {
			return errorsmod.Wrapf(types.ErrDomainNotFound, "domain=%s", checkpoint.DomainId)
		}
		verified, err := types.VerifyCometBFTConsensusProofHeader(domain, checkpoint)
		if err != nil {
			return errorsmod.Wrapf(err, "domain=%s height=%d", checkpoint.DomainId, checkpoint.Height)
		}
		if err := k.UpdateTrustedLightClient(ctx, domain, checkpoint, verified); err != nil {
			return errorsmod.Wrapf(err, "domain=%s height=%d", checkpoint.DomainId, checkpoint.Height)
		}
	}
	return nil
}

func (k Keeper) UpdateTrustedLightClient(ctx context.Context, domain types.DomainInfo, checkpoint types.Checkpoint, verified types.VerifiedCometBFTConsensusProof) error {
	if verified.Height != checkpoint.Height {
		return errorsmod.Wrapf(types.ErrConsensusProofInvalid, "verified height=%d checkpoint height=%d", verified.Height, checkpoint.Height)
	}
	if len(domain.ValidatorSetHash) == 0 {
		return errorsmod.Wrap(types.ErrConsensusProofInvalid, "registered domain validator_set_hash trust root is required")
	}

	now := int64(0)
	if sdkCtx := sdk.UnwrapSDKContext(ctx); !sdkCtx.BlockTime().IsZero() {
		now = sdkCtx.BlockTime().Unix()
	}

	current, found, err := k.GetTrustedLightClientState(ctx, domain.DomainId)
	if err != nil {
		return err
	}
	trustedHeight := uint64(0)
	var trustedValidatorSetHash []byte
	verificationMode := "trust-root"

	if !found {
		if !bytes.Equal(domain.ValidatorSetHash, verified.ValidatorSetHash) {
			return errorsmod.Wrapf(types.ErrConsensusProofInvalid, "trust root validator_set_hash mismatch registered=%X proof=%X", domain.ValidatorSetHash, verified.ValidatorSetHash)
		}
		trustedValidatorSetHash = domain.ValidatorSetHash
	} else {
		if current.ChainId != "" && current.ChainId != verified.ChainID {
			return errorsmod.Wrapf(types.ErrConsensusProofInvalid, "trusted chain_id=%s proof chain_id=%s", current.ChainId, verified.ChainID)
		}
		if verified.Height < current.LatestHeight {
			return errorsmod.Wrapf(types.ErrConsensusProofInvalid, "trusted height regression current=%d proof=%d", current.LatestHeight, verified.Height)
		}
		if verified.Height == current.LatestHeight {
			if bytes.Equal(current.LatestBlockHash, verified.BlockHash) &&
				bytes.Equal(current.LatestAppHash, verified.AppHash) &&
				bytes.Equal(current.LatestValidatorSetHash, verified.ValidatorSetHash) {
				return nil
			}
			return errorsmod.Wrapf(types.ErrConsensusProofInvalid, "conflicting light client update at height=%d", verified.Height)
		}
		if len(current.NextValidatorSetHash) == 0 {
			return errorsmod.Wrap(types.ErrConsensusProofInvalid, "trusted next_validator_set_hash is missing")
		}
		if !bytes.Equal(current.NextValidatorSetHash, verified.ValidatorSetHash) {
			return errorsmod.Wrapf(types.ErrConsensusProofInvalid, "validator transition mismatch trusted_next=%X proof_validators=%X", current.NextValidatorSetHash, verified.ValidatorSetHash)
		}
		trustedHeight = current.LatestHeight
		trustedValidatorSetHash = current.LatestValidatorSetHash
		verificationMode = "trusted-next-validator-set"
	}

	nextState := types.TrustedLightClientState{
		DomainId:               domain.DomainId,
		ChainId:                verified.ChainID,
		LatestHeight:           verified.Height,
		LatestBlockHash:        verified.BlockHash,
		LatestAppHash:          verified.AppHash,
		LatestValidatorSetHash: verified.ValidatorSetHash,
		NextValidatorSetHash:   verified.NextValidatorSetHash,
		LatestBlockTimeUnix:    verified.BlockTimeUnix,
		UpdatedTimeUnix:        now,
		UpdateCount:            current.UpdateCount + 1,
	}
	if err := k.SetTrustedLightClientState(ctx, nextState); err != nil {
		return err
	}
	return k.RecordLightClientUpdate(ctx, types.LightClientUpdate{
		DomainId:                domain.DomainId,
		ChainId:                 verified.ChainID,
		TrustedHeight:           trustedHeight,
		TrustedValidatorSetHash: trustedValidatorSetHash,
		TargetHeight:            verified.Height,
		TargetBlockHash:         verified.BlockHash,
		TargetAppHash:           verified.AppHash,
		TargetValidatorSetHash:  verified.ValidatorSetHash,
		NextValidatorSetHash:    verified.NextValidatorSetHash,
		TargetBlockTimeUnix:     verified.BlockTimeUnix,
		UpdatedTimeUnix:         now,
		VerificationMode:        verificationMode,
	})
}
