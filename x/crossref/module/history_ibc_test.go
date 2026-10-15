package crossref

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func TestHistoryIBCDispatchAndRetryCallbacks(t *testing.T) {
	f := initIBCFixture(t)
	f.ctx = f.ctx.WithBlockHeight(20)
	c := types.HistoryConfig{Enabled: true, SessionId: "ibc-module", LocalDomainId: "b", IntervalBlocks: 3, PacketTimeoutSeconds: 60}
	for _, id := range []string{"a", "b", "c"} {
		seed := sha256.Sum256([]byte(id))
		key := ed25519.NewKeyFromSeed(seed[:])
		c.Members = append(c.Members, types.HistoryMember{DomainId: id, ChainId: id, PublicKey: key.Public().(ed25519.PublicKey)})
	}
	require.NoError(t, f.keeper.HistoryState.Set(f.ctx, types.HistoryState{Config: c}))
	require.NoError(t, f.keeper.SetDomainChannel(f.ctx, types.DomainChannel{LocalDomainId: "b", RemoteDomainId: "a", PortId: "crossref", ChannelId: "channel-1", CounterpartyPortId: "crossref", CounterpartyChannelId: "channel-0"}))
	hash := bytes.Repeat([]byte{1}, 32)
	p := &types.HistoryPacketData{SessionHash: types.HistorySessionHash(c), Round: 1, SourceDomainId: "a", Part: &types.HistoryPart{DomainId: "a", ChainId: "a", Height: 9, BlockHash: hash, AppHash: hash}}
	data := types.CrossrefPacketData{Packet: &types.CrossrefPacketData_History{History: p}}
	wire, err := data.Marshal()
	require.NoError(t, err)
	packet := channeltypes.Packet{Sequence: 9, SourcePort: "crossref", SourceChannel: "channel-0", DestinationPort: "crossref", DestinationChannel: "channel-1", Data: wire}
	relayer := sdk.AccAddress(bytes.Repeat([]byte{7}, 20))
	result := f.ibcModule.OnRecvPacket(f.ctx, types.Version, packet, relayer)
	require.True(t, result.Success())
	s, err := f.keeper.GetHistory(f.ctx)
	require.NoError(t, err)
	require.Len(t, s.PendingParts, 1)
	require.Zero(t, s.Current.Number)
	forged := packet
	forged.SourceChannel = "channel-99"
	require.False(t, f.ibcModule.OnRecvPacket(f.ctx, types.Version, forged, relayer).Success())
	packet.SourcePort = "crossref"
	packet.SourceChannel = "channel-1"
	s.Outbox = []types.HistoryOutbox{{Destination: "a", Packet: *p, Sequence: 9, PortId: "crossref", ChannelId: "channel-1"}}
	require.NoError(t, f.keeper.HistoryState.Set(f.ctx, s))
	require.NoError(t, f.ibcModule.OnTimeoutPacket(f.ctx, types.Version, packet, relayer))
	s, err = f.keeper.GetHistory(f.ctx)
	require.NoError(t, err)
	require.Zero(t, s.Outbox[0].Sequence)
	require.Equal(t, uint64(25), s.Outbox[0].RetryHeight)
	s.Outbox[0].Sequence = 9
	require.NoError(t, f.keeper.HistoryState.Set(f.ctx, s))
	ack := channeltypes.NewResultAcknowledgement([]byte("history-v2-buffered-or-stored"))
	ackBytes := f.ibcModule.cdc.MustMarshalJSON(&ack)
	require.NoError(t, f.ibcModule.OnAcknowledgementPacket(f.ctx, types.Version, packet, ackBytes, relayer))
	s, err = f.keeper.GetHistory(f.ctx)
	require.NoError(t, err)
	require.Empty(t, s.Outbox)
}
