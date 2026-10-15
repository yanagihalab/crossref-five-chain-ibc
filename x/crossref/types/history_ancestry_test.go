package types_test

import (
	"bytes"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/gogoproto/proto"
	fixture "github.com/crossref/crossrefd/testutil/historyproof"
	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func TestAncestryProofRejectsBrokenHistory(t *testing.T) {
	headers := fixture.Headers("a", 80)
	anchor := fixture.Part("a", headers, 9, 9)
	part := fixture.Part("a", headers, 9, 19)
	require.NoError(t, types.VerifyHistoryAncestry(part, &anchor))
	require.NoError(t, types.VerifyHistoryAncestry(anchor, nil))
	for _, name := range []string{"missing", "oversized", "gap", "wrong_anchor", "wrong_post_state", "wrong_terminal", "wrong_chain", "omitted", "reordered", "broken_link", "future_time_reversal", "extra_header", "unknown_field"} {
		t.Run(name, func(t *testing.T) {
			p := proto.Clone(&part).(*types.HistoryPart)
			a := proto.Clone(&anchor).(*types.HistoryPart)
			var proof types.HistoryAncestryProof
			require.NoError(t, proof.Unmarshal(p.AncestryProof))
			switch name {
			case "missing":
				p.AncestryProof = nil
			case "oversized":
				p.AncestryProof = make([]byte, types.MaxHistoryProofBytes+1)
			case "gap":
				p.Height = 79
			case "wrong_anchor":
				a.BlockHash[0] ^= 1
			case "wrong_post_state":
				a.AppHash[0] ^= 1
			case "wrong_terminal":
				p.AppHash[0] ^= 1
			case "wrong_chain":
				p.ChainId = "other"
			case "omitted":
				proof.Headers = proof.Headers[1:]
				p.AncestryProof, _ = proof.Marshal()
			case "reordered":
				proof.Headers[1], proof.Headers[2] = proof.Headers[2], proof.Headers[1]
				p.AncestryProof, _ = proof.Marshal()
			case "extra_header":
				proof.Headers = append(proof.Headers, proof.Headers[len(proof.Headers)-1])
				p.AncestryProof, _ = proof.Marshal()
			case "unknown_field":
				p.AncestryProof = append(p.AncestryProof, 0x10, 0x01)
			default:
				var h cmtproto.Header
				require.NoError(t, h.Unmarshal(proof.Headers[2]))
				if name == "broken_link" {
					h.LastBlockId.Hash[0] ^= 1
				} else {
					h.Time = headers[0].Time
				}
				proof.Headers[2], _ = h.Marshal()
				p.AncestryProof, _ = proof.Marshal()
			}
			require.Error(t, types.VerifyHistoryAncestry(*p, a))
		})
	}
	// Recompute every descendant of a rewritten anchor. The old external hash
	// still rejects this internally consistent replacement.
	headers[8].DataHash = bytes.Repeat([]byte{7}, 32)
	for i := 9; i < len(headers); i++ {
		headers[i].LastBlockID.Hash = headers[i-1].Hash()
	}
	forged := fixture.Part("a", headers, 9, 19)
	require.ErrorContains(t, types.VerifyHistoryAncestry(forged, &anchor), "anchor hash")
}

func TestLongAncestryVerifiesEveryHeaderIncludingBatchBoundaries(t *testing.T) {
	headers := fixture.Headers("a", types.MaxHistoryProofHeaders+2)
	anchor := fixture.Part("a", headers, 1, 1)
	for _, gap := range []int{94, 102, 105, 510} {
		part := fixture.Part("a", headers, 1, 1+gap)
		require.NoError(t, types.VerifyHistoryAncestry(part, &anchor))
	}
	part := fixture.Part("a", headers, 1, 511)
	var proof types.HistoryAncestryProof
	require.NoError(t, proof.Unmarshal(part.AncestryProof))
	for i := range proof.Headers {
		bad := proto.Clone(&proof).(*types.HistoryAncestryProof)
		var h cmtproto.Header
		require.NoError(t, h.Unmarshal(bad.Headers[i]))
		h.AppHash[0] ^= 1
		bad.Headers[i], _ = h.Marshal()
		p := part
		p.AncestryProof, _ = bad.Marshal()
		require.Error(t, types.VerifyHistoryAncestry(p, &anchor), "mutated header %d", i)
	}
	tooLong := fixture.Part("a", headers, 1, 512)
	require.ErrorContains(t, types.VerifyHistoryAncestry(tooLong, &anchor), "bounded proof window")
	for _, index := range []int{19, 20, 63, 64, 255, 510} {
		bad := proto.Clone(&proof).(*types.HistoryAncestryProof)
		bad.Headers = append(bad.Headers[:index], bad.Headers[index+1:]...)
		p := part
		p.AncestryProof, _ = bad.Marshal()
		require.Error(t, types.VerifyHistoryAncestry(p, &anchor))
	}
}

func TestAncestryRequiredBeforeSendReceiveAndSign(t *testing.T) {
	c := fixture.Config("b")
	previous := fixture.Round(c, 1, 9, types.HistoryRound{})
	s := types.HistoryState{Config: c, Current: previous}
	headers := fixture.Headers("b", 20)
	opened, err := s.BeginHistory(20, headers[18].Hash(), headers[19].AppHash)
	require.NoError(t, err)
	require.True(t, opened)
	require.Empty(t, s.Outbox)
	next := fixture.Round(c, 2, 19, previous)
	for _, p := range next.Parts {
		if p.DomainId == "b" {
			continue
		}
		wire := types.HistoryPacketData{SessionHash: types.HistorySessionHash(c), Round: 2, SourceDomainId: p.DomainId, Part: &p}
		bad := proto.Clone(&wire).(*types.HistoryPacketData)
		bad.Part.AncestryProof = nil
		require.Error(t, s.ReceiveHistory(*bad, 20))
		require.NoError(t, s.ReceiveHistory(wire, 20))
	}
	require.Equal(t, types.HistoryCollecting, s.Current.Status)
	_, err = types.HistorySignBytes(c, s.Current, s.Previous, "b")
	require.Error(t, err)
	require.NoError(t, s.AttachHistoryProof(2, next.Parts[1].AncestryProof, 21))
	require.Len(t, s.Outbox, 2)
	require.Equal(t, types.HistorySigning, s.Current.Status)
	_, err = types.HistorySignBytes(c, s.Current, s.Previous, "b")
	require.NoError(t, err)
	require.NoError(t, types.ValidateHistoryGenesis(&s, []types.HistoryRound{previous, s.Current}))
	bad := proto.Clone(&s).(*types.HistoryState)
	bad.Current.Parts[0].AncestryProof = nil
	require.Error(t, bad.SealHistory(2, 21, next.Seals[1].Signature))
	require.Error(t, types.ValidateHistoryGenesis(bad, []types.HistoryRound{previous, bad.Current}))
	// An early n+1 packet must be proved against the current fixed source part.
	pending := types.HistoryState{Config: c, Current: previous}
	p := next.Parts[0]
	wire := types.HistoryPacketData{SessionHash: types.HistorySessionHash(c), Round: 2, SourceDomainId: "a", Part: &p}
	require.NoError(t, pending.ReceiveHistory(wire, 15))
	require.Len(t, pending.PendingParts, 1)
	badWire := proto.Clone(&wire).(*types.HistoryPacketData)
	badWire.Part.AncestryProof = nil
	require.Error(t, pending.ReceiveHistory(*badWire, 15))
	legacy := c
	legacy.RequireAncestryProof = false
	require.NotEqual(t, types.HistorySessionHash(c), types.HistorySessionHash(legacy))
}
