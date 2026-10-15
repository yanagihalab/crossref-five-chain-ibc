package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	core "github.com/cometbft/cometbft/rpc/core/types"
	cmt "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	fixture "github.com/crossref/crossrefd/testutil/historyproof"
	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func code(n uint32) *uint32 { return &n }

type txRPCFixture struct {
	*httptest.Server
	mu      sync.Mutex
	replies map[string]string
}

func newTxRPC(t *testing.T, chain string) *txRPCFixture {
	f := &txRPCFixture{replies: map[string]string{}}
	f.setRaw("/status", fmt.Sprintf(`{"result":{"node_info":{"network":%q},"sync_info":{"latest_block_height":"2000","catching_up":false}}}`, chain))
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		body, ok := f.replies[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *txRPCFixture) setRaw(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[path] = body
}

func (f *txRPCFixture) result(t *testing.T, r core.ResultTx) {
	bz, err := cmtjson.Marshal(r)
	require.NoError(t, err)
	f.setRaw("/tx", fmt.Sprintf(`{"result":%s}`, bz))
}

func txResult(label string, resultCode uint32, wanted int64) core.ResultTx {
	tx := cmt.Tx(label)
	return core.ResultTx{Hash: tx.Hash(), Height: 1302, Tx: tx,
		TxResult: abci.ExecTxResult{Code: resultCode, Codespace: "sdk", GasWanted: wanted, GasUsed: wanted + 32000}}
}

func hashOf(r core.ResultTx) string { return strings.ToUpper(hex.EncodeToString(r.Hash)) }

func mockSignerCLI(t *testing.T, o *options, response types.QueryHistoryResponse, reply string) string {
	dir := t.TempDir()
	o.home, o.journal, o.binary = dir, filepath.Join(dir, "journal.json"), filepath.Join(dir, "cli")
	setCLIState(t, dir, response)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broadcast.json"), []byte(reply), 0600))
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
  "query crossref") cat %q ;;
  "tx crossref")
    printf 'ATTEMPT\n' >> %q
    printf '%%s\n' "$@" >> %q
    cat %q ;;
  *) exit 2 ;;
esac
`, filepath.Join(dir, "state.json"), filepath.Join(dir, "calls"), filepath.Join(dir, "calls"), filepath.Join(dir, "broadcast.json"))
	require.NoError(t, os.WriteFile(o.binary, []byte(script), 0700))
	return dir
}

func setCLIState(t *testing.T, dir string, r types.QueryHistoryResponse) {
	bz, err := codec.NewProtoCodec(codectypes.NewInterfaceRegistry()).MarshalJSON(&r)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), bz, 0600))
}

func reloadJournal(t *testing.T, file string) journal {
	bz, err := os.ReadFile(file)
	require.NoError(t, err)
	var j journal
	require.NoError(t, json.Unmarshal(bz, &j))
	return j
}

func TestCommittedFailureRetriesAndPreservesEvidenceAcrossRestart(t *testing.T) {
	o, key, response := responseFixture(t)
	o.gas = 6089593
	f := newTxRPC(t, o.chain)
	o.node = f.URL
	failed := txResult("first", 11, int64(o.gas))
	accepted := txResult("replacement", 0, int64(o.gas*2))
	dir := mockSignerCLI(t, &o, response, fmt.Sprintf(`{"code":0,"txhash":%q}`, hashOf(accepted)))
	id, r, err := signResponse(o, key, response, journal{})
	require.NoError(t, err)
	r.TxHash = hashOf(failed) // Legacy journals did not have Attempts.
	j := journal{id: r}
	f.result(t, failed)
	require.NoError(t, reconcileTransactions(context.Background(), o, j))
	j = reloadJournal(t, o.journal)
	require.Equal(t, uint32(11), *j[id].Attempts[0].Code)
	require.NoError(t, broadcastRecorded(context.Background(), o, key, j, id, j[id], "sign-history-round", "1", r.Signature))
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	require.NoError(t, err)
	require.Contains(t, string(calls), "--gas\n12179186\n")
	require.NotContains(t, string(calls), "auto")
	require.NotContains(t, string(calls), "--sequence")
	require.Len(t, j[id].Attempts, 2)
	require.Equal(t, hashOf(failed), j[id].Attempts[0].Hash)
	require.Equal(t, hashOf(accepted), j[id].Attempts[1].Hash)
	require.Equal(t, r.Digest, j[id].Digest)
	require.Equal(t, r.Signature, j[id].Signature)

	// Pending is not permission to resend, including after a restart.
	j = reloadJournal(t, o.journal)
	require.NoError(t, broadcastRecorded(context.Background(), o, key, j, id, j[id], "sign-history-round", "1", r.Signature))
	f.result(t, accepted)
	// Reconcile success even when the chain no longer requests a signature.
	response.SignBytes = nil
	setCLIState(t, dir, response)
	require.NoError(t, step(context.Background(), o, key, j))
	j = reloadJournal(t, o.journal)
	require.Equal(t, uint32(0), *j[id].Attempts[1].Code)
	require.NoError(t, broadcastRecorded(context.Background(), o, key, j, id, j[id], "sign-history-round", "1", r.Signature))
	after, err := os.ReadFile(filepath.Join(dir, "calls"))
	require.NoError(t, err)
	require.Equal(t, string(calls), string(after))
}

func TestUnknownOrInvalidCommittedResultNeverResubmits(t *testing.T) {
	for _, kind := range []string{"not-found", "network", "malformed", "wrong-hash", "wrong-tx", "missing-code", "no-height", "wrong-chain", "catching-up"} {
		t.Run(kind, func(t *testing.T) {
			o, key, response := responseFixture(t)
			f := newTxRPC(t, o.chain)
			o.node = f.URL
			dir := mockSignerCLI(t, &o, response, `{}`)
			result := txResult("pending", 11, 6089593)
			id, r, err := signResponse(o, key, response, journal{})
			require.NoError(t, err)
			r.TxHash = hashOf(result)
			j := journal{id: r}
			switch kind {
			case "not-found":
				f.setRaw("/tx", `{"error":{"code":-32603}}`)
			case "network":
				// No /tx handler: returns HTTP 503.
			case "malformed":
				f.setRaw("/tx", `{`)
			case "wrong-hash":
				result.Hash = cmt.Tx("other").Hash()
				f.result(t, result)
			case "wrong-tx":
				result.Tx = cmt.Tx("other")
				f.result(t, result)
			case "missing-code":
				f.setRaw("/tx", `{"result":{"height":"1302","tx_result":{}}}`)
			case "no-height":
				result.Height = 0
				f.result(t, result)
			case "wrong-chain":
				f.setRaw("/status", `{"result":{"node_info":{"network":"other"}}}`)
			case "catching-up":
				f.setRaw("/status", `{"result":{"node_info":{"network":"a"},"sync_info":{"catching_up":true}}}`)
			}
			require.Error(t, step(context.Background(), o, key, j))
			require.NoFileExists(t, filepath.Join(dir, "calls"))
			require.Equal(t, r.TxHash, j[id].TxHash)
		})
	}
}

func TestRetryPolicyIsBoundedAndOnlyDefinitiveFailuresQualify(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    signedRound
		gas  uint64
		stop bool
	}{
		{"initial", signedRound{}, defaultHistoryGas, false},
		{"uncertain", signedRound{Uncertain: true}, 0, true},
		{"legacy-pending", signedRound{TxHash: strings.Repeat("A", 64)}, 0, false},
		{"pending", signedRound{Attempts: []txAttempt{{Hash: "pending", CheckCode: code(0)}}}, 0, false},
		{"success", signedRound{Attempts: []txAttempt{{Code: code(0)}}}, 0, false},
		{"gas", signedRound{Attempts: []txAttempt{{Code: code(11), Codespace: "sdk", GasWanted: 6089593}}}, defaultHistoryGas, false},
		{"gas-cap", signedRound{Attempts: []txAttempt{{Code: code(11), Codespace: "sdk", GasLimit: defaultHistoryGas}}}, maxHistoryGas, false},
		{"at-cap", signedRound{Attempts: []txAttempt{{Code: code(11), Codespace: "sdk", GasLimit: maxHistoryGas}}}, 0, true},
		{"sequence", signedRound{Attempts: []txAttempt{{Code: code(32), Codespace: "sdk"}}}, defaultHistoryGas, false},
		{"check-sequence", signedRound{Attempts: []txAttempt{{CheckCode: code(32), Codespace: "sdk"}}}, defaultHistoryGas, false},
		{"unauthorized", signedRound{Attempts: []txAttempt{{Code: code(4), Codespace: "sdk"}}}, 0, true},
		{"other-codespace", signedRound{Attempts: []txAttempt{{Code: code(11), Codespace: "crossref"}}}, 0, true},
		{"exhausted", signedRound{Attempts: []txAttempt{{}, {}, {Code: code(11), Codespace: "sdk"}}}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gas, err := submissionGas(options{}, tc.r)
			require.Equal(t, tc.gas, gas)
			if tc.stop {
				require.ErrorIs(t, err, errSubmissionStopped)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBroadcastOutcomeUnknownSurvivesRestart(t *testing.T) {
	for _, reply := range []string{`{`, `{"txhash":"missing-code"}`, `{"code":0,"txhash":"bad"}`} {
		o, key, response := responseFixture(t)
		f := newTxRPC(t, o.chain)
		o.node = f.URL
		dir := mockSignerCLI(t, &o, response, reply)
		id, r, err := signResponse(o, key, response, journal{})
		require.NoError(t, err)
		j := journal{}
		require.ErrorIs(t, broadcastRecorded(context.Background(), o, key, j, id, r, "sign-history-round", "1", r.Signature), errSubmissionStopped)
		j = reloadJournal(t, o.journal)
		require.True(t, j[id].Uncertain)
		require.Len(t, j[id].Attempts, 1)
		before, err := os.ReadFile(filepath.Join(dir, "calls"))
		require.NoError(t, err)
		require.ErrorIs(t, reconcileTransactions(context.Background(), o, j), errSubmissionStopped)
		require.ErrorIs(t, broadcastRecorded(context.Background(), o, key, j, id, j[id], "sign-history-round", "1", r.Signature), errSubmissionStopped)
		after, err := os.ReadFile(filepath.Join(dir, "calls"))
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
	}
}

func TestRejectedCheckTxCannotRetryWithoutBound(t *testing.T) {
	o, key, response := responseFixture(t)
	f := newTxRPC(t, o.chain)
	o.node = f.URL
	dir := mockSignerCLI(t, &o, response, `{"code":32,"codespace":"sdk"}`)
	id, r, err := signResponse(o, key, response, journal{})
	require.NoError(t, err)
	j := journal{}
	for i := 0; i < maxTxAttempts; i++ {
		err = broadcastRecorded(context.Background(), o, key, j, id, r, "sign-history-round", "1", r.Signature)
		require.Error(t, err)
		j = reloadJournal(t, o.journal)
		r = j[id]
		require.Len(t, r.Attempts, i+1)
	}
	require.ErrorIs(t, err, errSubmissionStopped)
	require.ErrorIs(t, broadcastRecorded(context.Background(), o, key, j, id, r, "sign-history-round", "1", r.Signature), errSubmissionStopped)
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	require.NoError(t, err)
	require.Equal(t, maxTxAttempts, strings.Count(string(calls), "ATTEMPT\n"))
}

func TestProofRetryRechecksIntentAndAlreadyAppliedState(t *testing.T) {
	c := fixture.Config("a")
	round := fixture.Round(c, 1, 9, types.HistoryRound{})
	proof := round.Parts[0].AncestryProof
	round.Parts = round.Parts[:1]
	round.Parts[0].AncestryProof = nil
	round.Status = types.HistoryCollecting
	response := types.QueryHistoryResponse{State: types.HistoryState{Config: c, Current: round}}
	o := options{chain: "a", session: hex.EncodeToString(types.HistorySessionHash(c))}
	f := newTxRPC(t, o.chain)
	o.node = f.URL
	dir := mockSignerCLI(t, &o, response, fmt.Sprintf(`{"code":0,"txhash":%q}`, strings.Repeat("A", 64)))
	file := filepath.Join(dir, "proof")
	require.NoError(t, os.WriteFile(file, proof, 0600))
	digest := sha256.Sum256(proof)
	id := o.session + "/proof/a/1"
	r := signedRound{Digest: hex.EncodeToString(digest[:]), Attempts: []txAttempt{{Code: code(11), Codespace: "sdk", GasWanted: 6089593}}}
	j := journal{id: r}
	args := []string{"attach-history-proof", "1", "@" + file}
	needed, err := submissionNeeded(context.Background(), o, fixture.Key("a"), j, id, r, args)
	require.NoError(t, err)
	require.True(t, needed)
	response.State.Current.Parts[0].AncestryProof = proof
	setCLIState(t, dir, response)
	require.NoError(t, broadcastRecorded(context.Background(), o, fixture.Key("a"), j, id, r, args...))
	require.NoFileExists(t, filepath.Join(dir, "calls"))
	response.State.Current.Parts[0].AncestryProof = nil
	response.State.Current.Parts[0].BlockHash[0] ^= 1
	setCLIState(t, dir, response)
	_, err = submissionNeeded(context.Background(), o, fixture.Key("a"), j, id, r, args)
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(dir, "calls"))
}

func TestRetryRechecksRoundSessionAndRPCChain(t *testing.T) {
	for _, kind := range []string{"round-advanced", "paused", "session-changed", "RPC-chain-changed", "signing-bytes-changed"} {
		t.Run(kind, func(t *testing.T) {
			o, key, response := responseFixture(t)
			f := newTxRPC(t, o.chain)
			o.node = f.URL
			id, r, err := signResponse(o, key, response, journal{})
			require.NoError(t, err)
			r.Attempts = []txAttempt{{Code: code(11), Codespace: "sdk", GasWanted: 6089593}}
			j := journal{id: r}
			switch kind {
			case "round-advanced":
				response.State.Current.Number++
			case "paused":
				response.State.Config.Enabled = false
			case "session-changed":
				response.State.Config.SessionId = "other"
			case "RPC-chain-changed":
				f.setRaw("/status", `{"result":{"node_info":{"network":"other"}}}`)
			case "signing-bytes-changed":
				response.SignBytes = []byte("other")
			}
			dir := mockSignerCLI(t, &o, response, `{}`)
			err = broadcastRecorded(context.Background(), o, key, j, id, r, "sign-history-round", "1", r.Signature)
			if kind == "round-advanced" || kind == "paused" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.NoFileExists(t, filepath.Join(dir, "calls"))
			require.Len(t, j[id].Attempts, 1)
		})
	}
}

func TestBroadcastCommandFailureAndJournalFailureAreFailClosed(t *testing.T) {
	for _, kind := range []string{"command", "journal"} {
		t.Run(kind, func(t *testing.T) {
			o, key, response := responseFixture(t)
			f := newTxRPC(t, o.chain)
			o.node = f.URL
			dir := mockSignerCLI(t, &o, response, `{}`)
			id, r, err := signResponse(o, key, response, journal{})
			require.NoError(t, err)
			j := journal{}
			if kind == "journal" {
				o.journal = filepath.Join(dir, "missing", "journal.json")
			} else {
				bz, err := os.ReadFile(o.binary)
				require.NoError(t, err)
				// The process fails after the submit command was invoked, so its
				// on-chain outcome is unknown even though the local exit is known.
				script := strings.Replace(string(bz), "cat "+fmt.Sprintf("%q", filepath.Join(dir, "broadcast.json")), "exit 1", 1)
				require.NoError(t, os.WriteFile(o.binary, []byte(script), 0700))
			}
			require.ErrorIs(t, broadcastRecorded(context.Background(), o, key, j, id, r, "sign-history-round", "1", r.Signature), errSubmissionStopped)
			if kind == "journal" {
				require.NoFileExists(t, filepath.Join(dir, "calls"))
			} else {
				j = reloadJournal(t, o.journal)
				require.True(t, j[id].Uncertain)
				require.ErrorIs(t, reconcileTransactions(context.Background(), o, j), errSubmissionStopped)
			}
		})
	}
}
