package types

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/cosmos/gogoproto/proto"
)

const HistoryCollecting = "collecting"
const HistorySigning = "signing"
const HistoryCompleted = "completed"

func ValidateHistoryConfig(c HistoryConfig) error {
	if c.RequireAncestryProof && c.IntervalBlocks > MaxHistoryProofHeaders-2 {
		return fmt.Errorf("ancestry history interval exceeds proof window")
	}
	if c.SessionId == "" || len(c.SessionId) > 128 || len(c.Members) < 3 || len(c.Members) > 32 || c.IntervalBlocks < 2 || c.IntervalBlocks > 1000000 || c.PacketTimeoutSeconds < 30 || c.PacketTimeoutSeconds > 3600 {
		return fmt.Errorf("history requires session, 3..32 members, interval 2..1000000 blocks and timeout 30..3600 seconds")
	}
	local := false
	chains := map[string]bool{}
	last := ""
	for _, m := range c.Members {
		if m.DomainId == "" || len(m.DomainId) > 128 || m.DomainId <= last || m.ChainId == "" || len(m.ChainId) > 128 || chains[m.ChainId] || len(m.PublicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("history members must be uniquely sorted with distinct chains and Ed25519 keys")
		}
		local = local || m.DomainId == c.LocalDomainId
		last = m.DomainId
		chains[m.ChainId] = true
	}
	if !local {
		return fmt.Errorf("local history domain is not a participant")
	}
	return nil
}

func historyField(out []byte, v []byte) []byte {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(v)))
	out = append(out, length[:]...)
	return append(out, v...)
}
func historyNumber(out []byte, n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return append(out, b[:]...)
}

// Local scheduling parameters are excluded: membership and session identity are shared by all BCs.
func HistorySessionHash(c HistoryConfig) []byte {
	prefix := "crossref-history-session-v2"
	if c.RequireAncestryProof {
		prefix = "crossref-history-session-v3-ancestry"
	}
	b := historyField([]byte(prefix), []byte(c.SessionId))
	for _, m := range c.Members {
		b = historyField(b, []byte(m.DomainId))
		b = historyField(b, []byte(m.ChainId))
		b = historyField(b, m.PublicKey)
	}
	h := sha256.Sum256(b)
	return h[:]
}
func HistoryPreviousSignatureHash(previous HistoryRound, domain string) []byte {
	var signature []byte
	for _, s := range previous.Seals {
		if s.DomainId == domain {
			signature = s.Signature
			break
		}
	}
	h := sha256.Sum256(signature)
	return h[:]
}

// Sign all participants' confirmed blocks AND H(previous raw signature). The context
// prefix prevents replay across sessions, rounds, identities and protocol versions.
func HistorySignBytes(c HistoryConfig, r HistoryRound, previous HistoryRound, domain string) ([]byte, error) {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "sign_bytes")
	if err := ValidateHistoryConfig(c); err != nil {
		return nil, err
	}
	if r.Number == 0 || r.Number != previous.Number+1 || len(r.Parts) != len(c.Members) {
		return nil, fmt.Errorf("history round is not ready")
	}
	if previous.Number > 0 && (previous.Status != HistoryCompleted || len(previous.Seals) != len(c.Members)) {
		return nil, fmt.Errorf("missing previous completed signatures")
	}
	found := false
	prefix := "crossref-history-sign-v2"
	if c.RequireAncestryProof {
		prefix = "crossref-history-sign-v3-ancestry"
	}
	b := append([]byte(prefix), HistorySessionHash(c)...)
	b = historyField(b, []byte(domain))
	b = historyNumber(b, r.Number)
	b = append(b, HistoryPreviousSignatureHash(previous, domain)...)
	for i, m := range c.Members {
		p := r.Parts[i]
		if p.DomainId != m.DomainId || p.ChainId != m.ChainId || p.Height == 0 || len(p.BlockHash) != 32 || len(p.AppHash) != 32 {
			return nil, fmt.Errorf("invalid or unsorted history part")
		}
		if err := VerifyHistoryPartProof(c, r.Number, p, previous); err != nil {
			return nil, err
		}
		found = found || domain == m.DomainId
		b = historyField(b, []byte(p.DomainId))
		b = historyField(b, []byte(p.ChainId))
		b = historyNumber(b, p.Height)
		b = append(b, p.BlockHash...)
		b = append(b, p.AppHash...)
	}
	if !found {
		return nil, fmt.Errorf("non-participant signer")
	}
	return b, nil
}

func (s *HistoryState) member(domain string) (HistoryMember, error) {
	for _, m := range s.Config.Members {
		if m.DomainId == domain {
			return m, nil
		}
	}
	return HistoryMember{}, fmt.Errorf("unknown history participant")
}
func (s *HistoryState) queue(p HistoryPacketData) {
	for _, m := range s.Config.Members {
		if m.DomainId != s.Config.LocalDomainId {
			s.Outbox = append(s.Outbox, HistoryOutbox{Destination: m.DomainId, Packet: p})
		}
	}
}

// Called with parent block hash and post-parent app hash from the current header.
func (s *HistoryState) BeginHistory(height uint64, parentHash, parentAppHash []byte) (bool, error) {
	if !s.Config.Enabled {
		return false, nil
	}
	if err := ValidateHistoryConfig(s.Config); err != nil {
		return false, err
	}
	if height < 2 || len(parentHash) != 32 || len(parentAppHash) != 32 {
		return false, nil
	}
	if s.Current.Number > 0 && s.Current.Status != HistoryCompleted {
		return false, nil
	}
	if len(s.Outbox) > 0 {
		return false, nil
	}
	if s.Current.Number > 0 && (height <= s.Current.CompletedHeight || height-s.Current.CompletedHeight < s.Config.IntervalBlocks) {
		return false, nil
	}
	s.Previous = s.Current
	s.Current = HistoryRound{Number: s.Previous.Number + 1, OpenedHeight: height, Status: HistoryCollecting}
	m, _ := s.member(s.Config.LocalDomainId)
	p := HistoryPart{DomainId: m.DomainId, ChainId: m.ChainId, Height: height - 1, BlockHash: append([]byte(nil), parentHash...), AppHash: append([]byte(nil), parentAppHash...)}
	s.Current.Parts = append(s.Current.Parts, p)
	if !s.Config.RequireAncestryProof {
		s.queue(HistoryPacketData{SessionHash: HistorySessionHash(s.Config), Round: s.Current.Number, SourceDomainId: m.DomainId, Part: &p})
	}
	pending := s.PendingParts
	s.PendingParts = nil
	for _, packet := range pending {
		if err := s.ReceiveHistory(packet, height); err != nil {
			return false, err
		}
	}
	return true, s.advance(height)
}

func (s *HistoryState) validatePart(p HistoryPart) error {
	m, err := s.member(p.DomainId)
	if err != nil {
		return err
	}
	if p.ChainId != m.ChainId || p.Height == 0 || len(p.BlockHash) != 32 || len(p.AppHash) != 32 {
		return fmt.Errorf("invalid history part")
	}
	for _, seal := range s.Previous.Seals {
		if seal.DomainId == p.DomainId && p.Height <= seal.RecordedHeight {
			return fmt.Errorf("source height must follow previous history signature height")
		}
	}
	for _, old := range s.Previous.Parts {
		if old.DomainId == p.DomainId && p.Height <= old.Height {
			return fmt.Errorf("stale history block")
		}
	}
	return nil
}
func (s *HistoryState) ReceiveHistory(p HistoryPacketData, height uint64) error {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "receive_state")
	if !bytes.Equal(p.SessionHash, HistorySessionHash(s.Config)) || p.SourceDomainId == s.Config.LocalDomainId {
		return fmt.Errorf("wrong history session or source")
	}
	if _, err := s.member(p.SourceDomainId); err != nil {
		return err
	}
	if (p.Part == nil) == (p.Seal == nil) || p.Round == 0 {
		return fmt.Errorf("exactly one history payload required")
	}
	if p.Part != nil && p.Part.DomainId != p.SourceDomainId || p.Seal != nil && p.Seal.DomainId != p.SourceDomainId {
		return fmt.Errorf("history source mismatch")
	}
	// A fast BC may open n+1 before this BC completes n. Buffer one authenticated
	// part per member; never accept a future seal or advance past missing members.
	if p.Round == s.Current.Number+1 && p.Part != nil {
		member, _ := s.member(p.SourceDomainId)
		if p.Part.ChainId != member.ChainId || p.Part.Height == 0 || len(p.Part.BlockHash) != 32 || len(p.Part.AppHash) != 32 {
			return fmt.Errorf("invalid pending part")
		}
		if err := VerifyHistoryPartProof(s.Config, p.Round, *p.Part, s.Current); err != nil {
			return err
		}
		for _, old := range s.PendingParts {
			if old.SourceDomainId == p.SourceDomainId {
				if !proto.Equal(&old, &p) {
					return fmt.Errorf("conflicting pending part")
				}
				return nil
			}
		}
		if len(s.PendingParts) >= len(s.Config.Members)-1 {
			return fmt.Errorf("history pending bound")
		}
		s.PendingParts = append(s.PendingParts, p)
		sort.Slice(s.PendingParts, func(i, j int) bool { return s.PendingParts[i].SourceDomainId < s.PendingParts[j].SourceDomainId })
		return nil
	}
	if p.Round == s.Previous.Number && p.Round > 0 {
		if p.Part != nil {
			for _, old := range s.Previous.Parts {
				if proto.Equal(&old, p.Part) {
					return nil
				}
			}
		}
		if p.Seal != nil {
			for _, old := range s.Previous.Seals {
				if proto.Equal(&old, p.Seal) {
					return nil
				}
			}
		}
		return fmt.Errorf("conflicting historical packet")
	}
	if p.Round != s.Current.Number {
		return fmt.Errorf("unexpected history round")
	}
	if p.Part != nil {
		if err := s.validatePart(*p.Part); err != nil {
			return err
		}
		if err := VerifyHistoryPartProof(s.Config, p.Round, *p.Part, s.Previous); err != nil {
			return err
		}
		for _, old := range s.Current.Parts {
			if old.DomainId == p.SourceDomainId {
				if !proto.Equal(&old, p.Part) {
					return fmt.Errorf("conflicting history part")
				}
				return nil
			}
		}
		s.Current.Parts = append(s.Current.Parts, *p.Part)
		sort.Slice(s.Current.Parts, func(i, j int) bool { return s.Current.Parts[i].DomainId < s.Current.Parts[j].DomainId })
	} else {
		if len(p.Seal.Signature) != ed25519.SignatureSize || len(p.Seal.PreviousSignatureHash) != 32 || p.Seal.RecordedHeight == 0 {
			return fmt.Errorf("invalid history seal")
		}
		for _, old := range s.Current.Seals {
			if old.DomainId == p.SourceDomainId {
				if !proto.Equal(&old, p.Seal) {
					return fmt.Errorf("conflicting history seal")
				}
				return nil
			}
		}
		s.Current.Seals = append(s.Current.Seals, *p.Seal)
		sort.Slice(s.Current.Seals, func(i, j int) bool { return s.Current.Seals[i].DomainId < s.Current.Seals[j].DomainId })
	}
	return s.advance(height)
}
func (s *HistoryState) advance(height uint64) error {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "advance_state")
	if len(s.Current.Parts) != len(s.Config.Members) {
		return nil
	}
	if s.Config.RequireAncestryProof {
		for _, p := range s.Current.Parts {
			if p.DomainId == s.Config.LocalDomainId && len(p.AncestryProof) == 0 && s.Current.Status == HistoryCollecting {
				return nil
			}
			if err := VerifyHistoryPartProof(s.Config, s.Current.Number, p, s.Previous); err != nil {
				return err
			}
		}
	}
	for _, seal := range s.Current.Seals {
		b, err := HistorySignBytes(s.Config, s.Current, s.Previous, seal.DomainId)
		if err != nil {
			return err
		}
		m, _ := s.member(seal.DomainId)
		if !bytes.Equal(seal.PreviousSignatureHash, HistoryPreviousSignatureHash(s.Previous, seal.DomainId)) || !verifyHistorySignature(m.PublicKey, b, seal.Signature) {
			return fmt.Errorf("invalid history signature")
		}
		for _, p := range s.Current.Parts {
			if p.DomainId == seal.DomainId && seal.RecordedHeight <= p.Height {
				return fmt.Errorf("signature block must follow source block")
			}
		}
	}
	if s.Current.Status == HistoryCompleted {
		return nil
	}
	s.Current.Status = HistorySigning
	if len(s.Current.Seals) == len(s.Config.Members) {
		s.Current.Status = HistoryCompleted
		s.Current.CompletedHeight = height
	}
	return nil
}
func (s *HistoryState) SealHistory(round, height uint64, signature []byte) error {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "seal_state")
	if !s.Config.Enabled || s.Current.Number != round || s.Current.Status != HistorySigning {
		return fmt.Errorf("history round is not signable")
	}
	for _, old := range s.Current.Seals {
		if old.DomainId == s.Config.LocalDomainId {
			return fmt.Errorf("history round already signed")
		}
	}
	b, err := HistorySignBytes(s.Config, s.Current, s.Previous, s.Config.LocalDomainId)
	if err != nil {
		return err
	}
	m, _ := s.member(s.Config.LocalDomainId)
	if !verifyHistorySignature(m.PublicKey, b, signature) {
		return fmt.Errorf("invalid history signature")
	}
	seal := HistorySeal{DomainId: m.DomainId, Signature: append([]byte(nil), signature...), PreviousSignatureHash: HistoryPreviousSignatureHash(s.Previous, m.DomainId), RecordedHeight: height}
	s.Current.Seals = append(s.Current.Seals, seal)
	sort.Slice(s.Current.Seals, func(i, j int) bool { return s.Current.Seals[i].DomainId < s.Current.Seals[j].DomainId })
	if err := s.advance(height); err != nil {
		return err
	}
	s.queue(HistoryPacketData{SessionHash: HistorySessionHash(s.Config), Round: round, SourceDomainId: m.DomainId, Seal: &seal})
	return nil
}

func verifyHistorySignature(key ed25519.PublicKey, message, signature []byte) bool {
	defer telemetry.MeasureSince(telemetry.Now(), "crossref", "history", "signature_verify")
	return ed25519.Verify(key, message, signature)
}
