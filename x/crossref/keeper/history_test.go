package keeper_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	"github.com/crossref/crossrefd/x/crossref/keeper"
	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func TestHistoryAutomaticMultiChain(t *testing.T) {
	for _, n := range []int{3, 5, 6} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			fixtures := make([]*fixture, n)
			keys := make([]ed25519.PrivateKey, n)
			members := make([]types.HistoryMember, n)
			for i := 0; i < n; i++ {
				seed := sha256.Sum256([]byte(fmt.Sprint("history-test-", i)))
				keys[i] = ed25519.NewKeyFromSeed(seed[:])
				members[i] = types.HistoryMember{DomainId: fmt.Sprintf("chain-%c", 'a'+i), ChainId: fmt.Sprintf("test-%d", i), PublicKey: keys[i].Public().(ed25519.PublicKey)}
			}
			type delivery struct {
				source, dest int
				packet       channeltypes.Packet
			}
			var queue []delivery
			sequence := uint64(0)
			setHeight := func(f *fixture, i int, h int64) {
				s, _ := f.keeper.GetHistory(f.ctx)
				bz, _ := s.Marshal()
				post := sha256.Sum256(bz)
				block := sha256.Sum256(append(bz, byte(h-1)))
				f.ctx = sdk.UnwrapSDKContext(f.ctx).WithChainID(members[i].ChainId).WithHeaderHash(block[:]).WithBlockHeader(cmtproto.Header{ChainID: members[i].ChainId, Height: h, Time: time.Unix(1800000000+h*6, 0), AppHash: post[:]})
			}
			for i := 0; i < n; i++ {
				f := initFixture(t)
				fixtures[i] = f
				setHeight(f, i, 2)
				for _, m := range members {
					require.NoError(t, f.keeper.SetDomain(f.ctx, types.DomainInfo{DomainId: m.DomainId, ChainId: m.ChainId, HysteresisPublicKey: m.PublicKey}))
				}
				for j := 0; j < n; j++ {
					if i == j {
						continue
					}
					require.NoError(t, f.keeper.SetDomainChannel(f.ctx, types.DomainChannel{LocalDomainId: members[i].DomainId, RemoteDomainId: members[j].DomainId, PortId: "crossref", ChannelId: fmt.Sprintf("channel-%d", j), CounterpartyPortId: "crossref", CounterpartyChannelId: fmt.Sprintf("channel-%d", i)}))
				}
				source := i
				f.keeper.SetHistoryPacketSender(func(_ sdk.Context, port, channel string, timeout uint64, bz []byte) (uint64, error) {
					var dest int
					_, err := fmt.Sscanf(channel, "channel-%d", &dest)
					if err != nil {
						return 0, err
					}
					sequence++
					queue = append(queue, delivery{source, dest, channeltypes.Packet{Sequence: sequence, SourcePort: port, SourceChannel: channel, DestinationPort: "crossref", DestinationChannel: fmt.Sprintf("channel-%d", source), Data: append([]byte(nil), bz...), TimeoutTimestamp: timeout}})
					return sequence, nil
				})
				authority, err := f.addressCodec.BytesToString(f.keeper.GetAuthority())
				require.NoError(t, err)
				_, err = keeper.NewMsgServerImpl(f.keeper).ConfigureHistory(f.ctx, &types.MsgConfigureHistory{Authority: authority, Config: types.HistoryConfig{Enabled: true, SessionId: "integration-v2", LocalDomainId: members[i].DomainId, Members: members, IntervalBlocks: 3, PacketTimeoutSeconds: 60}})
				require.NoError(t, err)
			}
			var observations []map[string]any
			for i, f := range fixtures {
				setHeight(f, i, 9)
				require.NoError(t, f.keeper.HistoryBeginBlock(f.ctx))
			}
			for round := uint64(1); round <= 3; round++ {
				h := int64(10 + (round-1)*5)
				for i, f := range fixtures {
					setHeight(f, i, h)
					require.NoError(t, f.keeper.HistoryBeginBlock(f.ctx))
					s, err := f.keeper.GetHistory(f.ctx)
					require.NoError(t, err)
					require.Equal(t, round, s.Current.Number)
					require.Equal(t, uint64(h-1), s.Current.Parts[0].Height)
					if round > 1 {
						require.Greater(t, s.Current.Parts[0].Height, s.Previous.CompletedHeight)
					}
				}
				// Reverse delivery order and duplicate every packet to exercise idempotency.
				drain := func() {
					for len(queue) > 0 {
						d := queue[len(queue)-1]
						queue = queue[:len(queue)-1]
						var wire types.CrossrefPacketData
						require.NoError(t, wire.Unmarshal(d.packet.Data))
						p := wire.GetHistory()
						dest := fixtures[d.dest]
						require.NoError(t, dest.keeper.ReceiveHistoryPacket(sdk.UnwrapSDKContext(dest.ctx), d.packet, p))
						require.NoError(t, dest.keeper.ReceiveHistoryPacket(sdk.UnwrapSDKContext(dest.ctx), d.packet, p))
						require.NoError(t, fixtures[d.source].keeper.HistoryPacketResult(sdk.UnwrapSDKContext(fixtures[d.source].ctx), d.packet, p, true))
					}
				}
				drain()
				for i, f := range fixtures {
					setHeight(f, i, h+1)
					response, err := keeper.NewQueryServerImpl(f.keeper).History(f.ctx, &types.QueryHistoryRequest{})
					require.NoError(t, err)
					require.Equal(t, types.HistorySigning, response.State.Current.Status)
					require.Len(t, response.State.Current.Parts, n)
					require.NotEmpty(t, response.SignBytes)
					if round > 1 {
						prior := response.State.Previous.Seals[i].Signature
						hash := sha256.Sum256(prior)
						require.Equal(t, hash[:], types.HistoryPreviousSignatureHash(response.State.Previous, members[i].DomainId))
						require.Contains(t, string(response.SignBytes), string(hash[:]))
					}
					sender, _ := f.addressCodec.BytesToString(f.keeper.GetAuthority())
					_, err = keeper.NewMsgServerImpl(f.keeper).SignHistoryRound(f.ctx, &types.MsgSignHistoryRound{Sender: sender, Round: round, Signature: ed25519.Sign(keys[i], response.SignBytes)})
					require.NoError(t, err)
				}
				for i, f := range fixtures {
					setHeight(f, i, h+2)
					require.NoError(t, f.keeper.HistoryBeginBlock(f.ctx))
				}
				drain()
				for i, f := range fixtures {
					s, err := f.keeper.GetHistory(f.ctx)
					require.NoError(t, err)
					require.Equal(t, types.HistoryCompleted, s.Current.Status)
					require.Len(t, s.Current.Seals, n)
					require.Empty(t, s.Outbox)
					for _, seal := range s.Current.Seals {
						b, err := types.HistorySignBytes(s.Config, s.Current, s.Previous, seal.DomainId)
						require.NoError(t, err)
						var pub []byte
						for _, m := range members {
							if m.DomainId == seal.DomainId {
								pub = m.PublicKey
							}
						}
						require.True(t, ed25519.Verify(pub, b, seal.Signature))
					}
					observations = append(observations, map[string]any{"domain": members[i].DomainId, "round": round, "sourceHeight": h - 1, "completedHeight": s.Current.CompletedHeight, "parts": len(s.Current.Parts), "seals": len(s.Current.Seals), "previousSignatureChained": round > 1, "status": s.Current.Status})
					// Export/import round-trip preserves the previous signature and in-flight protocol state.
					require.NoError(t, f.keeper.Port.Set(f.ctx, types.PortID))
					gen, err := f.keeper.ExportGenesis(f.ctx)
					require.NoError(t, err)
					restored := initFixture(t)
					require.NoError(t, restored.keeper.InitGenesis(restored.ctx, *gen))
					again, err := restored.keeper.GetHistory(restored.ctx)
					require.NoError(t, err)
					require.True(t, proto.Equal(&s, &again))
					stored, err := restored.keeper.HistoryRounds.Get(restored.ctx, round)
					require.NoError(t, err)
					require.True(t, proto.Equal(&stored, &s.Current))
				}
				for step := h + 3; step < h+5; step++ {
					for i, f := range fixtures {
						setHeight(f, i, step)
						require.NoError(t, f.keeper.HistoryBeginBlock(f.ctx))
					}
				}
			}
			if out := os.Getenv("HISTORY_VERIFICATION_OUTPUT"); out != "" {
				require.NoError(t, os.MkdirAll(out, 0700))
				bz, err := json.MarshalIndent(map[string]any{"kind": "sdk-keeper-history-v2-test", "chains": n, "rounds": 3, "ok": true, "realIBCProofs": false, "scope": "SDK keeper / Msg / query / injected IBC transport; no live validators or real light-client proofs", "observations": observations}, "", "  ")
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(out, fmt.Sprintf("history-%dc.json", n)), bz, 0600))
			}
		})
	}
}

func TestHistoryRejectsBadSignaturesAndIncompleteRounds(t *testing.T) {
	config := types.HistoryConfig{Enabled: true, SessionId: "test", LocalDomainId: "a", IntervalBlocks: 3, PacketTimeoutSeconds: 60}
	keys := map[string]ed25519.PrivateKey{}
	for _, id := range []string{"a", "b", "c"} {
		seed := sha256.Sum256([]byte(id))
		keys[id] = ed25519.NewKeyFromSeed(seed[:])
		config.Members = append(config.Members, types.HistoryMember{DomainId: id, ChainId: id, PublicKey: keys[id].Public().(ed25519.PublicKey)})
	}
	s := types.HistoryState{Config: config}
	hash := bytes.Repeat([]byte{1}, 32)
	opened, err := s.BeginHistory(10, hash, hash)
	require.NoError(t, err)
	require.True(t, opened)
	_, err = types.HistorySignBytes(config, s.Current, s.Previous, "a")
	require.Error(t, err)
	require.Error(t, s.SealHistory(1, 11, make([]byte, 64)))
	for _, id := range []string{"b", "c"} {
		p := types.HistoryPacketData{SessionHash: types.HistorySessionHash(config), Round: 1, SourceDomainId: id, Part: &types.HistoryPart{DomainId: id, ChainId: id, Height: 9, BlockHash: hash, AppHash: hash}}
		wrong := proto.Clone(&p).(*types.HistoryPacketData)
		wrong.SessionHash = []byte("wrong")
		require.Error(t, s.ReceiveHistory(*wrong, 10))
		require.NoError(t, s.ReceiveHistory(p, 10))
		wrong = proto.Clone(&p).(*types.HistoryPacketData)
		wrong.Part.BlockHash = bytes.Repeat([]byte{2}, 32)
		require.Error(t, s.ReceiveHistory(*wrong, 10))
	}
	b, err := types.HistorySignBytes(config, s.Current, s.Previous, "a")
	require.NoError(t, err)
	wrong := proto.Clone(&s).(*types.HistoryState)
	wrong.Current.Parts[2].BlockHash = bytes.Repeat([]byte{3}, 32)
	require.Error(t, wrong.SealHistory(1, 11, ed25519.Sign(keys["a"], b)))
	require.NoError(t, s.SealHistory(1, 11, ed25519.Sign(keys["a"], b)))
	require.Error(t, s.SealHistory(1, 11, ed25519.Sign(keys["a"], b)))
	require.NotEqual(t, types.HistoryCompleted, s.Current.Status)
	opened, err = s.BeginHistory(100, hash, hash)
	require.NoError(t, err)
	require.False(t, opened)
}

func TestHistoryOutboxRetryAndDisabledDefault(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.HistoryBeginBlock(f.ctx))
	s, err := f.keeper.GetHistory(f.ctx)
	require.NoError(t, err)
	require.Zero(t, s.Current.Number)
	packet := types.HistoryPacketData{Round: 1, SourceDomainId: "a", SessionHash: []byte("test")}
	s.Outbox = []types.HistoryOutbox{{Sequence: 7, PortId: "crossref", ChannelId: "channel-0", Packet: packet}}
	require.NoError(t, f.keeper.HistoryState.Set(f.ctx, s))
	ctx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(50)
	wire := channeltypes.Packet{Sequence: 7, SourcePort: "crossref", SourceChannel: "channel-0"}
	require.NoError(t, f.keeper.HistoryPacketResult(ctx, wire, &packet, false))
	s, err = f.keeper.GetHistory(ctx)
	require.NoError(t, err)
	require.Zero(t, s.Outbox[0].Sequence)
	require.Equal(t, uint64(55), s.Outbox[0].RetryHeight)
	require.NoError(t, f.keeper.HistoryPacketResult(ctx, wire, &packet, true))
	s, err = f.keeper.GetHistory(ctx)
	require.NoError(t, err)
	require.Len(t, s.Outbox, 1)
}
