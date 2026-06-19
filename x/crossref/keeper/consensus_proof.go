package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"

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
		return errorsmod.Wrapf(types.ErrConsensusProofInvalid, "domain=%s height=%d", checkpoint.DomainId, checkpoint.Height)
	}
	return nil
}
