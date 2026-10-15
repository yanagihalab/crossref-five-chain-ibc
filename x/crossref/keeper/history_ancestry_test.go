package keeper_test

import (
	"crypto/ed25519"
	"fmt"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	fixtureproof "github.com/crossref/crossrefd/testutil/historyproof"
	"github.com/crossref/crossrefd/x/crossref/keeper"
	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func TestAttachProofGasAfterInterveningParts(t *testing.T) {
	for _, height := range []int{34, 519} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			f := initFixture(t)
			config := fixtureproof.Config("b")
			previous := fixtureproof.Round(config, 1, 9, types.HistoryRound{})
			complete := fixtureproof.Round(config, 2, height, previous)
			local := complete.Parts[1]
			proof := local.AncestryProof
			local.AncestryProof = nil
			state := types.HistoryState{Config: config, Previous: previous, Current: types.HistoryRound{
				Number: 2, OpenedHeight: uint64(height + 1), Status: types.HistoryCollecting, Parts: []types.HistoryPart{local},
			}}
			ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height + 2))
			require.NoError(t, f.keeper.HistoryState.Set(ctx, state))
			payer, err := f.addressCodec.BytesToString(f.keeper.GetAuthority())
			require.NoError(t, err)
			attach := func(limit uint64) uint64 {
				meter := storetypes.NewGasMeter(limit)
				cache, _ := ctx.WithGasMeter(meter).CacheContext()
				_, err := keeper.NewMsgServerImpl(f.keeper).AttachHistoryProof(cache, &types.MsgAttachHistoryProof{Sender: payer, Round: 2, Proof: proof})
				require.NoError(t, err)
				return meter.GasConsumed()
			}
			estimate := attach(250000000)
			for _, index := range []int{0, 2} {
				part := complete.Parts[index]
				require.NoError(t, state.ReceiveHistory(types.HistoryPacketData{SessionHash: types.HistorySessionHash(config),
					Round: 2, SourceDomainId: part.DomainId, Part: &part}, uint64(height+2)))
				require.NoError(t, f.keeper.HistoryState.Set(ctx, state))
				if index == 0 {
					require.Panics(t, func() { attach(estimate * 3 / 2) }, "the old simulation multiplier misses intervening Part costs")
				}
			}
			actual := attach(150000000)
			require.Less(t, actual, uint64(150000000))
			t.Logf("height=%d proof_bytes=%d before_receive=%d after_two_parts=%d; excludes ante/IBC overhead", height, len(proof), estimate, actual)
		})
	}
}

func TestHistoryAncestryThreeChainsThreeRounds(t *testing.T) {
	var fixtures []*fixture
	var maxReceiveGas, maxAttachGas, maxSignGas uint64
	sawLargePacket := false
	type delivery struct {
		source, dest int
		packet       channeltypes.Packet
	}
	var queue []delivery
	seq := uint64(0)
	for i, id := range []string{"a", "b", "c"} {
		f := initFixture(t)
		fixtures = append(fixtures, f)
		require.NoError(t, f.keeper.HistoryState.Set(f.ctx, types.HistoryState{Config: fixtureproof.Config(id)}))
		for _, m := range fixtureproof.Config(id).Members {
			require.NoError(t, f.keeper.SetDomain(f.ctx, types.DomainInfo{DomainId: m.DomainId, ChainId: m.ChainId, HysteresisPublicKey: m.PublicKey}))
		}
		for j, remote := range []string{"a", "b", "c"} {
			if i != j {
				require.NoError(t, f.keeper.SetDomainChannel(f.ctx, types.DomainChannel{LocalDomainId: id, RemoteDomainId: remote, PortId: "crossref", ChannelId: fmt.Sprintf("channel-%d", j), CounterpartyPortId: "crossref", CounterpartyChannelId: fmt.Sprintf("channel-%d", i)}))
			}
		}
		source := i
		f.keeper.SetHistoryPacketSender(func(_ sdk.Context, port, channel string, timeout uint64, b []byte) (uint64, error) {
			var dest int
			_, err := fmt.Sscanf(channel, "channel-%d", &dest)
			if err != nil {
				return 0, err
			}
			seq++
			queue = append(queue, delivery{source, dest, channeltypes.Packet{Sequence: seq, SourcePort: port, SourceChannel: channel, DestinationPort: "crossref", DestinationChannel: fmt.Sprintf("channel-%d", source), Data: b, TimeoutTimestamp: timeout}})
			return seq, nil
		})
	}
	drain := func() {
		for len(queue) > 0 {
			d := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			var wire types.CrossrefPacketData
			require.NoError(t, wire.Unmarshal(d.packet.Data))
			p := wire.GetHistory()
			require.NotNil(t, p)
			dst := fixtures[d.dest]
			sawLargePacket = sawLargePacket || len(d.packet.Data) > 65536
			require.LessOrEqual(t, len(d.packet.Data), types.MaxHistoryPacketBytes)
			beforeGas := sdk.UnwrapSDKContext(dst.ctx).GasMeter().GasConsumed()
			require.NoError(t, dst.keeper.ReceiveHistoryPacket(sdk.UnwrapSDKContext(dst.ctx), d.packet, p))
			maxReceiveGas = max(maxReceiveGas, sdk.UnwrapSDKContext(dst.ctx).GasMeter().GasConsumed()-beforeGas)
			require.NoError(t, dst.keeper.ReceiveHistoryPacket(sdk.UnwrapSDKContext(dst.ctx), d.packet, p))
			src := fixtures[d.source]
			require.NoError(t, src.keeper.HistoryPacketResult(sdk.UnwrapSDKContext(src.ctx), d.packet, p, true))
		}
	}
	for index, h := range []int{10, 115, 625} {
		round := index + 1
		for i, f := range fixtures {
			id := string(rune('a' + i))
			headers := fixtureproof.Headers(id, h+2)
			s, err := f.keeper.GetHistory(f.ctx)
			require.NoError(t, err)
			s.LastBlockHeight = uint64(h - 1)
			s.LastBlockHash = headers[h-2].Hash()
			require.NoError(t, f.keeper.HistoryState.Set(f.ctx, s))
			f.ctx = sdk.UnwrapSDKContext(f.ctx).WithChainID(id).WithBlockHeader(cmtproto.Header{ChainID: id, Height: int64(h), Time: time.Unix(1800000000+int64(h*6), 0), AppHash: headers[h-1].AppHash}).WithHeaderHash(headers[h-1].Hash())
			require.NoError(t, f.keeper.HistoryBeginBlock(f.ctx))
			s, err = f.keeper.GetHistory(f.ctx)
			require.NoError(t, err)
			require.Equal(t, uint64(round), s.Current.Number)
			require.Empty(t, s.Outbox)
			start := h - 1
			if previous := types.HistoryAnchor(s.Previous, id); previous != nil {
				start = int(previous.Height)
			}
			part := fixtureproof.Part(id, headers, start, h-1)
			payer, _ := f.addressCodec.BytesToString(f.keeper.GetAuthority())
			beforeGas := sdk.UnwrapSDKContext(f.ctx).GasMeter().GasConsumed()
			_, err = keeper.NewMsgServerImpl(f.keeper).AttachHistoryProof(f.ctx, &types.MsgAttachHistoryProof{Sender: payer, Round: uint64(round), Proof: part.AncestryProof})
			require.NoError(t, err)
			maxAttachGas = max(maxAttachGas, sdk.UnwrapSDKContext(f.ctx).GasMeter().GasConsumed()-beforeGas)
			require.NoError(t, f.keeper.HistoryBeginBlock(f.ctx))
		}
		require.Len(t, queue, 6)
		drain()
		for i, f := range fixtures {
			f.ctx = sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(h + 2))
			response, err := keeper.NewQueryServerImpl(f.keeper).History(f.ctx, &types.QueryHistoryRequest{})
			require.NoError(t, err)
			require.NotEmpty(t, response.SignBytes)
			payer, _ := f.addressCodec.BytesToString(f.keeper.GetAuthority())
			beforeGas := sdk.UnwrapSDKContext(f.ctx).GasMeter().GasConsumed()
			_, err = keeper.NewMsgServerImpl(f.keeper).SignHistoryRound(f.ctx, &types.MsgSignHistoryRound{Sender: payer, Round: uint64(round), Signature: ed25519.Sign(fixtureproof.Key(string(rune('a'+i))), response.SignBytes)})
			require.NoError(t, err)
			maxSignGas = max(maxSignGas, sdk.UnwrapSDKContext(f.ctx).GasMeter().GasConsumed()-beforeGas)
			require.NoError(t, f.keeper.HistoryBeginBlock(f.ctx))
		}
		require.Len(t, queue, 6)
		drain()
		for _, f := range fixtures {
			s, err := f.keeper.GetHistory(f.ctx)
			require.NoError(t, err)
			require.Equal(t, types.HistoryCompleted, s.Current.Status)
			require.Empty(t, s.Outbox)
			require.NoError(t, f.keeper.Port.Set(f.ctx, types.PortID))
			gen, err := f.keeper.ExportGenesis(f.ctx)
			require.NoError(t, err)
			require.NoError(t, gen.Validate())
			restored := initFixture(t)
			require.NoError(t, restored.keeper.InitGenesis(restored.ctx, *gen))
		}
	}
	require.True(t, sawLargePacket)
	for _, gas := range []uint64{maxReceiveGas, maxAttachGas, maxSignGas} {
		require.Less(t, gas, uint64(150000000))
	}
	t.Logf("maximum keeper gas: receive=%d attach=%d sign=%d; excludes IBC and ante overhead", maxReceiveGas, maxAttachGas, maxSignGas)
}
