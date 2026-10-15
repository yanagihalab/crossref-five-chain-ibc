package crossref

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

// Calls the production IBC application handler with an authenticated-channel
// fixture. This does not exercise network delivery or IBC core Merkle proofs.
// This retains the legacy v2 baseline; v3 rejection is in history_ancestry_test.go.
func TestHistoryTamperRejectionAudit(t *testing.T) {
	c := types.HistoryConfig{Enabled: true, SessionId: "isolated-tamper-audit", LocalDomainId: "b", IntervalBlocks: 3, PacketTimeoutSeconds: 60}
	keys := map[string]ed25519.PrivateKey{}
	for _, id := range []string{"a", "b", "c"} {
		seed := sha256.Sum256([]byte("isolated-audit-" + id))
		keys[id] = ed25519.NewKeyFromSeed(seed[:])
		c.Members = append(c.Members, types.HistoryMember{DomainId: id, ChainId: id, PublicKey: keys[id].Public().(ed25519.PublicKey)})
	}
	s := types.HistoryState{Config: c}
	hash := bytes.Repeat([]byte{1}, 32)
	_, err := s.BeginHistory(10, hash, hash)
	require.NoError(t, err)
	part := types.HistoryPacketData{SessionHash: types.HistorySessionHash(c), Round: 1, SourceDomainId: "a", Part: &types.HistoryPart{DomainId: "a", ChainId: "a", Height: 9, BlockHash: hash, AppHash: hash}}
	require.NoError(t, s.ReceiveHistory(part, 10))
	other := proto.Clone(&part).(*types.HistoryPacketData)
	other.SourceDomainId, other.Part.DomainId, other.Part.ChainId = "c", "c", "c"
	require.NoError(t, s.ReceiveHistory(*other, 10))
	unsigned := proto.Clone(&s).(*types.HistoryState)
	for _, id := range []string{"a", "b", "c"} {
		bz, e := types.HistorySignBytes(c, s.Current, s.Previous, id)
		require.NoError(t, e)
		sig := ed25519.Sign(keys[id], bz)
		if id == "b" {
			require.NoError(t, s.SealHistory(1, 11, sig))
			continue
		}
		require.NoError(t, s.ReceiveHistory(types.HistoryPacketData{SessionHash: types.HistorySessionHash(c), Round: 1, SourceDomainId: id,
			Seal: &types.HistorySeal{DomainId: id, Signature: sig, PreviousSignatureHash: types.HistoryPreviousSignatureHash(s.Previous, id), RecordedHeight: 11}}, 11))
	}
	require.Equal(t, types.HistoryCompleted, s.Current.Status)
	type result struct {
		Name           string  `json:"name"`
		SuccessACK     bool    `json:"successAck"`
		StateUnchanged bool    `json:"stateUnchanged"`
		DurationMS     float64 `json:"durationMs"`
	}
	var rows []result
	run := func(name string, state types.HistoryState, p *types.HistoryPacketData, success, unchanged bool) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			f := initIBCFixture(t)
			f.ctx = f.ctx.WithBlockHeight(40)
			require.NoError(t, f.keeper.HistoryState.Set(f.ctx, state))
			require.NoError(t, f.keeper.SetDomainChannel(f.ctx, types.DomainChannel{LocalDomainId: "b", RemoteDomainId: "a", PortId: "crossref", ChannelId: "channel-1", CounterpartyPortId: "crossref", CounterpartyChannelId: "channel-0"}))
			before, e := f.keeper.GetHistory(f.ctx)
			require.NoError(t, e)
			wire := types.CrossrefPacketData{Packet: &types.CrossrefPacketData_History{History: p}}
			data, e := wire.Marshal()
			require.NoError(t, e)
			packet := channeltypes.Packet{Sequence: 77, SourcePort: "crossref", SourceChannel: "channel-0", DestinationPort: "crossref", DestinationChannel: "channel-1", Data: data}
			start := time.Now()
			ack := f.ibcModule.OnRecvPacket(f.ctx, types.Version, packet, sdk.AccAddress(bytes.Repeat([]byte{7}, 20)))
			elapsed := float64(time.Since(start).Nanoseconds()) / 1e6
			after, e := f.keeper.GetHistory(f.ctx)
			require.NoError(t, e)
			require.Equal(t, success, ack.Success())
			require.Equal(t, unchanged, proto.Equal(&before, &after))
			rows = append(rows, result{name, ack.Success(), proto.Equal(&before, &after), elapsed})
		})
	}
	run("original_duplicate", s, &part, true, true)
	for _, field := range []string{"block_hash", "app_hash", "height", "chain_id"} {
		p := proto.Clone(&part).(*types.HistoryPacketData)
		switch field {
		case "block_hash":
			p.Part.BlockHash[0] ^= 1
		case "app_hash":
			p.Part.AppHash[0] ^= 1
		case "height":
			p.Part.Height++
		case "chain_id":
			p.Part.ChainId = "forged"
		}
		run("completed_"+field, s, p, false, true)
	}
	p := proto.Clone(&part).(*types.HistoryPacketData)
	p.Part = nil
	seal := s.Current.Seals[0]
	p.Seal = proto.Clone(&seal).(*types.HistorySeal)
	p.Seal.Signature[0] ^= 1
	run("invalid_aggregate_signature", *unsigned, p, false, true)
	p = proto.Clone(&part).(*types.HistoryPacketData)
	p.SessionHash[0] ^= 1
	run("wrong_session", s, p, false, true)
	next := proto.Clone(&s).(*types.HistoryState)
	next.Outbox = nil
	opened, err := next.BeginHistory(30, hash, hash)
	require.NoError(t, err)
	require.True(t, opened)
	p = proto.Clone(&part).(*types.HistoryPacketData)
	p.Part.BlockHash[0] ^= 1
	run("previous_round_rewrite", *next, p, false, true)
	run("previous_round_original_duplicate", *next, &part, true, true)
	// A fresh, well-shaped authenticated claim is buffered, not independently
	// checked against the source block store. Success ACK is not round approval.
	fresh := types.HistoryState{Config: c}
	run("unanchored_first_claim_is_buffered", fresh, p, true, false)
	p.Round = 2
	p.Part.Height = 29
	run("new_round_unproven_ancestry_is_stored", *next, p, true, false)
	// All participant signatures authenticate the new claims, but do not add an
	// ancestry proof. Document this boundary instead of counting it as rejection.
	unproven := proto.Clone(next).(*types.HistoryState)
	require.NoError(t, unproven.ReceiveHistory(*p, 30))
	cPart := proto.Clone(other).(*types.HistoryPacketData)
	cPart.Round, cPart.Part.Height = 2, 29
	require.NoError(t, unproven.ReceiveHistory(*cPart, 30))
	for _, id := range []string{"a", "b", "c"} {
		bz, e := types.HistorySignBytes(c, unproven.Current, unproven.Previous, id)
		require.NoError(t, e)
		signature := ed25519.Sign(keys[id], bz)
		if id == "b" {
			require.NoError(t, unproven.SealHistory(2, 31, signature))
			continue
		}
		require.NoError(t, unproven.ReceiveHistory(types.HistoryPacketData{SessionHash: types.HistorySessionHash(c), Round: 2, SourceDomainId: id,
			Seal: &types.HistorySeal{DomainId: id, Signature: signature, PreviousSignatureHash: types.HistoryPreviousSignatureHash(unproven.Previous, id), RecordedHeight: 31}}, 31))
	}
	require.Equal(t, types.HistoryCompleted, unproven.Current.Status)
	require.True(t, proto.Equal(&s.Current, &unproven.Previous))
	if file := os.Getenv("HISTORY_TAMPER_MODULE_OUTPUT"); file != "" {
		bz, e := json.MarshalIndent(map[string]any{"kind": "production-ibc-handler-isolated-audit", "scope": "OnRecvPacket and keeper state; mocked IBC core authentication; no live malicious traffic", "rows": rows,
			"boundary": map[string]any{"stateMachineOnly": true, "allTestParticipantsSignedNewClaims": true, "sourceAncestryProofSupplied": false, "newRoundCompleted": unproven.Current.Status == types.HistoryCompleted, "previousRecordUnchanged": true}}, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(file, append(bz, '\n'), 0600))
	}
}
