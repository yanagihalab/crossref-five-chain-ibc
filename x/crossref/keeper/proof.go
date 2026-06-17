package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types"

	"github.com/crossref/crossrefd/x/crossref/types"
)

const maxCheckpointProofLag = uint64(10000)

func (k Keeper) VerifySourceCheckpointProof(ctx context.Context, binding types.DomainChannel, checkpoint types.Checkpoint, proof []byte, revisionNumber, revisionHeight uint64) error {
	if len(proof) == 0 || revisionHeight == 0 {
		return errorsmod.Wrap(types.ErrCheckpointProofRequired, "source_checkpoint_proof and source_proof_revision_height are required")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if currentHeight := uint64(sdkCtx.BlockHeight()); currentHeight > revisionHeight+maxCheckpointProofLag {
		return errorsmod.Wrapf(types.ErrCheckpointProofStale, "current_height=%d proof_height=%d max_lag=%d", currentHeight, revisionHeight, maxCheckpointProofLag)
	}

	key, err := types.CheckpointStorageKey(checkpoint.DomainId, checkpoint.Height)
	if err != nil {
		return err
	}
	value, err := k.Checkpoints.ValueCodec().Encode(checkpoint)
	if err != nil {
		return err
	}
	path := commitmenttypes.NewMerklePath([]byte(types.StoreKey), key)
	height := clienttypes.NewHeight(revisionNumber, revisionHeight)

	clientID, err := k.sourceClientID(sdkCtx, binding)
	if err != nil {
		return err
	}
	if k.proofVerifier != nil {
		if err := k.proofVerifier(sdkCtx, clientID, height, proof, path, value); err != nil {
			return errorsmod.Wrap(types.ErrCheckpointProofInvalid, err.Error())
		}
		return nil
	}

	ibcKeeper := k.ibcKeeperFn()
	if ibcKeeper == nil {
		return errorsmod.Wrap(types.ErrCheckpointProofInvalid, "IBC keeper is not configured")
	}
	if err := ibcKeeper.ClientKeeper.VerifyMembership(sdkCtx, clientID, height, 0, 0, proof, path, value); err != nil {
		return errorsmod.Wrap(types.ErrCheckpointProofInvalid, err.Error())
	}
	return nil
}

func (k Keeper) sourceClientID(ctx sdk.Context, binding types.DomainChannel) (string, error) {
	if binding.ClientId != "" {
		return binding.ClientId, nil
	}

	ibcKeeper := k.ibcKeeperFn()
	if ibcKeeper == nil {
		return "", errorsmod.Wrap(types.ErrCheckpointProofInvalid, "IBC keeper is not configured")
	}
	channel, found := ibcKeeper.ChannelKeeper.GetChannel(ctx, binding.PortId, binding.ChannelId)
	if !found {
		return "", errorsmod.Wrapf(types.ErrDomainChannelNotFound, "port=%s channel=%s", binding.PortId, binding.ChannelId)
	}
	if len(channel.ConnectionHops) == 0 {
		return "", errorsmod.Wrapf(types.ErrCheckpointProofInvalid, "channel %s/%s has no connection hops", binding.PortId, binding.ChannelId)
	}
	connection, found := ibcKeeper.ConnectionKeeper.GetConnection(ctx, channel.ConnectionHops[0])
	if !found {
		return "", errorsmod.Wrapf(types.ErrCheckpointProofInvalid, "connection not found: %s", channel.ConnectionHops[0])
	}
	if connection.ClientId == "" {
		return "", errorsmod.Wrapf(types.ErrCheckpointProofInvalid, "connection %s has no client id", channel.ConnectionHops[0])
	}
	return connection.ClientId, nil
}
