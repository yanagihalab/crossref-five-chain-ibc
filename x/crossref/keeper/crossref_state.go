package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"github.com/crossref/crossrefd/x/crossref/types"
)

func domainChannelKey(localDomainID, remoteDomainID, portID, channelID string) string {
	return fmt.Sprintf("%s/%s/%s/%s", localDomainID, remoteDomainID, portID, channelID)
}

func channelDomainKey(portID, channelID string) string {
	return fmt.Sprintf("%s/%s", portID, channelID)
}

func checkpointKey(domainID string, height uint64) string {
	return fmt.Sprintf("%s/%020d", domainID, height)
}

func crossReferenceKey(localDomainID, remoteDomainID string, remoteHeight uint64) string {
	return fmt.Sprintf("%s/%s/%020d", localDomainID, remoteDomainID, remoteHeight)
}

func outgoingPacketKey(portID, channelID string, sequence uint64) string {
	return fmt.Sprintf("%s/%s/%020d", portID, channelID, sequence)
}

func (k Keeper) SetDomain(ctx context.Context, domain types.DomainInfo) error {
	return k.Domains.Set(ctx, domain.DomainId, domain)
}

func (k Keeper) GetDomain(ctx context.Context, domainID string) (types.DomainInfo, bool, error) {
	domain, err := k.Domains.Get(ctx, domainID)
	if err != nil {
		if errorsmod.IsOf(err, collections.ErrNotFound) {
			return types.DomainInfo{}, false, nil
		}
		return types.DomainInfo{}, false, err
	}
	return domain, true, nil
}

func (k Keeper) SetDomainChannel(ctx context.Context, binding types.DomainChannel) error {
	if err := k.DomainChannels.Set(ctx, domainChannelKey(binding.LocalDomainId, binding.RemoteDomainId, binding.PortId, binding.ChannelId), binding); err != nil {
		return err
	}
	return k.ChannelDomains.Set(ctx, channelDomainKey(binding.PortId, binding.ChannelId), binding)
}

func (k Keeper) GetDomainChannelByChannel(ctx context.Context, portID, channelID string) (types.DomainChannel, bool, error) {
	binding, err := k.ChannelDomains.Get(ctx, channelDomainKey(portID, channelID))
	if err != nil {
		if errorsmod.IsOf(err, collections.ErrNotFound) {
			return types.DomainChannel{}, false, nil
		}
		return types.DomainChannel{}, false, err
	}
	return binding, true, nil
}

func (k Keeper) SetCheckpoint(ctx context.Context, checkpoint types.Checkpoint) error {
	if err := k.Checkpoints.Set(ctx, checkpointKey(checkpoint.DomainId, checkpoint.Height), checkpoint); err != nil {
		return err
	}
	return k.LatestCheckpoints.Set(ctx, checkpoint.DomainId, checkpoint.Height)
}

func (k Keeper) GetCheckpoint(ctx context.Context, domainID string, height uint64) (types.Checkpoint, bool, error) {
	checkpoint, err := k.Checkpoints.Get(ctx, checkpointKey(domainID, height))
	if err != nil {
		if errorsmod.IsOf(err, collections.ErrNotFound) {
			return types.Checkpoint{}, false, nil
		}
		return types.Checkpoint{}, false, err
	}
	return checkpoint, true, nil
}

func (k Keeper) GetLatestCheckpoint(ctx context.Context, domainID string) (types.Checkpoint, bool, error) {
	height, err := k.LatestCheckpoints.Get(ctx, domainID)
	if err != nil {
		if errorsmod.IsOf(err, collections.ErrNotFound) {
			return types.Checkpoint{}, false, nil
		}
		return types.Checkpoint{}, false, err
	}
	return k.GetCheckpoint(ctx, domainID, height)
}

func (k Keeper) ValidateCheckpoint(ctx context.Context, checkpoint types.Checkpoint) error {
	expectedHash := types.ComputeCheckpointHash(checkpoint)
	if len(checkpoint.CheckpointHash) == 0 {
		checkpoint.CheckpointHash = expectedHash
	}
	if !bytes.Equal(expectedHash, checkpoint.CheckpointHash) {
		return errorsmod.Wrapf(types.ErrCheckpointHashMismatch, "domain=%s height=%d", checkpoint.DomainId, checkpoint.Height)
	}

	if existing, found, err := k.GetCheckpoint(ctx, checkpoint.DomainId, checkpoint.Height); err != nil {
		return err
	} else if found {
		if !bytes.Equal(existing.CheckpointHash, checkpoint.CheckpointHash) {
			return errorsmod.Wrapf(types.ErrCheckpointConflict, "domain=%s height=%d", checkpoint.DomainId, checkpoint.Height)
		}
		return nil
	}

	if latest, found, err := k.GetLatestCheckpoint(ctx, checkpoint.DomainId); err != nil {
		return err
	} else if found {
		if checkpoint.Height <= latest.Height {
			return errorsmod.Wrapf(types.ErrCheckpointConflict, "domain=%s height=%d latest=%d", checkpoint.DomainId, checkpoint.Height, latest.Height)
		}
		if !bytes.Equal(checkpoint.PreviousCheckpointHash, latest.CheckpointHash) {
			return errorsmod.Wrapf(types.ErrPreviousCheckpointBroken, "domain=%s height=%d latest=%d", checkpoint.DomainId, checkpoint.Height, latest.Height)
		}
	}

	return nil
}

func (k Keeper) SetCrossReference(ctx context.Context, reference types.CrossReference) error {
	return k.CrossReferences.Set(ctx, crossReferenceKey(reference.LocalDomainId, reference.RemoteDomainId, reference.RemoteHeight), reference)
}

func (k Keeper) GetCrossReference(ctx context.Context, localDomainID, remoteDomainID string, remoteHeight uint64) (types.CrossReference, bool, error) {
	reference, err := k.CrossReferences.Get(ctx, crossReferenceKey(localDomainID, remoteDomainID, remoteHeight))
	if err != nil {
		if errorsmod.IsOf(err, collections.ErrNotFound) {
			return types.CrossReference{}, false, nil
		}
		return types.CrossReference{}, false, err
	}
	return reference, true, nil
}

func (k Keeper) SetOutgoingPacket(ctx context.Context, portID, channelID string, sequence uint64, checkpointHash []byte) error {
	return k.OutgoingPackets.Set(ctx, outgoingPacketKey(portID, channelID, sequence), checkpointHash)
}
