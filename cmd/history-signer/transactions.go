package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	cmtjson "github.com/cometbft/cometbft/libs/json"
	core "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/crossref/crossrefd/x/crossref/types"
)

const (
	defaultHistoryGas = uint64(150000000)
	maxHistoryGas     = uint64(250000000)
	maxTxAttempts     = 3 // Initial submission plus at most two confirmed-failure retries.
)

var errSubmissionStopped = errors.New("history submission requires operator intervention")

type txAttempt struct {
	Hash      string  `json:"hash,omitempty"`
	GasLimit  uint64  `json:"gas_limit,omitempty"`
	CheckCode *uint32 `json:"check_code,omitempty"`
	Height    int64   `json:"height,omitempty"`
	Code      *uint32 `json:"code,omitempty"`
	Codespace string  `json:"codespace,omitempty"`
	GasWanted int64   `json:"gas_wanted,omitempty"`
	GasUsed   int64   `json:"gas_used,omitempty"`
}

func saveSubmission(o options, j journal, id string, record signedRound) error {
	j[id] = record
	if err := persist(o.journal, j); err != nil {
		return fmt.Errorf("%w: cannot persist transaction journal", errSubmissionStopped)
	}
	return nil
}

func withLegacyAttempt(record signedRound) signedRound {
	if len(record.Attempts) == 0 && record.TxHash != "" {
		record.Attempts = []txAttempt{{Hash: record.TxHash}}
	}
	return record
}

func historyRPC(ctx context.Context, o options, endpoint string, query url.Values, result any) (err error) {
	started := time.Now()
	defer func() { o.recordTiming("transaction.rpc", endpoint, 0, started, err) }()
	u, err := url.Parse(o.node)
	if err != nil {
		return fmt.Errorf("invalid RPC URL")
	}
	if u.Scheme == "tcp" {
		u.Scheme = "http"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported RPC scheme")
	}
	u.Path, u.RawQuery = endpoint, query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("invalid RPC request")
	}
	client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("RPC redirect refused") }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("transaction RPC unavailable; no resubmission")
	}
	defer resp.Body.Close()
	bz, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || resp.StatusCode != http.StatusOK || len(bz) > 4<<20 {
		return fmt.Errorf("invalid transaction RPC response; no resubmission")
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(bz, &envelope) != nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" ||
		(len(envelope.Error) > 0 && string(envelope.Error) != "null") {
		// A missing/unindexed transaction is not evidence that it failed.
		return fmt.Errorf("transaction RPC result unavailable; no resubmission")
	}
	if err = cmtjson.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("malformed transaction RPC result; no resubmission")
	}
	return nil
}

func checkRPCChain(ctx context.Context, o options) error {
	var status core.ResultStatus
	if err := historyRPC(ctx, o, "/status", nil, &status); err != nil {
		return err
	}
	if status.NodeInfo.Network != o.chain {
		return fmt.Errorf("%w: RPC chain ID mismatch", errSubmissionStopped)
	}
	if status.SyncInfo.CatchingUp || status.SyncInfo.LatestBlockHeight <= 0 {
		return fmt.Errorf("RPC not synchronized; no resubmission")
	}
	return nil
}

func reconcileTransactions(ctx context.Context, o options, j journal) error {
	ids := make([]string, 0, len(j))
	for id := range j {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	checkedChain := false
	for _, id := range ids {
		r := withLegacyAttempt(j[id])
		if r.Uncertain {
			return fmt.Errorf("%w: broadcast outcome unknown", errSubmissionStopped)
		}
		if len(r.Attempts) == 0 {
			continue
		}
		a := &r.Attempts[len(r.Attempts)-1]
		if a.Code != nil || (a.CheckCode != nil && *a.CheckCode != 0) {
			continue
		}
		hash, err := hex.DecodeString(a.Hash)
		if err != nil || len(hash) != sha256.Size {
			return fmt.Errorf("%w: missing transaction hash", errSubmissionStopped)
		}
		if !checkedChain {
			if err := checkRPCChain(ctx, o); err != nil {
				return err
			}
			checkedChain = true
		}
		var raw json.RawMessage
		if err := historyRPC(ctx, o, "/tx", url.Values{"hash": {"0x" + a.Hash}}, &raw); err != nil {
			return err
		}
		var result core.ResultTx
		var required struct {
			TxResult json.RawMessage `json:"tx_result"`
		}
		// CometBFT's JSON encoding may omit a zero code. Require the execution
		// object and actual gas accounting, not an invented default for a partial response.
		if json.Unmarshal(raw, &required) != nil || len(required.TxResult) == 0 || string(required.TxResult) == "null" || cmtjson.Unmarshal(raw, &result) != nil ||
			result.Height <= 0 || !bytes.Equal(result.Hash, hash) || !bytes.Equal(result.Tx.Hash(), hash) ||
			(result.TxResult.Code == 0 && (result.TxResult.GasWanted <= 0 || result.TxResult.GasUsed <= 0)) {
			return fmt.Errorf("invalid committed transaction evidence; no resubmission")
		}
		a.Height, a.Code, a.Codespace = result.Height, &result.TxResult.Code, result.TxResult.Codespace
		a.GasWanted, a.GasUsed = result.TxResult.GasWanted, result.TxResult.GasUsed
		if err := saveSubmission(o, j, id, r); err != nil {
			return err
		}
		fmt.Printf("history transaction committed: %s height=%d code=%d gas_wanted=%d gas_used=%d\n", a.Hash, a.Height, *a.Code, a.GasWanted, a.GasUsed)
	}
	return nil
}

func submissionGas(o options, r signedRound) (uint64, error) {
	gas := o.gas
	if gas == 0 {
		gas = defaultHistoryGas
	}
	if gas > maxHistoryGas {
		return 0, fmt.Errorf("%w: gas limit exceeds cap", errSubmissionStopped)
	}
	if r.Uncertain {
		return 0, fmt.Errorf("%w: broadcast outcome unknown", errSubmissionStopped)
	}
	r = withLegacyAttempt(r)
	if len(r.Attempts) == 0 {
		return gas, nil
	}
	a := r.Attempts[len(r.Attempts)-1]
	code := a.Code
	if code == nil && a.CheckCode != nil && *a.CheckCode != 0 {
		code = a.CheckCode
	}
	if code == nil || *code == 0 {
		return 0, nil // Pending/unknown or committed success: never submit again.
	}
	if len(r.Attempts) >= maxTxAttempts {
		return 0, fmt.Errorf("%w: transaction retry budget exhausted", errSubmissionStopped)
	}
	if a.Codespace != sdkerrors.RootCodespace || (*code != sdkerrors.ErrOutOfGas.ABCICode() && *code != sdkerrors.ErrWrongSequence.ABCICode()) {
		return 0, fmt.Errorf("%w: non-retryable transaction code %d", errSubmissionStopped, *code)
	}
	previousGas := a.GasLimit
	if a.GasWanted > 0 && uint64(a.GasWanted) > previousGas {
		previousGas = uint64(a.GasWanted)
	}
	if *code == sdkerrors.ErrOutOfGas.ABCICode() {
		if previousGas >= maxHistoryGas {
			return 0, fmt.Errorf("%w: out of gas at maximum budget", errSubmissionStopped)
		}
		gas = max(gas, min(previousGas*2, maxHistoryGas))
	} else {
		gas = max(gas, previousGas)
	}
	if gas > maxHistoryGas {
		return 0, fmt.Errorf("%w: previous gas exceeds cap", errSubmissionStopped)
	}
	return gas, nil
}

// Refresh protocol state after proof collection/result lookup. The CLI obtains a
// fresh account sequence when constructing each replacement transaction.
func submissionNeeded(ctx context.Context, o options, key ed25519.PrivateKey, j journal, id string, record signedRound, args []string) (bool, error) {
	if err := checkRPCChain(ctx, o); err != nil {
		return false, err
	}
	bz, err := command(ctx, o, "query", "crossref", "history")
	if err != nil {
		return false, err
	}
	var response types.QueryHistoryResponse
	if err := codec.NewProtoCodec(codectypes.NewInterfaceRegistry()).UnmarshalJSON(bz, &response); err != nil {
		return false, fmt.Errorf("invalid history query response")
	}
	s := response.State
	if err := checkPinnedHistory(o, key, s, j); err != nil {
		return false, err
	}
	if len(args) != 3 {
		return false, fmt.Errorf("invalid history submission arguments")
	}
	if !s.Config.Enabled || args[1] != strconv.FormatUint(s.Current.Number, 10) {
		return false, nil
	}
	switch args[0] {
	case "attach-history-proof":
		if id != o.session+"/proof/"+s.Config.LocalDomainId+"/"+args[1] || !strings.HasPrefix(args[2], "@") {
			return false, fmt.Errorf("proof submission identity mismatch")
		}
		proof, err := os.ReadFile(strings.TrimPrefix(args[2], "@"))
		if err != nil {
			return false, fmt.Errorf("cannot read proof input")
		}
		digest := sha256.Sum256(proof)
		if hex.EncodeToString(digest[:]) != record.Digest {
			return false, fmt.Errorf("proof changed since prior submission")
		}
		for _, p := range s.Current.Parts {
			if p.DomainId != s.Config.LocalDomainId {
				continue
			}
			if len(p.AncestryProof) > 0 {
				if !bytes.Equal(p.AncestryProof, proof) {
					return false, fmt.Errorf("on-chain proof differs from journal")
				}
				return false, nil
			}
			p.AncestryProof = proof
			if err := types.VerifyHistoryPartProof(s.Config, s.Current.Number, p, s.Previous); err != nil {
				return false, err
			}
			return s.Config.RequireAncestryProof && s.Current.Status == types.HistoryCollecting, nil
		}
		return false, fmt.Errorf("local Part missing")
	case "sign-history-round":
		for _, seal := range s.Current.Seals {
			if seal.DomainId == s.Config.LocalDomainId {
				if base64.StdEncoding.EncodeToString(seal.Signature) != record.Signature {
					return false, fmt.Errorf("on-chain signature differs from journal")
				}
				return false, nil
			}
		}
		currentID, current, err := signResponse(o, key, response, j)
		if err != nil || currentID == "" {
			return false, err
		}
		if currentID != id || current.Digest != record.Digest || current.Signature != record.Signature || args[2] != record.Signature {
			return false, fmt.Errorf("signature intent changed")
		}
		return true, nil
	default:
		return false, fmt.Errorf("unsupported history transaction")
	}
}

func broadcastRecorded(ctx context.Context, o options, key ed25519.PrivateKey, j journal, id string, record signedRound, args ...string) (err error) {
	round := uint64(0)
	if len(args) > 1 {
		round, _ = strconv.ParseUint(args[1], 10, 64)
	}
	totalStarted := time.Now()
	defer func() { o.recordTiming("transaction.submit", "", round, totalStarted, err) }()
	record = withLegacyAttempt(record)
	gas, err := submissionGas(o, record)
	if err != nil || gas == 0 {
		return err
	}
	started := time.Now()
	needed, err := submissionNeeded(ctx, o, key, j, id, record, args)
	o.recordTiming("transaction.preflight", "", round, started, err)
	if err != nil || !needed {
		return err
	}
	// A crash, timeout, or malformed response after this durable marker is unknown,
	// not permission to retry. Keep all previous attempts and the immutable intent.
	record.Uncertain = true
	record.Attempts = append(record.Attempts, txAttempt{GasLimit: gas})
	started = time.Now()
	if err := saveSubmission(o, j, id, record); err != nil {
		o.recordTiming("transaction.journal", "", round, started, err)
		return err
	}
	o.recordTiming("transaction.journal", "", round, started, nil)
	cmd := append([]string{"tx", "crossref"}, args...)
	cmd = append(cmd, "--chain-id", o.chain, "--from", o.from, "--keyring-backend", o.backend, "--fees", o.fees,
		"--gas", strconv.FormatUint(gas, 10), "--broadcast-mode", "sync", "--yes")
	bz, err := command(ctx, o, cmd...)
	if err != nil {
		return fmt.Errorf("%w: broadcast outcome unknown", errSubmissionStopped)
	}
	var tx struct {
		Code      *uint32 `json:"code"`
		Codespace string  `json:"codespace"`
		TxHash    string  `json:"txhash"`
	}
	if json.Unmarshal(bz, &tx) != nil || tx.Code == nil {
		return fmt.Errorf("%w: unknown broadcast response", errSubmissionStopped)
	}
	if hash, err := hex.DecodeString(tx.TxHash); *tx.Code == 0 && (err != nil || len(hash) != sha256.Size) {
		return fmt.Errorf("%w: missing broadcast hash", errSubmissionStopped)
	}
	a := &record.Attempts[len(record.Attempts)-1]
	a.Hash, a.CheckCode, a.Codespace = tx.TxHash, tx.Code, tx.Codespace
	record.Uncertain = false
	record.TxHash = tx.TxHash
	started = time.Now()
	if err := saveSubmission(o, j, id, record); err != nil {
		o.recordTiming("transaction.journal", "", round, started, err)
		return err
	}
	o.recordTiming("transaction.journal", "", round, started, nil)
	if *tx.Code != 0 {
		if _, err := submissionGas(o, record); err != nil {
			return err
		}
		return fmt.Errorf("CheckTx rejected with code %d; bounded retry on next poll", *tx.Code)
	}
	fmt.Printf("history %s submitted: %s attempt=%d gas_limit=%d\n", args[0], tx.TxHash, len(record.Attempts), gas)
	return nil
}
