// Offline header/Tx-commitment audit, not a full node or a replacement light client.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	cmted "github.com/cometbft/cometbft/crypto/ed25519"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtversion "github.com/cometbft/cometbft/proto/tendermint/version"
	core "github.com/cometbft/cometbft/rpc/core/types"
	cmt "github.com/cometbft/cometbft/types"
	"github.com/cometbft/cometbft/version"
)

type candidate struct {
	Headers []cmt.Header `json:"headers"`
	Data    []cmt.Data   `json:"data"`
	Commit  *cmt.Commit  `json:"commit"`
}
type anchor struct {
	Height int64
	Hash   []byte
}
type trust struct {
	ChainID      string
	Validators   *cmt.ValidatorSet
	Anchor       anchor
	TargetHeight int64
}

// Trust inputs must be pinned separately, never extracted from the candidate.
// Only a fixed validator set is supported; SDK execution and historical commit
// bodies/part-set availability are deliberately outside this verifier's scope.
func verify(c candidate, t trust, pinned bool) error {
	if len(c.Headers) == 0 || len(c.Headers) != len(c.Data) || c.Commit == nil || t.Validators == nil || t.ChainID == "" {
		return fmt.Errorf("missing evidence")
	}
	if pinned && (t.Anchor.Height <= 0 || len(t.Anchor.Hash) != 32) {
		return fmt.Errorf("missing trusted anchor")
	}
	if t.TargetHeight <= 0 || c.Headers[0].Height != t.TargetHeight || c.Headers[len(c.Headers)-1].Height != t.Anchor.Height {
		return fmt.Errorf("requested history range missing")
	}
	matched := false
	for i, h := range c.Headers {
		if err := h.ValidateBasic(); err != nil {
			return fmt.Errorf("header: %w", err)
		}
		if h.ChainID != t.ChainID {
			return fmt.Errorf("chain ID mismatch")
		}
		if !bytes.Equal(h.ValidatorsHash, t.Validators.Hash()) || !bytes.Equal(h.NextValidatorsHash, t.Validators.Hash()) {
			return fmt.Errorf("validator transition unsupported")
		}
		data := cmt.Data{Txs: c.Data[i].Txs}
		if !bytes.Equal(h.DataHash, data.Hash()) {
			return fmt.Errorf("Tx DataHash mismatch at %d", h.Height)
		}
		if i > 0 && (h.Height != c.Headers[i-1].Height+1 || !bytes.Equal(h.LastBlockID.Hash, c.Headers[i-1].Hash()) || !h.Time.After(c.Headers[i-1].Time)) {
			return fmt.Errorf("broken header chain at %d", h.Height)
		}
		if pinned && h.Height == t.Anchor.Height {
			if !bytes.Equal(h.Hash(), t.Anchor.Hash) {
				return fmt.Errorf("trusted anchor mismatch at %d", h.Height)
			}
			matched = true
		}
	}
	last := c.Headers[len(c.Headers)-1]
	if pinned && (!matched || last.Height != t.Anchor.Height) {
		return fmt.Errorf("range must end at trusted anchor")
	}
	if err := c.Commit.ValidateBasic(); err != nil {
		return fmt.Errorf("commit structure: %w", err)
	}
	if !bytes.Equal(last.Hash(), c.Commit.BlockID.Hash) {
		return fmt.Errorf("terminal commit hash mismatch")
	}
	if err := t.Validators.VerifyCommit(t.ChainID, c.Commit.BlockID, last.Height, c.Commit); err != nil {
		return fmt.Errorf("commit quorum/signature: %w", err)
	}
	return nil
}

func clone(c candidate) candidate {
	b, err := cmtjson.Marshal(c)
	must(err)
	var out candidate
	must(cmtjson.Unmarshal(b, &out))
	return out
}
func repair(c *candidate) {
	for i := range c.Headers {
		d := cmt.Data{Txs: c.Data[i].Txs}
		c.Headers[i].DataHash = d.Hash()
		if i > 0 {
			c.Headers[i].LastBlockID.Hash = c.Headers[i-1].Hash()
		}
	}
}
func sign(c *candidate, t trust, keys map[string]cmted.PrivKey, count int) {
	h := c.Headers[len(c.Headers)-1]
	id := cmt.BlockID{Hash: h.Hash(), PartSetHeader: cmt.PartSetHeader{Total: 1, Hash: bytes.Repeat([]byte{7}, 32)}}
	commit := &cmt.Commit{Height: h.Height, Round: 0, BlockID: id}
	for i, v := range t.Validators.Validators {
		if i >= count {
			commit.Signatures = append(commit.Signatures, cmt.NewCommitSigAbsent())
			continue
		}
		vote := &cmt.Vote{Type: cmtproto.PrecommitType, Height: h.Height, Round: 0, BlockID: id, Timestamp: h.Time.Add(time.Second), ValidatorAddress: v.Address, ValidatorIndex: int32(i)}
		sig, err := keys[string(v.Address)].Sign(cmt.VoteSignBytes(t.ChainID, vote.ToProto()))
		must(err)
		vote.Signature = sig
		commit.Signatures = append(commit.Signatures, vote.CommitSig())
	}
	c.Commit = commit
}
func synthetic() (candidate, trust, map[string]cmted.PrivKey) {
	keys := map[string]cmted.PrivKey{}
	vals := []*cmt.Validator{}
	for i := 0; i < 4; i++ {
		k := cmted.GenPrivKey()
		keys[string(k.PubKey().Address())] = k
		vals = append(vals, cmt.NewValidator(k.PubKey(), 10))
	}
	t := trust{ChainID: "isolated-tamper-fixture", Validators: cmt.NewValidatorSet(vals), TargetHeight: 1}
	c := candidate{}
	for i := 0; i < 3; i++ {
		h := cmt.Header{Version: cmtversion.Consensus{Block: version.BlockProtocol}, ChainID: t.ChainID, Height: int64(i + 1), Time: time.Unix(1800000000+int64(i*6), 0).UTC(), ValidatorsHash: t.Validators.Hash(), NextValidatorsHash: t.Validators.Hash(), ProposerAddress: t.Validators.Validators[0].Address}
		if i > 0 {
			h.LastBlockID = cmt.BlockID{PartSetHeader: cmt.PartSetHeader{Total: 1, Hash: bytes.Repeat([]byte{7}, 32)}}
		}
		c.Headers = append(c.Headers, h)
		c.Data = append(c.Data, cmt.Data{Txs: cmt.Txs{cmt.Tx(fmt.Sprintf("synthetic-test-payload-%d", i))}})
	}
	repair(&c)
	t.Anchor = anchor{Height: 3, Hash: append([]byte(nil), c.Headers[2].Hash()...)}
	sign(&c, t, keys, 3)
	return c, t, keys
}
func realEvidence(file string) (candidate, trust, string) {
	b, err := os.ReadFile(file)
	must(err)
	var e struct {
		ChainID      string            `json:"chainId"`
		AnchorHeight int64             `json:"anchorHeight,string"`
		TargetHeight int64             `json:"targetHeight,string"`
		AnchorHash   string            `json:"anchorHash"`
		Blocks       []json.RawMessage `json:"blocks"`
		Commit       json.RawMessage   `json:"commit"`
		Validators   json.RawMessage   `json:"validators"`
	}
	must(json.Unmarshal(b, &e))
	c := candidate{}
	for _, raw := range e.Blocks {
		var r core.ResultBlock
		must(cmtjson.Unmarshal(raw, &r))
		if r.Block == nil || !bytes.Equal(r.Block.Hash(), r.BlockID.Hash) {
			panic("invalid saved block hash")
		}
		c.Headers = append(c.Headers, r.Block.Header)
		c.Data = append(c.Data, r.Block.Data)
	}
	var cm core.ResultCommit
	must(cmtjson.Unmarshal(e.Commit, &cm))
	c.Commit = cm.SignedHeader.Commit
	var vs core.ResultValidators
	must(cmtjson.Unmarshal(e.Validators, &vs))
	if vs.Total != len(vs.Validators) || vs.BlockHeight != e.AnchorHeight {
		panic("validator set incomplete")
	}
	hash, err := hex.DecodeString(e.AnchorHash)
	must(err)
	digest := sha256.Sum256(b)
	return c, trust{e.ChainID, cmt.NewValidatorSet(vs.Validators), anchor{e.AnchorHeight, hash}, e.TargetHeight}, hex.EncodeToString(digest[:])
}

type outcome struct {
	Accepted      bool    `json:"accepted"`
	AcceptedCount int     `json:"acceptedCount"`
	RejectedCount int     `json:"rejectedCount"`
	Reason        string  `json:"reason,omitempty"`
	AvgMS         float64 `json:"avgMs"`
	P95MS         float64 `json:"p95Ms"`
}
type row struct {
	Name     string             `json:"name"`
	Evidence string             `json:"evidence"`
	Modes    map[string]outcome `json:"modes"`
}

func measure(c candidate, t trust, pinned bool, n int) outcome {
	times := make([]float64, n)
	result := outcome{}
	for i := range times {
		start := time.Now()
		err := verify(c, t, pinned)
		times[i] = float64(time.Since(start).Nanoseconds()) / 1e6
		result.Accepted = err == nil
		if err == nil {
			result.AcceptedCount++
		} else {
			result.RejectedCount++
		}
		if err != nil {
			result.Reason = err.Error()
		}
		result.AvgMS += times[i] / float64(n)
	}
	sort.Float64s(times)
	result.P95MS = times[(95*n+99)/100-1]
	return result
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	in := flag.String("evidence", "", "read-only collector evidence.json")
	out := flag.String("output", "", "result JSON file")
	repeats := flag.Int("repeats", 1000, "verification repetitions per case/mode")
	flag.Parse()
	if *in == "" || *out == "" || *repeats < 1 || *repeats > 10000 {
		fmt.Fprintln(os.Stderr, "--evidence, --output and --repeats 1..10000 required")
		os.Exit(2)
	}
	actual, at, digest := realEvidence(*in)
	must(verify(actual, at, true))
	rows := []row{}
	add := func(name, source string, c candidate, t trust, expected [3]bool) {
		r := row{Name: name, Evidence: source, Modes: map[string]outcome{}}
		for i, mode := range []string{"single_unpinned", "single_pinned", "crossref_pinned"} {
			v := measure(c, t, i > 0, *repeats)
			if v.Accepted != expected[i] || (expected[i] && v.AcceptedCount != *repeats) || (!expected[i] && v.RejectedCount != *repeats) {
				panic(fmt.Sprintf("unexpected %s/%s: %+v", name, mode, v))
			}
			r.Modes[mode] = v
		}
		rows = append(rows, r)
	}
	add("original", "real VPS blocks", actual, at, [3]bool{true, true, true})
	c := clone(actual)
	c.Data[0].Txs[0][0] ^= 1
	add("tx_byte_flip", "real copy", c, at, [3]bool{})
	c = clone(actual)
	c.Data[0].Txs = nil
	add("tx_delete", "real copy", c, at, [3]bool{})
	c = clone(actual)
	c.Data[0].Txs[0][0] ^= 1
	repair(&c)
	add("tx_rehashed_suffix_without_keys", "real copy", c, at, [3]bool{})
	c = clone(actual)
	c.Headers = append(c.Headers[:1], c.Headers[2:]...)
	c.Data = append(c.Data[:1], c.Data[2:]...)
	add("missing_intermediate_block", "real copy", c, at, [3]bool{})
	c = clone(actual)
	c.Headers[0].ChainID = "wrong-chain"
	add("wrong_chain", "real copy", c, at, [3]bool{})
	c = clone(actual)
	c.Commit.Signatures[0].Signature[0] ^= 1
	add("corrupted_commit_signature", "real copy", c, at, [3]bool{})
	s, st, keys := synthetic()
	add("synthetic_original", "ephemeral test keys; not SDK-valid transactions", s, st, [3]bool{true, true, true})
	c = clone(s)
	c.Data[0].Txs[0][0] ^= 1
	repair(&c)
	sign(&c, st, keys, 2)
	add("rehashed_2_of_4_commit", "synthetic", c, st, [3]bool{})
	sign(&c, st, keys, 3)
	add("rehashed_3_of_4_commit", "synthetic", c, st, [3]bool{true, false, false})
	result := map[string]any{"kind": "offline-header-tamper-audit", "createdAt": time.Now().UTC(), "evidenceSHA256": digest, "repetitionsPerCaseMode": *repeats, "rows": rows,
		"runtime": map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0)},
		"scope":   "Tx bytes/DataHash, contiguous fixed-validator-set headers and terminal CometBFT commit, pinned terminal hash. Crossref pinned and single pinned deliberately use identical verification logic.",
		"limits":  []string{"Not live malicious IBC traffic or real key compromise", "No SDK Tx validation or reexecution, appHash recomputation, full block/part-set validation or validator transition verification", "B witness trust comes from saved membership and pinned SSH, not an independently verified B ICS23 proof", "Synthetic payloads and commits model quorum control only; cannot show that existing full nodes reorg"}}
	b, err := json.MarshalIndent(result, "", "  ")
	must(err)
	must(os.WriteFile(*out, append(b, '\n'), 0600))
	fmt.Printf("%d cases x 3 modes x %d repetitions passed; %s\n", len(rows), *repeats, *out)
}
