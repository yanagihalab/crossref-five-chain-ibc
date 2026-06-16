package keeper

import (
	"bytes"
	"context"
	"time"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"

	"github.com/crossref/crossrefd/x/crossref/types"
)

func (k msgServer) RegisterDomain(ctx context.Context, req *types.MsgRegisterDomain) (*types.MsgRegisterDomainResponse, error) {
	if _, err := k.addressCodec.StringToBytes(req.Creator); err != nil {
		return nil, errorsmod.Wrap(err, "invalid creator address")
	}
	if req.DomainId == "" || req.ChainId == "" {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "domain_id and chain_id are required")
	}
	if _, found, err := k.GetDomain(ctx, req.DomainId); err != nil {
		return nil, err
	} else if found {
		return nil, errorsmod.Wrapf(types.ErrInvalidRequest, "domain already registered: %s", req.DomainId)
	}

	return &types.MsgRegisterDomainResponse{}, k.SetDomain(ctx, types.DomainInfo{
		DomainId:            req.DomainId,
		ChainId:             req.ChainId,
		ValidatorSetHash:    req.ValidatorSetHash,
		MetadataUri:         req.MetadataUri,
		HysteresisPublicKey: req.HysteresisPublicKey,
	})
}

func (k msgServer) BindDomainChannel(ctx context.Context, req *types.MsgBindDomainChannel) (*types.MsgBindDomainChannelResponse, error) {
	if _, err := k.addressCodec.StringToBytes(req.Creator); err != nil {
		return nil, errorsmod.Wrap(err, "invalid creator address")
	}
	if req.LocalDomainId == "" || req.RemoteDomainId == "" || req.PortId == "" || req.ChannelId == "" {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "local_domain_id, remote_domain_id, port_id and channel_id are required")
	}
	if _, found, err := k.GetDomain(ctx, req.LocalDomainId); err != nil {
		return nil, err
	} else if !found {
		return nil, errorsmod.Wrapf(types.ErrDomainNotFound, "local domain=%s", req.LocalDomainId)
	}
	if _, found, err := k.GetDomain(ctx, req.RemoteDomainId); err != nil {
		return nil, err
	} else if !found {
		return nil, errorsmod.Wrapf(types.ErrDomainNotFound, "remote domain=%s", req.RemoteDomainId)
	}

	return &types.MsgBindDomainChannelResponse{}, k.SetDomainChannel(ctx, types.DomainChannel{
		LocalDomainId:         req.LocalDomainId,
		RemoteDomainId:        req.RemoteDomainId,
		PortId:                req.PortId,
		ChannelId:             req.ChannelId,
		ClientId:              req.ClientId,
		CounterpartyPortId:    req.CounterpartyPortId,
		CounterpartyChannelId: req.CounterpartyChannelId,
	})
}

func (k msgServer) SubmitCheckpoint(ctx context.Context, req *types.MsgSubmitCheckpoint) (*types.MsgSubmitCheckpointResponse, error) {
	if _, err := k.addressCodec.StringToBytes(req.Creator); err != nil {
		return nil, errorsmod.Wrap(err, "invalid creator address")
	}
	if req.DomainId == "" || req.Height == 0 || len(req.BlockHash) == 0 || len(req.AppHash) == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "domain_id, height, block_hash and app_hash are required")
	}
	domain, found, err := k.GetDomain(ctx, req.DomainId)
	if err != nil {
		return nil, err
	} else if !found {
		return nil, errorsmod.Wrapf(types.ErrDomainNotFound, "domain=%s", req.DomainId)
	}

	checkpoint := types.Checkpoint{
		DomainId:               req.DomainId,
		Height:                 req.Height,
		BlockHash:              req.BlockHash,
		AppHash:                req.AppHash,
		ValidatorSetHash:       req.ValidatorSetHash,
		PreviousCheckpointHash: req.PreviousCheckpointHash,
		CheckpointHash:         req.CheckpointHash,
		HysteresisSignature:    req.HysteresisSignature,
		BlockTimeUnix:          req.BlockTimeUnix,
	}
	expectedHash := types.ComputeCheckpointHash(checkpoint)
	if len(checkpoint.CheckpointHash) == 0 {
		checkpoint.CheckpointHash = expectedHash
	} else if !bytes.Equal(checkpoint.CheckpointHash, expectedHash) {
		return nil, errorsmod.Wrapf(types.ErrCheckpointHashMismatch, "domain=%s height=%d", req.DomainId, req.Height)
	}
	if err := k.ValidateCheckpoint(ctx, checkpoint); err != nil {
		return nil, err
	}
	if err := types.VerifyHysteresisSignature(domain, checkpoint); err != nil {
		return nil, err
	}
	if err := k.SetCheckpoint(ctx, checkpoint); err != nil {
		return nil, err
	}

	return &types.MsgSubmitCheckpointResponse{CheckpointHash: checkpoint.CheckpointHash}, nil
}

func (k msgServer) SendCrossReferencePacket(ctx context.Context, req *types.MsgSendCrossReferencePacket) (*types.MsgSendCrossReferencePacketResponse, error) {
	if _, err := k.addressCodec.StringToBytes(req.Sender); err != nil {
		return nil, errorsmod.Wrap(err, "invalid sender address")
	}
	if req.SourceDomainId == "" || req.SourceHeight == 0 || req.PortId == "" || req.ChannelId == "" {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "source_domain_id, source_height, port_id and channel_id are required")
	}
	sequence, err := k.sendCrossReferencePacket(ctx, req.SourceDomainId, req.SourceHeight, req.PortId, req.ChannelId, req.TimeoutSeconds, req.SourceCheckpointProof, req.SourceProofRevisionNumber, req.SourceProofRevisionHeight)
	if err != nil {
		return nil, err
	}
	return &types.MsgSendCrossReferencePacketResponse{Sequence: sequence}, nil
}

func (k msgServer) BroadcastCrossReferencePacket(ctx context.Context, req *types.MsgBroadcastCrossReferencePacket) (*types.MsgBroadcastCrossReferencePacketResponse, error) {
	if _, err := k.addressCodec.StringToBytes(req.Sender); err != nil {
		return nil, errorsmod.Wrap(err, "invalid sender address")
	}
	if req.SourceDomainId == "" || req.SourceHeight == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "source_domain_id and source_height are required")
	}

	portID := req.PortId
	if portID == "" {
		portID = types.PortID
	}
	excluded := make(map[string]struct{}, len(req.ExcludeRemoteDomainIds))
	for _, domainID := range req.ExcludeRemoteDomainIds {
		excluded[domainID] = struct{}{}
	}

	bindings, err := k.ListDomainChannelsByLocalDomain(ctx, req.SourceDomainId, portID)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, errorsmod.Wrapf(types.ErrDomainChannelNotFound, "local domain=%s port=%s", req.SourceDomainId, portID)
	}

	results := make([]types.BroadcastCrossReferencePacketResult, 0, len(bindings))
	for _, binding := range bindings {
		if _, skip := excluded[binding.RemoteDomainId]; skip {
			continue
		}
		sequence, err := k.sendCrossReferencePacket(ctx, req.SourceDomainId, req.SourceHeight, binding.PortId, binding.ChannelId, req.TimeoutSeconds, req.SourceCheckpointProof, req.SourceProofRevisionNumber, req.SourceProofRevisionHeight)
		if err != nil {
			return nil, errorsmod.Wrapf(err, "remote domain=%s port=%s channel=%s", binding.RemoteDomainId, binding.PortId, binding.ChannelId)
		}
		results = append(results, types.BroadcastCrossReferencePacketResult{
			RemoteDomainId: binding.RemoteDomainId,
			PortId:         binding.PortId,
			ChannelId:      binding.ChannelId,
			Sequence:       sequence,
		})
	}
	if len(results) == 0 {
		return nil, errorsmod.Wrapf(types.ErrDomainChannelNotFound, "all bindings excluded for local domain=%s port=%s", req.SourceDomainId, portID)
	}

	return &types.MsgBroadcastCrossReferencePacketResponse{Results: results}, nil
}

func (k msgServer) sendCrossReferencePacket(ctx context.Context, sourceDomainID string, sourceHeight uint64, portID, channelID string, timeoutSeconds uint64, sourceCheckpointProof []byte, sourceProofRevisionNumber, sourceProofRevisionHeight uint64) (uint64, error) {
	domain, found, err := k.GetDomain(ctx, sourceDomainID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, errorsmod.Wrapf(types.ErrDomainNotFound, "domain=%s", sourceDomainID)
	}
	binding, found, err := k.GetDomainChannelByChannel(ctx, portID, channelID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, errorsmod.Wrapf(types.ErrDomainChannelNotFound, "port=%s channel=%s", portID, channelID)
	}
	if binding.LocalDomainId != sourceDomainID {
		return 0, errorsmod.Wrapf(types.ErrUnauthorizedChannel, "source domain=%s bound local domain=%s port=%s channel=%s", sourceDomainID, binding.LocalDomainId, portID, channelID)
	}
	checkpoint, found, err := k.GetCheckpoint(ctx, sourceDomainID, sourceHeight)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, errorsmod.Wrapf(types.ErrCheckpointNotFound, "domain=%s height=%d", sourceDomainID, sourceHeight)
	}
	ibcKeeper := k.ibcKeeperFn()
	if ibcKeeper == nil {
		return 0, errorsmod.Wrap(types.ErrInvalidRequest, "IBC keeper is not configured")
	}

	packet := types.CrossrefPacketData{
		Packet: &types.CrossrefPacketData_CrossReference{
			CrossReference: &types.CrossReferencePacketData{
				SourceDomainId:            checkpoint.DomainId,
				SourceChainId:             domain.ChainId,
				SourceHeight:              checkpoint.Height,
				BlockHash:                 checkpoint.BlockHash,
				AppHash:                   checkpoint.AppHash,
				ValidatorSetHash:          checkpoint.ValidatorSetHash,
				PreviousCheckpointHash:    checkpoint.PreviousCheckpointHash,
				CheckpointHash:            checkpoint.CheckpointHash,
				HysteresisSignature:       checkpoint.HysteresisSignature,
				BlockTimeUnix:             checkpoint.BlockTimeUnix,
				SourceCheckpointProof:     sourceCheckpointProof,
				SourceProofRevisionNumber: sourceProofRevisionNumber,
				SourceProofRevisionHeight: sourceProofRevisionHeight,
			},
		},
	}
	packetBz, err := packet.Marshal()
	if err != nil {
		return 0, err
	}
	if timeoutSeconds == 0 {
		timeoutSeconds = 600
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	timeoutTimestamp := uint64(sdkCtx.BlockTime().Add(time.Duration(timeoutSeconds) * time.Second).UnixNano())

	sequence, err := ibcKeeper.ChannelKeeper.SendPacket(sdkCtx, portID, channelID, clienttypes.ZeroHeight(), timeoutTimestamp, packetBz)
	if err != nil {
		return 0, err
	}
	if err := k.SetOutgoingPacket(ctx, portID, channelID, sequence, checkpoint.CheckpointHash); err != nil {
		return 0, err
	}
	return sequence, nil
}

func (k msgServer) requireAuthority(authority string) error {
	authorityBz, err := k.addressCodec.StringToBytes(authority)
	if err != nil {
		return errorsmod.Wrap(err, "invalid authority address")
	}
	if !bytes.Equal(k.GetAuthority(), authorityBz) {
		expected, _ := k.addressCodec.BytesToString(k.GetAuthority())
		return errorsmod.Wrapf(types.ErrInvalidSigner, "invalid authority; expected %s, got %s", expected, authority)
	}
	return nil
}
