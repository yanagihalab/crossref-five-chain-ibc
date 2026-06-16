package crossref

import (
	"bytes"
	"fmt"

	errorsmod "cosmossdk.io/errors"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v10/modules/core/exported"
	"github.com/crossref/crossrefd/x/crossref/keeper"
	"github.com/crossref/crossrefd/x/crossref/types"
)

// IBCModule implements the ICS26 interface for interchain accounts host chains
type IBCModule struct {
	cdc    codec.Codec
	keeper keeper.Keeper
}

// NewIBCModule creates a new IBCModule given the associated keeper
func NewIBCModule(cdc codec.Codec, k keeper.Keeper) IBCModule {
	return IBCModule{
		cdc:    cdc,
		keeper: k,
	}
}

// OnChanOpenInit implements the IBCModule interface
func (im IBCModule) OnChanOpenInit(
	ctx sdk.Context,
	order channeltypes.Order,
	connectionHops []string,
	portID string,
	channelID string,
	counterparty channeltypes.Counterparty,
	version string,
) (string, error) {
	if version == "" {
		version = types.Version
	}
	if version != types.Version {
		return "", errorsmod.Wrapf(types.ErrInvalidVersion, "got %s, expected %s", version, types.Version)
	}

	return version, nil
}

// OnChanOpenTry implements the IBCModule interface
func (im IBCModule) OnChanOpenTry(
	ctx sdk.Context,
	order channeltypes.Order,
	connectionHops []string,
	portID,
	channelID string,
	counterparty channeltypes.Counterparty,
	counterpartyVersion string,
) (string, error) {
	if counterpartyVersion != types.Version {
		return "", errorsmod.Wrapf(types.ErrInvalidVersion, "invalid counterparty version: got: %s, expected %s", counterpartyVersion, types.Version)
	}

	return counterpartyVersion, nil
}

// OnChanOpenAck implements the IBCModule interface
func (im IBCModule) OnChanOpenAck(
	ctx sdk.Context,
	portID,
	channelID,
	counterpartyChannelID,
	counterpartyVersion string,
) error {
	if counterpartyVersion != types.Version {
		return errorsmod.Wrapf(types.ErrInvalidVersion, "invalid counterparty version: %s, expected %s", counterpartyVersion, types.Version)
	}
	return nil
}

// OnChanOpenConfirm implements the IBCModule interface
func (im IBCModule) OnChanOpenConfirm(
	ctx sdk.Context,
	portID,
	channelID string,
) error {
	return nil
}

// OnChanCloseInit implements the IBCModule interface
func (im IBCModule) OnChanCloseInit(
	ctx sdk.Context,
	portID,
	channelID string,
) error {
	// Disallow user-initiated channel closing for channels
	return errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "user cannot close channel")
}

// OnChanCloseConfirm implements the IBCModule interface
func (im IBCModule) OnChanCloseConfirm(
	ctx sdk.Context,
	portID,
	channelID string,
) error {
	return nil
}

// OnRecvPacket implements the IBCModule interface
func (im IBCModule) OnRecvPacket(
	ctx sdk.Context,
	channelVersion string,
	modulePacket channeltypes.Packet,
	relayer sdk.AccAddress,
) ibcexported.Acknowledgement {
	var modulePacketData types.CrossrefPacketData
	if err := modulePacketData.Unmarshal(modulePacket.GetData()); err != nil {
		return channeltypes.NewErrorAcknowledgement(errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "cannot unmarshal packet data: %s", err.Error()))
	}

	// Dispatch packet
	switch packet := modulePacketData.Packet.(type) {
	case *types.CrossrefPacketData_CrossReference:
		hash, err := im.receiveCrossReferencePacket(ctx, modulePacket.GetDestPort(), modulePacket.GetDestChannel(), relayer.String(), packet.CrossReference)
		if err != nil {
			return channeltypes.NewErrorAcknowledgement(err)
		}
		ackBz := im.cdc.MustMarshalJSON(&types.CrossReferencePacketAck{
			Accepted:             true,
			Code:                 "accepted",
			StoredCheckpointHash: hash,
		})
		return channeltypes.NewResultAcknowledgement(ackBz)
	default:
		err := fmt.Errorf("unrecognized %s packet type: %T", types.ModuleName, packet)
		return channeltypes.NewErrorAcknowledgement(err)
	}
}

// OnAcknowledgementPacket implements the IBCModule interface
func (im IBCModule) OnAcknowledgementPacket(
	ctx sdk.Context,
	channelVersion string,
	modulePacket channeltypes.Packet,
	acknowledgement []byte,
	relayer sdk.AccAddress,
) error {
	var ack channeltypes.Acknowledgement
	if err := im.cdc.UnmarshalJSON(acknowledgement, &ack); err != nil {
		return errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "cannot unmarshal packet acknowledgement: %v", err)
	}

	var modulePacketData types.CrossrefPacketData
	if err := modulePacketData.Unmarshal(modulePacket.GetData()); err != nil {
		return errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "cannot unmarshal packet data: %s", err.Error())
	}

	var eventType string

	// Dispatch packet
	switch packet := modulePacketData.Packet.(type) {
	case *types.CrossrefPacketData_CrossReference:
		eventType = "crossref_ack"
		_ = packet
	default:
		errMsg := fmt.Sprintf("unrecognized %s packet type: %T", types.ModuleName, packet)
		return errorsmod.Wrap(sdkerrors.ErrUnknownRequest, errMsg)
	}

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			eventType,
			sdk.NewAttribute(types.AttributeKeyAck, fmt.Sprintf("%v", ack)),
		),
	)

	switch resp := ack.Response.(type) {
	case *channeltypes.Acknowledgement_Result:
		ctx.EventManager().EmitEvent(
			sdk.NewEvent(
				eventType,
				sdk.NewAttribute(types.AttributeKeyAckSuccess, string(resp.Result)),
			),
		)
	case *channeltypes.Acknowledgement_Error:
		ctx.EventManager().EmitEvent(
			sdk.NewEvent(
				eventType,
				sdk.NewAttribute(types.AttributeKeyAckError, resp.Error),
			),
		)
	}

	return nil
}

// OnTimeoutPacket implements the IBCModule interface
func (im IBCModule) OnTimeoutPacket(
	ctx sdk.Context,
	channelVersion string,
	modulePacket channeltypes.Packet,
	relayer sdk.AccAddress,
) error {
	var modulePacketData types.CrossrefPacketData
	if err := modulePacketData.Unmarshal(modulePacket.GetData()); err != nil {
		return errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "cannot unmarshal packet data: %s", err.Error())
	}

	// Dispatch packet
	switch packet := modulePacketData.Packet.(type) {
	case *types.CrossrefPacketData_CrossReference:
		_ = packet
	default:
		errMsg := fmt.Sprintf("unrecognized %s packet type: %T", types.ModuleName, packet)
		return errorsmod.Wrap(sdkerrors.ErrUnknownRequest, errMsg)
	}

	return nil
}

func (im IBCModule) receiveCrossReferencePacket(ctx sdk.Context, portID, channelID, relayer string, data *types.CrossReferencePacketData) ([]byte, error) {
	if data == nil || data.SourceDomainId == "" {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "source_domain_id is required")
	}
	if data.SourceHeight == 0 || len(data.BlockHash) == 0 || len(data.AppHash) == 0 || len(data.CheckpointHash) == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidRequest, "source_height, block_hash, app_hash and checkpoint_hash are required")
	}
	binding, found, err := im.keeper.GetDomainChannelByChannel(ctx, portID, channelID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errorsmod.Wrapf(types.ErrDomainChannelNotFound, "port=%s channel=%s", portID, channelID)
	}
	if binding.RemoteDomainId != data.SourceDomainId {
		return nil, errorsmod.Wrapf(types.ErrUnauthorizedChannel, "packet domain=%s bound domain=%s", data.SourceDomainId, binding.RemoteDomainId)
	}
	sourceDomain, found, err := im.keeper.GetDomain(ctx, data.SourceDomainId)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errorsmod.Wrapf(types.ErrDomainNotFound, "source domain=%s", data.SourceDomainId)
	}
	if sourceDomain.ChainId != data.SourceChainId {
		return nil, errorsmod.Wrapf(types.ErrSourceChainMismatch, "domain=%s packet_chain=%s registered_chain=%s", data.SourceDomainId, data.SourceChainId, sourceDomain.ChainId)
	}

	checkpoint := types.Checkpoint{
		DomainId:               data.SourceDomainId,
		Height:                 data.SourceHeight,
		BlockHash:              data.BlockHash,
		AppHash:                data.AppHash,
		ValidatorSetHash:       data.ValidatorSetHash,
		PreviousCheckpointHash: data.PreviousCheckpointHash,
		CheckpointHash:         data.CheckpointHash,
		HysteresisSignature:    data.HysteresisSignature,
		BlockTimeUnix:          data.BlockTimeUnix,
	}
	if !bytes.Equal(types.ComputeCheckpointHash(checkpoint), checkpoint.CheckpointHash) {
		return nil, errorsmod.Wrap(types.ErrCheckpointHashMismatch, "packet checkpoint hash mismatch")
	}
	if err := im.keeper.VerifySourceCheckpointProof(ctx, binding, checkpoint, data.SourceCheckpointProof, data.SourceProofRevisionNumber, data.SourceProofRevisionHeight); err != nil {
		return nil, err
	}
	if err := im.keeper.ValidateCheckpoint(ctx, checkpoint); err != nil {
		return nil, err
	}
	if err := im.keeper.SetCheckpoint(ctx, checkpoint); err != nil {
		return nil, err
	}
	if err := im.keeper.SetCrossReference(ctx, types.CrossReference{
		LocalDomainId:        binding.LocalDomainId,
		RemoteDomainId:       data.SourceDomainId,
		RemoteHeight:         data.SourceHeight,
		RemoteCheckpointHash: data.CheckpointHash,
		PortId:               portID,
		ChannelId:            channelID,
		Relayer:              relayer,
		ReceivedTimeUnix:     ctx.BlockTime().Unix(),
	}); err != nil {
		return nil, err
	}
	return checkpoint.CheckpointHash, nil
}
