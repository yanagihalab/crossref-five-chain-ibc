package types_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	core "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/cosmos/gogoproto/jsonpb"
	"github.com/cosmos/gogoproto/proto"
	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func TestHistoryAncestryRealEvidence(t *testing.T) {
	file := os.Getenv("HISTORY_ANCESTRY_EVIDENCE")
	if file == "" {
		t.Skip("set HISTORY_ANCESTRY_EVIDENCE to a read-only collected fixture")
	}
	bz, err := os.ReadFile(file)
	require.NoError(t, err)
	var e struct {
		OldPart json.RawMessage   `json:"oldPart"`
		NewPart json.RawMessage   `json:"newPart"`
		Blocks  []json.RawMessage `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(bz, &e))
	var old, part types.HistoryPart
	require.NoError(t, jsonpb.UnmarshalString(string(e.OldPart), &old))
	require.NoError(t, jsonpb.UnmarshalString(string(e.NewPart), &part))
	proof := types.HistoryAncestryProof{}
	for _, raw := range e.Blocks {
		var r core.ResultBlock
		require.NoError(t, cmtjson.Unmarshal(raw, &r))
		require.NotNil(t, r.Block)
		pb := r.Block.Header.ToProto()
		b, err := pb.Marshal()
		require.NoError(t, err)
		proof.Headers = append(proof.Headers, b)
	}
	part.AncestryProof, err = proof.Marshal()
	require.NoError(t, err)
	start := time.Now()
	for i := 0; i < 1000; i++ {
		require.NoError(t, types.VerifyHistoryAncestry(part, &old))
	}
	avg := float64(time.Since(start).Nanoseconds()) / 1e6 / 1000
	mutations := 0
	for i := range proof.Headers {
		bad := proto.Clone(&proof).(*types.HistoryAncestryProof)
		var h cmtproto.Header
		require.NoError(t, h.Unmarshal(bad.Headers[i]))
		require.NotEmpty(t, h.AppHash)
		h.AppHash[0] ^= 1
		bad.Headers[i], err = h.Marshal()
		require.NoError(t, err)
		p := part
		p.AncestryProof, err = bad.Marshal()
		require.NoError(t, err)
		require.Error(t, types.VerifyHistoryAncestry(p, &old))
		mutations++
	}
	digest := sha256.Sum256(bz)
	t.Logf("real ancestry %d->%d: %d headers, %d bytes, 1000 accepted, %d header mutations rejected, avg %.6f ms", old.Height, part.Height, len(proof.Headers), len(part.AncestryProof), mutations, avg)
	if output := os.Getenv("HISTORY_ANCESTRY_RESULT"); output != "" {
		result, err := json.MarshalIndent(map[string]any{"kind": "real-evidence-offline-ancestry-verification", "evidenceSHA256": hex.EncodeToString(digest[:]), "anchorHeight": old.Height, "newPartHeight": part.Height, "headers": len(proof.Headers), "proofBytes": len(part.AncestryProof), "normalAccepted": 1000, "headerMutationsRejected": mutations, "avgMs": avg, "liveV3IBC": false}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, append(result, '\n'), 0600))
	}
}

func TestHistoryAncestryLiveReport(t *testing.T) {
	file := os.Getenv("HISTORY_ANCESTRY_LIVE_REPORT")
	if file == "" {
		t.Skip("set HISTORY_ANCESTRY_LIVE_REPORT to a completed strict IBC report")
	}
	bz, err := os.ReadFile(file)
	require.NoError(t, err)
	var report struct {
		RunID                string `json:"runId"`
		Status               string `json:"status"`
		RequireAncestryProof bool   `json:"requireAncestryProof"`
		Rounds               []struct {
			Canonical json.RawMessage `json:"canonical"`
		} `json:"rounds"`
	}
	require.NoError(t, json.Unmarshal(bz, &report))
	require.Contains(t, []string{"completed", "partial"}, report.Status)
	require.True(t, report.RequireAncestryProof)
	// Two rounds are sufficient to verify the initial anchor and its successor.
	require.GreaterOrEqual(t, len(report.Rounds), 2)
	previous := map[string]types.HistoryPart{}
	verified, rejected, totalBytes := 0, 0, 0
	type proofTiming struct {
		Round             uint64    `json:"round"`
		Domain            string    `json:"domain"`
		Headers           int       `json:"headers"`
		ProofBytes        int       `json:"proofBytes"`
		VerifyMs          float64   `json:"verifyMs"`
		DecodeMs          float64   `json:"decodeMs"`
		MutationPrepareMs []float64 `json:"mutationPrepareMs"`
		MutationVerifyMs  []float64 `json:"mutationVerifyMs"`
	}
	proofTimings := make([]proofTiming, 0, len(report.Rounds)*3)
	for index, entry := range report.Rounds {
		var round types.HistoryRound
		require.NoError(t, jsonpb.UnmarshalString(string(entry.Canonical), &round))
		require.Equal(t, uint64(index+1), round.Number)
		require.Len(t, round.Parts, 3)
		seen := map[string]bool{}
		for _, part := range round.Parts {
			require.False(t, seen[part.DomainId])
			seen[part.DomainId] = true
			var anchor *types.HistoryPart
			if index > 0 {
				old, ok := previous[part.DomainId]
				require.True(t, ok)
				anchor = &old
			}
			started := time.Now()
			verifyErr := types.VerifyHistoryAncestry(part, anchor)
			verifiedMs := float64(time.Since(started).Nanoseconds()) / 1e6
			require.NoError(t, verifyErr)
			verified++
			totalBytes += len(part.AncestryProof)
			var proof types.HistoryAncestryProof
			started = time.Now()
			decodeErr := proof.Unmarshal(part.AncestryProof)
			decodeMs := float64(time.Since(started).Nanoseconds()) / 1e6
			require.NoError(t, decodeErr)
			row := proofTiming{Round: round.Number, Domain: part.DomainId, Headers: len(proof.Headers),
				ProofBytes: len(part.AncestryProof), VerifyMs: verifiedMs, DecodeMs: decodeMs,
				MutationPrepareMs: make([]float64, 0, len(proof.Headers)), MutationVerifyMs: make([]float64, 0, len(proof.Headers))}
			for i := range proof.Headers {
				started = time.Now()
				bad := proto.Clone(&proof).(*types.HistoryAncestryProof)
				var header cmtproto.Header
				require.NoError(t, header.Unmarshal(bad.Headers[i]))
				require.NotEmpty(t, header.AppHash)
				header.AppHash[0] ^= 1
				bad.Headers[i], err = header.Marshal()
				require.NoError(t, err)
				changed := part
				changed.AncestryProof, err = bad.Marshal()
				require.NoError(t, err)
				row.MutationPrepareMs = append(row.MutationPrepareMs, float64(time.Since(started).Nanoseconds())/1e6)
				started = time.Now()
				mutationErr := types.VerifyHistoryAncestry(changed, anchor)
				row.MutationVerifyMs = append(row.MutationVerifyMs, float64(time.Since(started).Nanoseconds())/1e6)
				require.Error(t, mutationErr)
				rejected++
			}
			proofTimings = append(proofTimings, row)
			t.Logf("round %d %s height %d: %d headers verified", round.Number, part.DomainId, part.Height, len(proof.Headers))
			previous[part.DomainId] = part
		}
	}
	digest := sha256.Sum256(bz)
	t.Logf("live stored evidence: %d proofs verified; %d offline mutations rejected", verified, rejected)
	if output := os.Getenv("HISTORY_ANCESTRY_RESULT"); output != "" {
		result, err := json.MarshalIndent(map[string]any{
			"kind": "live-stored-ancestry-offline-reverification", "evidenceSHA256": hex.EncodeToString(digest[:]),
			"proofsVerified": verified, "totalProofBytes": totalBytes, "offlineHeaderMutationsRejected": rejected,
			"mutationsBroadcast": false, "runId": report.RunID, "timingClock": "process-monotonic",
			"timingSchema": "offline-ancestry-timing-v1",
			"timingScope":  "offline-reverification", "proofTimings": proofTimings,
		}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, append(result, '\n'), 0600))
	}
}
