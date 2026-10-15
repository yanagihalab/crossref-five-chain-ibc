package crossref

import (
	"bytes"
	"fmt"

	errorsmod "cosmossdk.io/errors"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	porttypes "github.com/cosmos/ibc-go/v11/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v11/modules/core/exported"
	"github.com/crossref/crossrefd/x/crossref/keeper"
	"github.com/crossref/crossrefd/x/crossref/types"
)

// IBCModule implements the ICS26 interface for interchain accounts host chains
type IBCModule struct {
	cdc    codec.Codec
	keeper keeper.Keeper
}

type crossReferenceReceipt struct {
	localDomainID             string
	remoteDomainID            string
	remoteHeight              uint64
	portID                    string
	channelID                 string
	packetSequence            uint64
	storedCheckpointHash      []byte
	destinationReceivedHeight uint64
	destinationReceivedTime   int64
}

// NewIBCModule creates a new IBCModule given the associated keeper
func NewIBCModule(cdc codec.Codec, k keeper.Keeper) IBCModule {
	return IBCModule{
		cdc:    cdc,
		keeper: k,
	}
}

// SetICS4Wrapper implements the IBCModule interface for ibc-go v11 middleware stacks.
func (im IBCModule) SetICS4Wrapper(porttypes.ICS4Wrapper) {}

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
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "ibc_recv_callback")
	var modulePacketData types.CrossrefPacketData
	if err := modulePacketData.Unmarshal(modulePacket.GetData()); err != nil {
		return channeltypes.NewErrorAcknowledgement(errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "cannot unmarshal packet data: %s", err.Error()))
	}

	// Dispatch packet
	switch packet := modulePacketData.Packet.(type) {
	case *types.CrossrefPacketData_History:
		if err := im.keeper.ReceiveHistoryPacket(ctx, modulePacket, packet.History); err != nil {
			return channeltypes.NewErrorAcknowledgement(err)
		}
		return channeltypes.NewResultAcknowledgement([]byte("history-v2-buffered-or-stored"))
	case *types.CrossrefPacketData_CrossReference:
		receipt, err := im.receiveCrossReferencePacket(ctx, modulePacket.GetDestPort(), modulePacket.GetDestChannel(), modulePacket.GetSequence(), relayer.String(), packet.CrossReference)
		if err != nil {
			return channeltypes.NewErrorAcknowledgement(err)
		}
		ackBz := im.cdc.MustMarshalJSON(&types.CrossReferencePacketAck{
			Accepted:                    true,
			Code:                        "accepted",
			Message:                     "cross-reference stored",
			StoredCheckpointHash:        receipt.storedCheckpointHash,
			LocalDomainId:               receipt.localDomainID,
			RemoteDomainId:              receipt.remoteDomainID,
			RemoteHeight:                receipt.remoteHeight,
			PortId:                      receipt.portID,
			ChannelId:                   receipt.channelID,
			PacketSequence:              receipt.packetSequence,
			DestinationReceivedHeight:   receipt.destinationReceivedHeight,
			DestinationReceivedTimeUnix: receipt.destinationReceivedTime,
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
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "ibc_ack_callback")
	var ack channeltypes.Acknowledgement
	if err := im.cdc.UnmarshalJSON(acknowledgement, &ack); err != nil {
		return errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "cannot unmarshal packet acknowledgement: %v", err)
	}

	var modulePacketData types.CrossrefPacketData
	if err := modulePacketData.Unmarshal(modulePacket.GetData()); err != nil {
		return errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "cannot unmarshal packet data: %s", err.Error())
	}

	var eventType string
	if packet, ok := modulePacketData.Packet.(*types.CrossrefPacketData_History); ok {
		return im.keeper.HistoryPacketResult(ctx, modulePacket, packet.History, ack.Success() && bytes.Equal(ack.GetResult(), []byte("history-v2-buffered-or-stored")))
	}
	var sourceDomainID string
	var sourceHeight uint64

	// Dispatch packet
	switch packet := modulePacketData.Packet.(type) {
	case *types.CrossrefPacketData_CrossReference:
		eventType = "crossref_ack"
		sourceDomainID = packet.CrossReference.SourceDomainId
		sourceHeight = packet.CrossReference.SourceHeight
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
		var crossrefAck types.CrossReferencePacketAck
		if err := im.cdc.UnmarshalJSON(resp.Result, &crossrefAck); err != nil {
			return errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "cannot unmarshal crossref acknowledgement: %v", err)
		}
		if err := im.keeper.SetCrossReferenceAck(ctx, types.CrossReferenceAck{
			SourceDomainId:       sourceDomainID,
			SourceHeight:         sourceHeight,
			PortId:               modulePacket.GetSourcePort(),
			ChannelId:            modulePacket.GetSourceChannel(),
			PacketSequence:       modulePacket.GetSequence(),
			IbcAckSuccess:        true,
			IbcAcknowledgement:   acknowledgement,
			CrossrefAck:          &crossrefAck,
			AcknowledgedHeight:   uint64(ctx.BlockHeight()),
			AcknowledgedTimeUnix: ctx.BlockTime().Unix(),
			Relayer:              relayer.String(),
		}); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(
			sdk.NewEvent(
				eventType,
				sdk.NewAttribute(types.AttributeKeyAckSuccess, string(resp.Result)),
			),
		)
	case *channeltypes.Acknowledgement_Error:
		if err := im.keeper.SetCrossReferenceAck(ctx, types.CrossReferenceAck{
			SourceDomainId:       sourceDomainID,
			SourceHeight:         sourceHeight,
			PortId:               modulePacket.GetSourcePort(),
			ChannelId:            modulePacket.GetSourceChannel(),
			PacketSequence:       modulePacket.GetSequence(),
			IbcAckSuccess:        false,
			IbcAckError:          resp.Error,
			IbcAcknowledgement:   acknowledgement,
			AcknowledgedHeight:   uint64(ctx.BlockHeight()),
			AcknowledgedTimeUnix: ctx.BlockTime().Unix(),
			Relayer:              relayer.String(),
		}); err != nil {
			return err
		}
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
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "ibc_timeout_callback")
	var modulePacketData types.CrossrefPacketData
	if err := modulePacketData.Unmarshal(modulePacket.GetData()); err != nil {
		return errorsmod.Wrapf(sdkerrors.ErrUnknownRequest, "cannot unmarshal packet data: %s", err.Error())
	}

	// Dispatch packet
	switch packet := modulePacketData.Packet.(type) {
	case *types.CrossrefPacketData_History:
		return im.keeper.HistoryPacketResult(ctx, modulePacket, packet.History, false)
	case *types.CrossrefPacketData_CrossReference:
		_ = packet
	default:
		errMsg := fmt.Sprintf("unrecognized %s packet type: %T", types.ModuleName, packet)
		return errorsmod.Wrap(sdkerrors.ErrUnknownRequest, errMsg)
	}

	return nil
}

func (im IBCModule) receiveCrossReferencePacket(ctx sdk.Context, portID, channelID string, packetSequence uint64, relayer string, data *types.CrossReferencePacketData) (crossReferenceReceipt, error) {
	var empty crossReferenceReceipt
	if data == nil || data.SourceDomainId == "" {
		return empty, errorsmod.Wrap(types.ErrInvalidRequest, "source_domain_id is required")
	}
	if data.SourceHeight == 0 || len(data.BlockHash) == 0 || len(data.AppHash) == 0 || len(data.CheckpointHash) == 0 {
		return empty, errorsmod.Wrap(types.ErrInvalidRequest, "source_height, block_hash, app_hash and checkpoint_hash are required")
	}
	binding, found, err := im.keeper.GetDomainChannelByChannel(ctx, portID, channelID)
	if err != nil {
		return empty, err
	}
	if !found {
		return empty, errorsmod.Wrapf(types.ErrDomainChannelNotFound, "port=%s channel=%s", portID, channelID)
	}
	if binding.RemoteDomainId != data.SourceDomainId {
		return empty, errorsmod.Wrapf(types.ErrUnauthorizedChannel, "packet domain=%s bound domain=%s", data.SourceDomainId, binding.RemoteDomainId)
	}
	sourceDomain, found, err := im.keeper.GetDomain(ctx, data.SourceDomainId)
	if err != nil {
		return empty, err
	}
	if !found {
		return empty, errorsmod.Wrapf(types.ErrDomainNotFound, "source domain=%s", data.SourceDomainId)
	}
	if sourceDomain.ChainId != data.SourceChainId {
		return empty, errorsmod.Wrapf(types.ErrSourceChainMismatch, "domain=%s packet_chain=%s registered_chain=%s", data.SourceDomainId, data.SourceChainId, sourceDomain.ChainId)
	}

	checkpoint := types.Checkpoint{
		DomainId:                     data.SourceDomainId,
		Height:                       data.SourceHeight,
		BlockHash:                    data.BlockHash,
		AppHash:                      data.AppHash,
		ValidatorSetHash:             data.ValidatorSetHash,
		PreviousCheckpointHash:       data.PreviousCheckpointHash,
		CheckpointHash:               data.CheckpointHash,
		HysteresisSignature:          data.HysteresisSignature,
		BlockTimeUnix:                data.BlockTimeUnix,
		StateHash:                    data.StateHash,
		PreviousStateHash:            data.PreviousStateHash,
		ConsensusProof:               data.ConsensusProof,
		ConsensusProofRevisionNumber: data.ConsensusProofRevisionNumber,
		ConsensusProofRevisionHeight: data.ConsensusProofRevisionHeight,
		KeyEpoch:                     data.KeyEpoch,
	}
	if checkpoint.KeyEpoch == 0 {
		checkpoint.KeyEpoch = sourceDomain.KeyEpoch
	}
	if err := types.NormalizeCheckpointHashes(&checkpoint); err != nil {
		return empty, errorsmod.Wrap(err, "packet checkpoint hash mismatch")
	}
	if !bytes.Equal(types.ComputeCheckpointHash(checkpoint), checkpoint.CheckpointHash) {
		return empty, errorsmod.Wrap(types.ErrCheckpointHashMismatch, "packet checkpoint hash mismatch")
	}
	if err := im.keeper.VerifySourceCheckpointProof(ctx, binding, checkpoint, data.SourceCheckpointProof, data.SourceProofRevisionNumber, data.SourceProofRevisionHeight); err != nil {
		_ = im.keeper.RecordAccountabilityEvent(ctx, packetAccountabilityEvent(data, relayer, "invalid_source_checkpoint_proof", err.Error(), "reject_packet"))
		return empty, err
	}
	if err := im.keeper.VerifyConsensusProof(ctx, checkpoint); err != nil {
		_ = im.keeper.RecordAccountabilityEvent(ctx, packetAccountabilityEvent(data, relayer, "invalid_consensus_proof", err.Error(), "reject_packet"))
		return empty, err
	}
	if err := im.keeper.ValidateCheckpoint(ctx, checkpoint); err != nil {
		_ = im.keeper.RecordAccountabilityEvent(ctx, packetAccountabilityEvent(data, relayer, "invalid_checkpoint_chain", err.Error(), "reject_packet"))
		return empty, err
	}
	if err := types.VerifyHysteresisSignature(sourceDomain, checkpoint); err != nil {
		_ = im.keeper.RecordAccountabilityEvent(ctx, packetAccountabilityEvent(data, relayer, "invalid_hysteresis_signature", err.Error(), "reject_packet"))
		return empty, err
	}
	if existing, found, err := im.keeper.GetCrossReference(ctx, binding.LocalDomainId, data.SourceDomainId, data.SourceHeight); err != nil {
		return empty, err
	} else if found {
		_ = im.keeper.RecordAccountabilityEvent(ctx, packetAccountabilityEvent(data, relayer, "replay_packet", fmt.Sprintf("existing_hash=%X packet_hash=%X", existing.RemoteCheckpointHash, data.CheckpointHash), "reject_packet"))
		return empty, errorsmod.Wrapf(types.ErrReplayPacket, "local=%s remote=%s height=%d existing_hash=%X packet_hash=%X", binding.LocalDomainId, data.SourceDomainId, data.SourceHeight, existing.RemoteCheckpointHash, data.CheckpointHash)
	}
	if err := im.keeper.SetCheckpoint(ctx, checkpoint); err != nil {
		return empty, err
	}
	receivedHeight := uint64(ctx.BlockHeight())
	receivedTime := ctx.BlockTime().Unix()
	if err := im.keeper.SetCrossReference(ctx, types.CrossReference{
		LocalDomainId:             binding.LocalDomainId,
		RemoteDomainId:            data.SourceDomainId,
		RemoteHeight:              data.SourceHeight,
		RemoteCheckpointHash:      data.CheckpointHash,
		PortId:                    portID,
		ChannelId:                 channelID,
		Relayer:                   relayer,
		ReceivedTimeUnix:          receivedTime,
		DestinationReceivedHeight: receivedHeight,
		PacketSequence:            packetSequence,
	}); err != nil {
		return empty, err
	}
	return crossReferenceReceipt{
		localDomainID:             binding.LocalDomainId,
		remoteDomainID:            data.SourceDomainId,
		remoteHeight:              data.SourceHeight,
		portID:                    portID,
		channelID:                 channelID,
		packetSequence:            packetSequence,
		storedCheckpointHash:      checkpoint.CheckpointHash,
		destinationReceivedHeight: receivedHeight,
		destinationReceivedTime:   receivedTime,
	}, nil
}

func packetAccountabilityEvent(data *types.CrossReferencePacketData, relayer, eventType, evidence, action string) types.AccountabilityEvent {
	return types.AccountabilityEvent{
		EventId:           fmt.Sprintf("%s/%d/%s/%s", data.SourceDomainId, data.SourceHeight, eventType, relayer),
		DomainId:          data.SourceDomainId,
		ChainId:           data.SourceChainId,
		Height:            data.SourceHeight,
		Actor:             relayer,
		EventType:         eventType,
		Evidence:          evidence,
		RecommendedAction: action,
	}
}
