package crossref

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	fixture "github.com/crossref/crossrefd/testutil/historyproof"
	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func TestHistoryAncestryIBCRejectsNewRoundRewrite(t *testing.T) {
	c := fixture.Config("b")
	previous := fixture.Round(c, 1, 9, types.HistoryRound{})
	next := fixture.Round(c, 2, 19, previous)
	for _, kind := range []string{"valid", "missing", "forked_anchor", "app_hash", "skipped_height", "wrong_channel"} {
		t.Run(kind, func(t *testing.T) {
			f := initIBCFixture(t)
			f.ctx = f.ctx.WithBlockHeight(30)
			s := types.HistoryState{Config: c, Previous: previous, Current: types.HistoryRound{Number: 2, Status: types.HistoryCollecting, Parts: []types.HistoryPart{next.Parts[1]}}}
			require.NoError(t, f.keeper.HistoryState.Set(f.ctx, s))
			require.NoError(t, f.keeper.SetDomainChannel(f.ctx, types.DomainChannel{LocalDomainId: "b", RemoteDomainId: "a", PortId: "crossref", ChannelId: "channel-1", CounterpartyPortId: "crossref", CounterpartyChannelId: "channel-0"}))
			p := proto.Clone(&next.Parts[0]).(*types.HistoryPart)
			switch kind {
			case "missing":
				p.AncestryProof = nil
			case "forked_anchor":
				h := fixture.Headers("a", 20)
				h[8].DataHash = bytes.Repeat([]byte{7}, 32)
				for i := 9; i < len(h); i++ {
					h[i].LastBlockID.Hash = h[i-1].Hash()
				}
				v := fixture.Part("a", h, 9, 19)
				p = &v
			case "app_hash":
				p.AppHash[0] ^= 1
			case "skipped_height":
				p.Height++
			}
			wire := types.CrossrefPacketData{Packet: &types.CrossrefPacketData_History{History: &types.HistoryPacketData{SessionHash: types.HistorySessionHash(c), Round: 2, SourceDomainId: "a", Part: p}}}
			data, err := wire.Marshal()
			require.NoError(t, err)
			packet := channeltypes.Packet{Sequence: 1, SourcePort: "crossref", SourceChannel: "channel-0", DestinationPort: "crossref", DestinationChannel: "channel-1", Data: data}
			if kind == "wrong_channel" {
				packet.SourceChannel = "channel-99"
			}
			ack := f.ibcModule.OnRecvPacket(f.ctx, types.Version, packet, sdk.AccAddress(bytes.Repeat([]byte{7}, 20)))
			require.Equal(t, kind == "valid", ack.Success())
			after, err := f.keeper.GetHistory(f.ctx)
			require.NoError(t, err)
			if kind != "valid" {
				require.True(t, proto.Equal(&s, &after))
			} else {
				require.Len(t, after.Current.Parts, 2)
			}
		})
	}
}
