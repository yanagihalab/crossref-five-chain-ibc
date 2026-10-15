package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"cosmossdk.io/collections"
	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	clienttypes "github.com/cosmos/ibc-go/v11/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	"github.com/crossref/crossrefd/x/crossref/types"
)

func (k Keeper) GetHistory(ctx context.Context) (types.HistoryState, error) {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "state_read")
	s, err := k.HistoryState.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return types.HistoryState{}, nil
	}
	return s, err
}
func (k Keeper) saveHistory(ctx context.Context, s types.HistoryState) error {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "state_write")
	if s.Current.Number > 0 {
		if err := k.HistoryRounds.Set(ctx, s.Current.Number, s.Current); err != nil {
			return err
		}
	}
	return k.HistoryState.Set(ctx, s)
}
func (k msgServer) ConfigureHistory(ctx context.Context, req *types.MsgConfigureHistory) (*types.MsgConfigureHistoryResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("missing history config")
	}
	if err := k.requireAuthority(req.Authority); err != nil {
		return nil, err
	}
	if err := types.ValidateHistoryConfig(req.Config); err != nil {
		return nil, err
	}
	s, err := k.GetHistory(ctx)
	if err != nil {
		return nil, err
	}
	if s.Config.SessionId != "" && (!bytes.Equal(types.HistorySessionHash(s.Config), types.HistorySessionHash(req.Config)) || s.Config.LocalDomainId != req.Config.LocalDomainId) {
		return nil, fmt.Errorf("history session membership is immutable; use an explicit upgrade for replacement")
	}
	for _, m := range req.Config.Members {
		d, found, err := k.GetDomain(ctx, m.DomainId)
		if err != nil {
			return nil, err
		}
		if !found || d.ChainId != m.ChainId || !bytes.Equal(d.HysteresisPublicKey, m.PublicKey) {
			return nil, fmt.Errorf("history member differs from registered domain")
		}
		if m.DomainId == req.Config.LocalDomainId && m.ChainId != sdk.UnwrapSDKContext(ctx).ChainID() {
			return nil, fmt.Errorf("local history chain mismatch")
		}
	}
	s.Config = req.Config
	return &types.MsgConfigureHistoryResponse{}, k.saveHistory(ctx, s)
}
func (k msgServer) SignHistoryRound(ctx context.Context, req *types.MsgSignHistoryRound) (*types.MsgSignHistoryRoundResponse, error) {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "sign_tx")
	if req == nil {
		return nil, fmt.Errorf("missing history signature")
	}
	if _, err := k.addressCodec.StringToBytes(req.Sender); err != nil {
		return nil, err
	}
	s, err := k.GetHistory(ctx)
	if err != nil {
		return nil, err
	}
	chargeHistoryProofs(sdk.UnwrapSDKContext(ctx), s, 0)
	if err := s.SealHistory(req.Round, uint64(sdk.UnwrapSDKContext(ctx).BlockHeight()), req.Signature); err != nil {
		return nil, err
	}
	if err := k.saveHistory(ctx, s); err != nil {
		return nil, err
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent("history_signed", sdk.NewAttribute("round", fmt.Sprint(req.Round))))
	return &types.MsgSignHistoryRoundResponse{}, nil
}
func chargeHistoryProofs(ctx sdk.Context, s types.HistoryState, extra int) {
	if !s.Config.RequireAncestryProof {
		return
	}
	n := uint64(extra)
	for _, p := range s.Current.Parts {
		n += uint64(len(p.AncestryProof))
	}
	ctx.GasMeter().ConsumeGas(n*uint64(len(s.Config.Members)+2)*20, "history ancestry-v4 verification")
}
func (k msgServer) AttachHistoryProof(ctx context.Context, req *types.MsgAttachHistoryProof) (*types.MsgAttachHistoryProofResponse, error) {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "attach_proof_tx")
	if req == nil || len(req.Proof) > types.MaxHistoryProofBytes {
		return nil, fmt.Errorf("invalid history proof request")
	}
	if _, err := k.addressCodec.StringToBytes(req.Sender); err != nil {
		return nil, err
	}
	s, err := k.GetHistory(ctx)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	chargeHistoryProofs(sdkCtx, s, len(req.Proof))
	if err = s.AttachHistoryProof(req.Round, req.Proof, uint64(sdkCtx.BlockHeight())); err != nil {
		return nil, err
	}
	if err = k.saveHistory(ctx, s); err != nil {
		return nil, err
	}
	return &types.MsgAttachHistoryProofResponse{}, nil
}
func (k queryServer) History(ctx context.Context, req *types.QueryHistoryRequest) (*types.QueryHistoryResponse, error) {
	s, err := k.k.GetHistory(ctx)
	if err != nil {
		return nil, err
	}
	var b []byte
	if s.Config.Enabled && s.Current.Status == types.HistorySigning {
		signed := false
		for _, seal := range s.Current.Seals {
			signed = signed || seal.DomainId == s.Config.LocalDomainId
		}
		if !signed {
			b, err = types.HistorySignBytes(s.Config, s.Current, s.Previous, s.Config.LocalDomainId)
			if err != nil {
				return nil, err
			}
		}
	}
	return &types.QueryHistoryResponse{State: s, SignBytes: b}, nil
}
func (k queryServer) HistoryRound(ctx context.Context, req *types.QueryHistoryRoundRequest) (*types.QueryHistoryRoundResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("missing round")
	}
	r, err := k.k.HistoryRounds.Get(ctx, req.Round)
	if err != nil {
		return nil, err
	}
	return &types.QueryHistoryRoundResponse{Round: r}, nil
}
func (k *Keeper) SetHistoryPacketSender(sender func(sdk.Context, string, string, uint64, []byte) (uint64, error)) {
	k.historySender = sender
}
func (k Keeper) historyChannel(ctx sdk.Context, local, remote string) (types.DomainChannel, error) {
	var found []types.DomainChannel
	err := k.DomainChannels.Walk(ctx, nil, func(_ string, b types.DomainChannel) (bool, error) {
		if b.LocalDomainId == local && b.RemoteDomainId == remote {
			found = append(found, b)
		}
		return false, nil
	})
	if err != nil {
		return types.DomainChannel{}, err
	}
	if len(found) != 1 {
		return types.DomainChannel{}, fmt.Errorf("history needs exactly one bound channel per peer")
	}
	return found[0], nil
}

// The BeginBlock hook does not hold signing keys. It opens rounds and drains the
// persisted IBC outbox; a permissionless fee payer submits a participant signature.
func (k Keeper) HistoryBeginBlock(ctx context.Context) error {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "begin_block")
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cache, write := sdkCtx.CacheContext()
	s, err := k.GetHistory(cache)
	if err != nil {
		return err
	}
	if !s.Config.Enabled {
		return nil
	}
	for _, member := range s.Config.Members {
		if member.DomainId == s.Config.LocalDomainId && member.ChainId != cache.ChainID() {
			return fmt.Errorf("configured history chain differs from execution chain")
		}
	}
	header := cache.BlockHeader()
	// ABCI++ does not populate LastBlockId in SDK BlockHeader. Persist the
	// canonical RequestFinalizeBlock hash and use it only in the following block.
	var parentHash []byte
	if s.LastBlockHeight+1 == uint64(cache.BlockHeight()) {
		parentHash = s.LastBlockHash
	}
	before := proto.Clone(&s).(*types.HistoryState)
	opened, err := s.BeginHistory(uint64(cache.BlockHeight()), parentHash, header.AppHash)
	if err != nil {
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent("history_blocked", sdk.NewAttribute("reason", err.Error())))
		s = *before
	}
	s.LastBlockHeight = uint64(cache.BlockHeight())
	s.LastBlockHash = cache.HeaderHash()
	for i := range s.Outbox {
		o := &s.Outbox[i]
		if o.Sequence != 0 || uint64(cache.BlockHeight()) < o.RetryHeight {
			continue
		}
		if o.Packet.Part != nil {
			if err := types.VerifyHistoryPartProof(s.Config, o.Packet.Round, *o.Packet.Part, s.Previous); err != nil {
				return err
			}
		}
		binding, sendErr := k.historyChannel(cache, s.Config.LocalDomainId, o.Destination)
		var seq uint64
		if sendErr == nil {
			wire := types.CrossrefPacketData{Packet: &types.CrossrefPacketData_History{History: &o.Packet}}
			bz, e := wire.Marshal()
			if e != nil {
				return e
			}
			if len(bz) > types.MaxHistoryPacketBytes {
				return fmt.Errorf("invalid history packet size")
			}
			sendCtx, commit := cache.CacheContext()
			timeout := uint64(cache.BlockTime().Add(time.Duration(s.Config.PacketTimeoutSeconds) * time.Second).UnixNano())
			sendStart := telemetry.Now()
			if k.historySender != nil {
				seq, sendErr = k.historySender(sendCtx, binding.PortId, binding.ChannelId, timeout, bz)
			} else if k.ibcKeeperFn != nil && k.ibcKeeperFn() != nil {
				seq, sendErr = k.ibcKeeperFn().ChannelKeeper.SendPacket(sendCtx, binding.PortId, binding.ChannelId, clienttypes.ZeroHeight(), timeout, bz)
			} else {
				sendErr = fmt.Errorf("IBC keeper unavailable")
			}
			telemetry.MeasureSince(sendStart, "crossref", "history", "ibc_send_packet")
			if sendErr == nil {
				commit()
				o.Sequence = seq
				o.PortId = binding.PortId
				o.ChannelId = binding.ChannelId
			}
		}
		o.Attempts++
		o.RetryHeight = uint64(cache.BlockHeight()) + 5
		if sendErr != nil {
			cache.EventManager().EmitEvent(sdk.NewEvent("history_send_retry", sdk.NewAttribute("destination", o.Destination)))
		}
	}
	if err := k.saveHistory(cache, s); err != nil {
		return err
	}
	if opened {
		cache.EventManager().EmitEvent(sdk.NewEvent("history_opened", sdk.NewAttribute("round", fmt.Sprint(s.Current.Number)), sdk.NewAttribute("source_height", fmt.Sprint(cache.BlockHeight()-1))))
	}
	write()
	return nil
}
func (k Keeper) ReceiveHistoryPacket(ctx sdk.Context, packet channeltypes.Packet, p *types.HistoryPacketData) error {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "receive_packet")
	if p == nil || len(packet.Data) > types.MaxHistoryPacketBytes {
		return fmt.Errorf("invalid history packet size")
	}
	s, err := k.GetHistory(ctx)
	if err != nil {
		return err
	}
	if s.Config.SessionId == "" {
		return fmt.Errorf("history unconfigured")
	}
	b, found, err := k.GetDomainChannelByChannel(ctx, packet.DestinationPort, packet.DestinationChannel)
	if err != nil {
		return err
	}
	if !found || b.LocalDomainId != s.Config.LocalDomainId || b.RemoteDomainId != p.SourceDomainId || b.CounterpartyPortId != packet.SourcePort || b.CounterpartyChannelId != packet.SourceChannel {
		return fmt.Errorf("history channel/source mismatch")
	}
	extra := 0
	if p.Part != nil {
		extra = len(p.Part.AncestryProof)
	}
	chargeHistoryProofs(ctx, s, extra)
	if err := s.ReceiveHistory(*p, uint64(ctx.BlockHeight())); err != nil {
		return err
	}
	if err := k.saveHistory(ctx, s); err != nil {
		return err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("history_received", sdk.NewAttribute("round", fmt.Sprint(p.Round)), sdk.NewAttribute("source", p.SourceDomainId), sdk.NewAttribute("status", s.Current.Status)))
	return nil
}
func (k Keeper) HistoryPacketResult(ctx sdk.Context, packet channeltypes.Packet, p *types.HistoryPacketData, success bool) error {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "packet_result")
	s, err := k.GetHistory(ctx)
	if err != nil {
		return err
	}
	for i, o := range s.Outbox {
		if o.Sequence != packet.Sequence || o.PortId != packet.SourcePort || o.ChannelId != packet.SourceChannel || !proto.Equal(&o.Packet, p) {
			continue
		}
		if success {
			s.Outbox = append(s.Outbox[:i], s.Outbox[i+1:]...)
		} else {
			s.Outbox[i].Sequence = 0
			s.Outbox[i].RetryHeight = uint64(ctx.BlockHeight()) + 5
		}
		return k.saveHistory(ctx, s)
	}
	return nil
}
