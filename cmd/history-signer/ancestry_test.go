package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	cmtjson "github.com/cometbft/cometbft/libs/json"
	core "github.com/cometbft/cometbft/rpc/core/types"
	cmt "github.com/cometbft/cometbft/types"
	"github.com/cosmos/gogoproto/proto"
	fixture "github.com/crossref/crossrefd/testutil/historyproof"
	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func TestSignerVerifiesAncestryAndPinsPreviousAnchors(t *testing.T) {
	c := fixture.Config("a")
	first := fixture.Round(c, 1, 9, types.HistoryRound{})
	o := options{chain: "a", session: hex.EncodeToString(types.HistorySessionHash(c))}
	first.Status = types.HistorySigning
	b, err := types.HistorySignBytes(c, first, types.HistoryRound{}, "a")
	require.NoError(t, err)
	id, record, err := signResponse(o, fixture.Key("a"), types.QueryHistoryResponse{State: types.HistoryState{Config: c, Current: first}, SignBytes: b}, journal{})
	require.NoError(t, err)
	j := journal{id: record}
	first.Status = types.HistoryCompleted
	next := fixture.Round(c, 2, 19, first)
	next.Status = types.HistorySigning
	b, err = types.HistorySignBytes(c, next, first, "a")
	require.NoError(t, err)
	response := types.QueryHistoryResponse{State: types.HistoryState{Config: c, Previous: first, Current: next}, SignBytes: b}
	_, _, err = signResponse(o, fixture.Key("a"), response, j)
	require.NoError(t, err)
	_, _, err = signResponse(o, fixture.Key("a"), response, journal{})
	require.ErrorContains(t, err, "anchors missing")
	bad := proto.Clone(&response).(*types.QueryHistoryResponse)
	bad.State.Current.Parts[1].AncestryProof = nil
	_, _, err = signResponse(o, fixture.Key("a"), *bad, j)
	require.ErrorContains(t, err, "ancestry")
	bad = proto.Clone(&response).(*types.QueryHistoryResponse)
	bad.State.Previous.Parts[0].BlockHash[0] ^= 1
	_, _, err = signResponse(o, fixture.Key("a"), *bad, j)
	require.ErrorContains(t, err, "anchors missing")
	bad = proto.Clone(&response).(*types.QueryHistoryResponse)
	bad.State.Config.RequireAncestryProof = false
	_, _, err = signResponse(o, fixture.Key("a"), *bad, j)
	require.ErrorContains(t, err, "session mismatch")
}

func TestProofCollectorUsesBoundedLocalRPCAndChecksEndpoints(t *testing.T) {
	headers := fixture.Headers("a", 120)
	anchor := fixture.Part("a", headers, 9, 9)
	part := fixture.Part("a", headers, 9, 114)
	part.AncestryProof = nil
	count := 0
	broken := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		require.Equal(t, "/blockchain", r.URL.Path)
		lo, err := strconv.Atoi(r.URL.Query().Get("minHeight"))
		require.NoError(t, err)
		hi, err := strconv.Atoi(r.URL.Query().Get("maxHeight"))
		require.NoError(t, err)
		require.LessOrEqual(t, hi-lo+1, 20)
		metas := []*cmt.BlockMeta{}
		for h := hi; h >= lo; h-- {
			header := headers[h-1]
			if broken == "chain" {
				header.ChainID = "other"
			}
			if broken == "link" && h == 29 {
				header.AppHash = append([]byte(nil), header.AppHash...)
				header.AppHash[0] ^= 1
			}
			metas = append(metas, &cmt.BlockMeta{Header: header})
		}
		if len(metas) > 1 {
			switch broken {
			case "missing":
				metas = metas[1:]
			case "duplicate":
				metas[0] = metas[1]
			case "reordered":
				metas[0], metas[1] = metas[1], metas[0]
			case "null":
				metas[0] = nil
			}
		}
		bz, err := cmtjson.Marshal(core.ResultBlockchainInfo{BlockMetas: metas})
		require.NoError(t, err)
		fmt.Fprintf(w, `{"result":%s}`, bz)
	}))
	defer server.Close()
	proof, err := collectAncestry(context.Background(), options{node: server.URL}, part, &anchor)
	require.NoError(t, err)
	require.Equal(t, 6, count)
	part.AncestryProof = proof
	require.NoError(t, types.VerifyHistoryAncestry(part, &anchor))
	for _, kind := range []string{"chain", "link", "missing", "duplicate", "reordered", "null"} {
		t.Run(kind, func(t *testing.T) {
			broken = kind
			_, err = collectAncestry(context.Background(), options{node: server.URL}, part, &anchor)
			require.Error(t, err)
		})
	}
	part.Height = 1000
	count = 0
	_, err = collectAncestry(context.Background(), options{node: server.URL}, part, &anchor)
	require.ErrorIs(t, err, errAncestryLimit)
	require.Zero(t, count)
}
