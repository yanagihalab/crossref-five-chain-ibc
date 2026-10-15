package types

import (
	"bytes"
	"fmt"
	"math"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmt "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/telemetry"
)

// All intervening headers remain mandatory. These limits bound decoding,
// hashing, storage and packet size; they are not permission to skip links.
const MaxHistoryProofBytes = 384 * 1024
const MaxHistoryProofHeaders = 512
const MaxHistoryPacketBytes = MaxHistoryProofBytes + 16*1024

// This proves ancestry, not consensus or application execution. The endpoints
// are fixed by prior Crossref state and the authenticated source-chain packet.
func VerifyHistoryAncestry(part HistoryPart, previous *HistoryPart) error {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "ancestry_total")
	if len(part.AncestryProof) == 0 || len(part.AncestryProof) > MaxHistoryProofBytes || part.Height == 0 || part.Height >= math.MaxInt64 || len(part.BlockHash) != 32 || len(part.AppHash) != 32 || part.ChainId == "" {
		return fmt.Errorf("missing/oversized history ancestry proof or invalid height")
	}
	start := part.Height
	if previous != nil {
		if previous.DomainId != part.DomainId || previous.ChainId != part.ChainId || previous.Height == 0 || previous.Height >= part.Height || len(previous.BlockHash) != 32 || len(previous.AppHash) != 32 {
			return fmt.Errorf("invalid ancestry anchor")
		}
		start = previous.Height
	}
	count := part.Height - start + 2
	if count > MaxHistoryProofHeaders {
		return fmt.Errorf("history ancestry gap exceeds bounded proof window")
	}
	var proof HistoryAncestryProof
	decodeStart := telemetry.Now()
	decodeErr := proof.Unmarshal(part.AncestryProof)
	telemetry.MeasureSince(decodeStart, "crossref", "history", "ancestry_decode")
	if decodeErr != nil {
		return fmt.Errorf("invalid ancestry envelope: %w", decodeErr)
	}
	canonicalStart := telemetry.Now()
	canonical, err := proof.Marshal()
	telemetry.MeasureSince(canonicalStart, "crossref", "history", "ancestry_canonical")
	if err != nil || !bytes.Equal(canonical, part.AncestryProof) || uint64(len(proof.Headers)) != count {
		return fmt.Errorf("noncanonical or incomplete ancestry envelope")
	}
	var last *cmt.Header
	headersStart := telemetry.Now()
	defer telemetry.MeasureSince(headersStart, "crossref", "history", "ancestry_headers")
	for i, raw := range proof.Headers {
		var pb cmtproto.Header
		if err := pb.Unmarshal(raw); err != nil {
			return fmt.Errorf("invalid ancestry header")
		}
		encoded, err := pb.Marshal()
		if err != nil || !bytes.Equal(raw, encoded) {
			return fmt.Errorf("noncanonical ancestry header")
		}
		h, err := cmt.HeaderFromProto(&pb)
		if err != nil || h.ChainID != part.ChainId || uint64(h.Height) != start+uint64(i) || len(h.ValidatorsHash) != 32 || len(h.NextValidatorsHash) != 32 {
			return fmt.Errorf("ancestry header height/chain mismatch")
		}
		if last != nil && (!bytes.Equal(h.LastBlockID.Hash, last.Hash()) || !h.Time.After(last.Time)) {
			return fmt.Errorf("broken ancestry link")
		}
		if previous != nil {
			if uint64(h.Height) == start && !bytes.Equal(h.Hash(), previous.BlockHash) {
				return fmt.Errorf("ancestry anchor hash mismatch")
			}
			if uint64(h.Height) == start+1 && !bytes.Equal(h.AppHash, previous.AppHash) {
				return fmt.Errorf("ancestry anchor post-state mismatch")
			}
		}
		if uint64(h.Height) == part.Height && !bytes.Equal(h.Hash(), part.BlockHash) {
			return fmt.Errorf("ancestry terminal block hash mismatch")
		}
		if uint64(h.Height) == part.Height+1 && !bytes.Equal(h.AppHash, part.AppHash) {
			return fmt.Errorf("ancestry terminal post-state mismatch")
		}
		last = &h
	}
	return nil
}

func HistoryAnchor(previous HistoryRound, domain string) *HistoryPart {
	for i := range previous.Parts {
		if previous.Parts[i].DomainId == domain {
			return &previous.Parts[i]
		}
	}
	return nil
}

func VerifyHistoryPartProof(c HistoryConfig, round uint64, part HistoryPart, previous HistoryRound) error {
	if !c.RequireAncestryProof {
		return nil
	}
	anchor := HistoryAnchor(previous, part.DomainId)
	if round == 0 || (round > 1 && (previous.Number+1 != round || anchor == nil)) || (round == 1 && previous.Number != 0) {
		return fmt.Errorf("missing fixed history anchor")
	}
	return VerifyHistoryAncestry(part, anchor)
}

// A local part is fixed by BeginBlock but cannot leave the chain until a
// permissionless worker attaches a proof matching those exact endpoints.
func (s *HistoryState) AttachHistoryProof(round uint64, proof []byte, height uint64) error {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "attach_proof_state")
	if !s.Config.Enabled || !s.Config.RequireAncestryProof || round != s.Current.Number || s.Current.Status != HistoryCollecting {
		return fmt.Errorf("history proof is not attachable")
	}
	for i := range s.Current.Parts {
		p := &s.Current.Parts[i]
		if p.DomainId != s.Config.LocalDomainId {
			continue
		}
		if len(p.AncestryProof) > 0 {
			if bytes.Equal(p.AncestryProof, proof) {
				return nil
			}
			return fmt.Errorf("history proof already attached")
		}
		copy := *p
		copy.AncestryProof = append([]byte(nil), proof...)
		if err := VerifyHistoryPartProof(s.Config, round, copy, s.Previous); err != nil {
			return err
		}
		*p = copy
		s.queue(HistoryPacketData{SessionHash: HistorySessionHash(s.Config), Round: round, SourceDomainId: p.DomainId, Part: &copy})
		return s.advance(height)
	}
	return fmt.Errorf("local history part missing")
}
