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
		DomainId:         req.DomainId,
		ChainId:          req.ChainId,
		ValidatorSetHash: req.ValidatorSetHash,
		MetadataUri:      req.MetadataUri,
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
	if _, found, err := k.GetDomain(ctx, req.DomainId); err != nil {
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
	ibcKeeper := k.ibcKeeperFn()
	if ibcKeeper == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "IBC keeper is not configured")
	}
	domain, found, err := k.GetDomain(ctx, req.SourceDomainId)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errorsmod.Wrapf(types.ErrDomainNotFound, "domain=%s", req.SourceDomainId)
	}
	checkpoint, found, err := k.GetCheckpoint(ctx, req.SourceDomainId, req.SourceHeight)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errorsmod.Wrapf(types.ErrCheckpointNotFound, "domain=%s height=%d", req.SourceDomainId, req.SourceHeight)
	}

	packet := types.CrossrefPacketData{
		Packet: &types.CrossrefPacketData_CrossReference{
			CrossReference: &types.CrossReferencePacketData{
				SourceDomainId:         checkpoint.DomainId,
				SourceChainId:          domain.ChainId,
				SourceHeight:           checkpoint.Height,
				BlockHash:              checkpoint.BlockHash,
				AppHash:                checkpoint.AppHash,
				ValidatorSetHash:       checkpoint.ValidatorSetHash,
				PreviousCheckpointHash: checkpoint.PreviousCheckpointHash,
				CheckpointHash:         checkpoint.CheckpointHash,
				HysteresisSignature:    checkpoint.HysteresisSignature,
				BlockTimeUnix:          checkpoint.BlockTimeUnix,
			},
		},
	}
	packetBz, err := packet.Marshal()
	if err != nil {
		return nil, err
	}
	timeoutSeconds := req.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = 600
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	timeoutTimestamp := uint64(sdkCtx.BlockTime().Add(time.Duration(timeoutSeconds) * time.Second).UnixNano())

	sequence, err := ibcKeeper.ChannelKeeper.SendPacket(sdkCtx, req.PortId, req.ChannelId, clienttypes.ZeroHeight(), timeoutTimestamp, packetBz)
	if err != nil {
		return nil, err
	}
	if err := k.SetOutgoingPacket(ctx, req.PortId, req.ChannelId, sequence, checkpoint.CheckpointHash); err != nil {
		return nil, err
	}
	return &types.MsgSendCrossReferencePacketResponse{Sequence: sequence}, nil
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
