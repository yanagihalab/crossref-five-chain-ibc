package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	cmtjson "github.com/cometbft/cometbft/libs/json"
	core "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/crossref/crossrefd/x/crossref/types"
)

var errAncestryLimit = errors.New("history ancestry-v4 bound exceeded; operator intervention required")

func collectAncestry(ctx context.Context, o options, p types.HistoryPart, previous *types.HistoryPart, roundNumber ...uint64) (proofBytes []byte, err error) {
	round := uint64(0)
	if len(roundNumber) > 0 {
		round = roundNumber[0]
	}
	totalStarted := time.Now()
	defer func() { o.recordTiming("proof.collect", "", round, totalStarted, err) }()
	start := p.Height
	if previous != nil {
		start = previous.Height
	}
	if p.Height == 0 || p.Height >= math.MaxInt64 || start == 0 || p.Height < start || p.Height-start > types.MaxHistoryProofHeaders-2 {
		return nil, errAncestryLimit
	}
	base, err := url.Parse(o.node)
	if err != nil {
		return nil, err
	}
	if base.Scheme == "tcp" {
		base.Scheme = "http"
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("unsupported RPC scheme")
	}
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("RPC redirect refused") }}
	proof := types.HistoryAncestryProof{}
	// CometBFT returns at most 20 block metas in descending order per request.
	// Batch retrieval changes transport only: every header is verified below.
	for h := start; h <= p.Height+1; {
		end := h + 19
		if end > p.Height+1 {
			end = p.Height + 1
		}
		u := *base
		u.Path = "/blockchain"
		q := url.Values{}
		q.Set("minHeight", strconv.FormatUint(h, 10))
		q.Set("maxHeight", strconv.FormatUint(end, 10))
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		batchStarted := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			o.recordTiming("proof.rpc_batch", "/blockchain", round, batchStarted, err)
			return nil, fmt.Errorf("ancestry RPC unavailable")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
		resp.Body.Close()
		if readErr == nil && (resp.StatusCode != 200 || len(body) > 4*1024*1024) {
			readErr = fmt.Errorf("invalid ancestry RPC response")
		}
		o.recordTiming("proof.rpc_batch", "/blockchain", round, batchStarted, readErr)
		if readErr != nil || resp.StatusCode != 200 || len(body) > 4*1024*1024 {
			return nil, fmt.Errorf("invalid ancestry RPC response")
		}
		decodeStarted := time.Now()
		var envelope struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(body, &envelope) != nil || (len(envelope.Error) > 0 && string(envelope.Error) != "null") {
			return nil, fmt.Errorf("ancestry RPC error")
		}
		var batch core.ResultBlockchainInfo
		if err = cmtjson.Unmarshal(envelope.Result, &batch); err != nil || len(batch.BlockMetas) != int(end-h+1) {
			return nil, fmt.Errorf("incomplete ancestry batch")
		}
		for i := len(batch.BlockMetas) - 1; i >= 0; i-- {
			meta := batch.BlockMetas[i]
			if meta == nil || meta.Header.Height != int64(h) || meta.Header.ChainID != p.ChainId {
				return nil, fmt.Errorf("ancestry batch height/chain mismatch")
			}
			raw, err := meta.Header.ToProto().Marshal()
			if err != nil {
				return nil, err
			}
			proof.Headers = append(proof.Headers, raw)
			h++
		}
		o.recordTiming("proof.decode_batch", "", round, decodeStarted, nil)
		if proof.Size() > types.MaxHistoryProofBytes {
			return nil, errAncestryLimit
		}
	}
	marshalStarted := time.Now()
	bz, err := proof.Marshal()
	o.recordTiming("proof.marshal", "", round, marshalStarted, err)
	if err != nil {
		return nil, err
	}
	p.AncestryProof = bz
	verifyStarted := time.Now()
	if err = types.VerifyHistoryAncestry(p, previous); err != nil {
		o.recordTiming("proof.local_verify", "", round, verifyStarted, err)
		return nil, err
	}
	o.recordTiming("proof.local_verify", "", round, verifyStarted, nil)
	return bz, nil
}
