// history-signer keeps the participant's private key outside consensus execution.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/crossref/crossrefd/x/crossref/types"
)

type signedRound struct {
	Digest    string              `json:"digest"`
	Signature string              `json:"signature"`
	TxHash    string              `json:"tx_hash,omitempty"`
	Uncertain bool                `json:"uncertain"`
	Anchors   []types.HistoryPart `json:"anchors,omitempty"`
	Attempts  []txAttempt         `json:"attempts,omitempty"`
}
type journal map[string]signedRound
type options struct {
	inspect                                                                   bool
	binary, home, node, chain, from, backend, keyFile, session, journal, fees string
	runID, domain, timingFile                                                 string
	interval                                                                  time.Duration
	once                                                                      bool
	gas                                                                       uint64
	timings                                                                   *timingRecorder
}

func loadKey(name string) (ed25519.PrivateKey, error) {
	st, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("key file must be regular, not symlinked, mode 0600 or 0400")
	}
	bz, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(bz)
	if block == nil {
		return nil, fmt.Errorf("expected PKCS8 PEM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid PKCS8 key")
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("expected Ed25519 key")
	}
	return ed, nil
}
func persist(file string, j journal) error {
	bz, err := json.Marshal(j)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".history-journal-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(bz); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, file); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(file))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func signResponse(o options, key ed25519.PrivateKey, response types.QueryHistoryResponse, j journal) (string, signedRound, error) {
	s := response.State
	if err := checkPinnedHistory(o, key, s, j); err != nil {
		return "", signedRound{}, err
	}
	if !s.Config.Enabled || len(response.SignBytes) == 0 {
		return "", signedRound{}, nil
	}
	started := time.Now()
	b, err := types.HistorySignBytes(s.Config, s.Current, s.Previous, s.Config.LocalDomainId)
	o.recordTiming("signature.sign_bytes", "", s.Current.Number, started, err)
	if err != nil {
		return "", signedRound{}, err
	}
	if !bytes.Equal(b, response.SignBytes) {
		return "", signedRound{}, fmt.Errorf("RPC signing bytes differ from locally derived bytes")
	}
	digest := sha256.Sum256(b)
	id := o.session + "/" + s.Config.LocalDomainId + "/" + strconv.FormatUint(s.Current.Number, 10)
	if previous, found := j[id]; found {
		if previous.Digest != hex.EncodeToString(digest[:]) {
			return "", signedRound{}, fmt.Errorf("equivocation refused: round already signed with different blocks")
		}
		return id, previous, nil
	}
	started = time.Now()
	signature := ed25519.Sign(key, b)
	o.recordTiming("signature.sign", "", s.Current.Number, started, nil)
	return id, signedRound{Digest: hex.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(signature), Anchors: historyAnchors(s.Current.Parts)}, nil
}
func historyAnchors(parts []types.HistoryPart) []types.HistoryPart {
	copy := append([]types.HistoryPart(nil), parts...)
	for i := range copy {
		copy[i].AncestryProof = nil
	}
	return copy
}
func checkPinnedHistory(o options, key ed25519.PrivateKey, s types.HistoryState, j journal) error {
	if hex.EncodeToString(types.HistorySessionHash(s.Config)) != o.session {
		return fmt.Errorf("pinned history session mismatch")
	}
	matched := false
	for _, m := range s.Config.Members {
		if m.DomainId == s.Config.LocalDomainId {
			matched = m.ChainId == o.chain && bytes.Equal(m.PublicKey, key.Public().(ed25519.PublicKey))
		}
	}
	if !matched {
		return fmt.Errorf("local chain or participant public key mismatch")
	}
	if s.Config.RequireAncestryProof && s.Current.Number > 1 {
		id := o.session + "/" + s.Config.LocalDomainId + "/" + strconv.FormatUint(s.Current.Number-1, 10)
		old, ok := j[id]
		a, _ := json.Marshal(historyAnchors(s.Previous.Parts))
		b, _ := json.Marshal(old.Anchors)
		if !ok || s.Previous.Number+1 != s.Current.Number || s.Previous.Status != types.HistoryCompleted || !bytes.Equal(a, b) {
			return fmt.Errorf("previous anchors missing or differ from persistent signer journal")
		}
		matched := false
		for _, seal := range s.Previous.Seals {
			if seal.DomainId == s.Config.LocalDomainId {
				matched = base64.StdEncoding.EncodeToString(seal.Signature) == old.Signature
			}
		}
		if !matched {
			return fmt.Errorf("previous local signature differs from journal")
		}
	}
	return nil
}
func command(ctx context.Context, o options, args ...string) (result []byte, err error) {
	started := time.Now()
	label := ""
	if len(args) > 0 {
		label = args[0]
	}
	if len(args) > 1 {
		label += "/" + args[1]
	}
	defer func() { o.recordTiming("cli.command", label, 0, started, err) }()
	child, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	args = append(args, "--home", o.home, "--node", o.node, "--output", "json")
	// Do not echo stderr: keyring tools may include sensitive operational details.
	cmd := exec.CommandContext(child, o.binary, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("crossrefd command failed or timed out")
	}
	if out.Len() > 16<<20 {
		return nil, fmt.Errorf("oversized CLI response")
	}
	return out.Bytes(), nil
}
func step(ctx context.Context, o options, key ed25519.PrivateKey, j journal) error {
	bz, err := command(ctx, o, "query", "crossref", "history")
	if err != nil {
		return err
	}
	var response types.QueryHistoryResponse
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	started := time.Now()
	if err = cdc.UnmarshalJSON(bz, &response); err != nil {
		o.recordTiming("history.decode", "", 0, started, err)
		return fmt.Errorf("invalid history query response")
	}
	o.recordTiming("history.decode", "", response.State.Current.Number, started, nil)
	started = time.Now()
	if err = checkPinnedHistory(o, key, response.State, j); err != nil {
		o.recordTiming("history.pinned_check", "", response.State.Current.Number, started, err)
		return err
	}
	o.recordTiming("history.pinned_check", "", response.State.Current.Number, started, nil)
	// Reconcile even if the proof/signature is already visible or the round advanced.
	started = time.Now()
	if err = reconcileTransactions(ctx, o, j); err != nil {
		o.recordTiming("transaction.reconcile", "", response.State.Current.Number, started, err)
		return err
	}
	o.recordTiming("transaction.reconcile", "", response.State.Current.Number, started, nil)
	if response.State.Config.Enabled && response.State.Config.RequireAncestryProof {
		for _, p := range response.State.Current.Parts {
			if p.DomainId == response.State.Config.LocalDomainId && len(p.AncestryProof) == 0 {
				proof, err := collectAncestry(ctx, o, p, types.HistoryAnchor(response.State.Previous, p.DomainId), response.State.Current.Number)
				if err != nil {
					return err
				}
				digest := sha256.Sum256(proof)
				id := o.session + "/proof/" + p.DomainId + "/" + strconv.FormatUint(response.State.Current.Number, 10)
				record := signedRound{Digest: hex.EncodeToString(digest[:])}
				if old, ok := j[id]; ok {
					if old.Digest != record.Digest {
						return fmt.Errorf("proof changed since prior submission")
					}
					record = old
				}
				// File input avoids OS argument-size limits for long full-header proofs.
				file, err := os.CreateTemp(filepath.Dir(o.journal), ".ancestry-proof-")
				if err != nil {
					return err
				}
				defer os.Remove(file.Name())
				_, writeErr := file.Write(proof)
				closeErr := file.Close()
				if writeErr != nil {
					return writeErr
				}
				if closeErr != nil {
					return closeErr
				}
				return broadcastRecorded(ctx, o, key, j, id, record, "attach-history-proof", strconv.FormatUint(response.State.Current.Number, 10), "@"+file.Name())
			}
		}
	}
	started = time.Now()
	id, record, err := signResponse(o, key, response, j)
	o.recordTiming("history.sign_response", "", response.State.Current.Number, started, err)
	if err != nil || id == "" {
		return err
	}
	return broadcastRecorded(ctx, o, key, j, id, record, "sign-history-round", strconv.FormatUint(response.State.Current.Number, 10), record.Signature)
}
func run() error {
	var o options
	flag.StringVar(&o.binary, "binary", "crossrefd", "crossrefd executable")
	flag.StringVar(&o.home, "home", "", "fee payer keyring home")
	flag.StringVar(&o.node, "node", "tcp://127.0.0.1:26657", "trusted local RPC")
	flag.StringVar(&o.chain, "chain-id", "", "expected local chain ID")
	flag.StringVar(&o.from, "from", "", "dedicated fee payer account")
	flag.StringVar(&o.backend, "keyring-backend", "os", "keyring backend")
	flag.StringVar(&o.keyFile, "key-file", "", "private PKCS8 PEM file")
	flag.StringVar(&o.session, "session-hash", "", "pinned session SHA256 hex")
	flag.StringVar(&o.journal, "journal", "", "persistent signing journal")
	flag.StringVar(&o.runID, "run-id", "", "experiment run ID for timing records")
	flag.StringVar(&o.domain, "domain", "", "local domain for timing records")
	flag.StringVar(&o.timingFile, "timing-file", "", "append-only timing JSONL file (0600)")
	flag.StringVar(&o.fees, "fees", "", "explicit fee, e.g. 2000stake")
	flag.Uint64Var(&o.gas, "gas-limit", defaultHistoryGas, "initial transaction gas limit; confirmed out-of-gas retries are capped at 250000000")
	flag.DurationVar(&o.interval, "interval", 5*time.Second, "poll interval")
	flag.BoolVar(&o.once, "once", false, "one poll then exit")
	flag.BoolVar(&o.inspect, "inspect", false, "print public participant configuration and session hash; no signing")
	flag.Parse()
	if o.inspect {
		bz, err := command(context.Background(), o, "query", "crossref", "history")
		if err != nil {
			return err
		}
		var response types.QueryHistoryResponse
		if err := codec.NewProtoCodec(codectypes.NewInterfaceRegistry()).UnmarshalJSON(bz, &response); err != nil {
			return fmt.Errorf("invalid history query response")
		}
		if err := types.ValidateHistoryConfig(response.State.Config); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			SessionHash string              `json:"session_hash"`
			Config      types.HistoryConfig `json:"config"`
		}{hex.EncodeToString(types.HistorySessionHash(response.State.Config)), response.State.Config})
	}
	if o.home == "" || o.chain == "" || o.from == "" || o.keyFile == "" || o.journal == "" || o.fees == "" || len(o.session) != 64 || o.interval < time.Second {
		return fmt.Errorf("home, chain-id, from, key-file, session-hash, journal and fees are required")
	}
	if _, err := hex.DecodeString(o.session); err != nil {
		return fmt.Errorf("invalid session hash")
	}
	if o.gas == 0 || o.gas > maxHistoryGas {
		return fmt.Errorf("gas-limit must be 1..250000000")
	}
	key, err := loadKey(o.keyFile)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(o.journal), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(o.journal+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("signer journal is locked; verify no signer is running before removing a stale lock")
	}
	lock.Close()
	defer os.Remove(o.journal + ".lock")
	j := journal{}
	if bz, err := os.ReadFile(o.journal); err == nil {
		if err = json.Unmarshal(bz, &j); err != nil {
			return fmt.Errorf("invalid signing journal")
		}
		if j == nil {
			return fmt.Errorf("invalid null signing journal")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	o.timings, err = newTimingRecorder(o.timingFile, o.runID, o.domain)
	if err != nil {
		return err
	}
	defer o.timings.close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	for {
		err = step(ctx, o, key, j)
		if o.once || errors.Is(err, errAncestryLimit) || errors.Is(err, errSubmissionStopped) {
			return err
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(o.interval):
		}
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
