package types

import (
	"bytes"
	"fmt"
	"github.com/cosmos/gogoproto/proto"
)

func ValidateHistoryGenesis(state *HistoryState, rounds []HistoryRound) error {
	if state == nil {
		if len(rounds) != 0 {
			return fmt.Errorf("history rounds require history state")
		}
		return nil
	}
	if err := ValidateHistoryConfig(state.Config); err != nil {
		return err
	}
	if len(state.LastBlockHash) != 0 && len(state.LastBlockHash) != 32 {
		return fmt.Errorf("invalid last history block hash")
	}
	if uint64(len(rounds)) != state.Current.Number {
		return fmt.Errorf("history archive must contain all rounds")
	}
	previous := HistoryRound{}
	for i, r := range rounds {
		if r.Status != HistoryCollecting && r.Status != HistorySigning && r.Status != HistoryCompleted {
			return fmt.Errorf("unknown history round status")
		}
		if r.Number != uint64(i+1) || r.OpenedHeight < 2 || len(r.Parts) > len(state.Config.Members) || len(r.Seals) > len(state.Config.Members) {
			return fmt.Errorf("invalid history archive")
		}
		s := HistoryState{Config: state.Config, Current: r, Previous: previous}
		last := ""
		localPart := false
		for _, p := range r.Parts {
			localPart = localPart || p.DomainId == state.Config.LocalDomainId
			if p.DomainId <= last {
				return fmt.Errorf("duplicate/unsorted history part")
			}
			if err := s.validatePart(p); err != nil {
				return err
			}
			if !(p.DomainId == state.Config.LocalDomainId && len(p.AncestryProof) == 0 && r.Status == HistoryCollecting) {
				if err := VerifyHistoryPartProof(state.Config, r.Number, p, previous); err != nil {
					return err
				}
			}
			last = p.DomainId
		}
		if !localPart {
			return fmt.Errorf("history round missing local part")
		}
		last = ""
		for _, seal := range r.Seals {
			if _, err := s.member(seal.DomainId); err != nil {
				return err
			}
			if seal.DomainId <= last || len(seal.Signature) != 64 || seal.RecordedHeight == 0 || !bytes.Equal(seal.PreviousSignatureHash, HistoryPreviousSignatureHash(previous, seal.DomainId)) {
				return fmt.Errorf("invalid archived signature")
			}
			last = seal.DomainId
		}
		if err := s.advance(r.CompletedHeight); err != nil {
			return err
		}
		if s.Current.Status != r.Status || (r.Status == HistoryCompleted) != (r.CompletedHeight > 0) {
			return fmt.Errorf("inconsistent history round status")
		}
		if i < len(rounds)-1 && r.Status != HistoryCompleted {
			return fmt.Errorf("unfinished historical round")
		}
		if i == len(rounds)-1 {
			if !proto.Equal(&state.Current, &r) || !proto.Equal(&state.Previous, &previous) {
				return fmt.Errorf("history state/archive mismatch")
			}
		}
		previous = r
	}
	if state.Current.Number == 0 && (state.Previous.Number != 0 || len(state.Outbox) != 0) {
		return fmt.Errorf("invalid initial history state")
	}
	seen := map[string]bool{}
	for _, p := range state.PendingParts {
		if _, err := state.member(p.SourceDomainId); err != nil {
			return err
		}
		if seen[p.SourceDomainId] || p.SourceDomainId == state.Config.LocalDomainId || p.Round != state.Current.Number+1 || p.Part == nil || p.Seal != nil || p.Part.DomainId != p.SourceDomainId || !bytes.Equal(p.SessionHash, HistorySessionHash(state.Config)) {
			return fmt.Errorf("invalid pending history packet")
		}
		seen[p.SourceDomainId] = true
		if err := VerifyHistoryPartProof(state.Config, p.Round, *p.Part, state.Current); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for _, o := range state.Outbox {
		if _, err := state.member(o.Destination); err != nil {
			return err
		}
		p := o.Packet
		kind := "part"
		if p.Seal != nil {
			kind = "seal"
		}
		key := fmt.Sprintf("%s/%d/%s", o.Destination, p.Round, kind)
		if seen[key] || o.Destination == state.Config.LocalDomainId || p.SourceDomainId != state.Config.LocalDomainId || p.Round != state.Current.Number || !bytes.Equal(p.SessionHash, HistorySessionHash(state.Config)) || (p.Part == nil) == (p.Seal == nil) {
			return fmt.Errorf("invalid history outbox")
		}
		matched := false
		if p.Part != nil {
			for _, part := range state.Current.Parts {
				matched = matched || proto.Equal(&part, p.Part)
			}
		}
		if p.Seal != nil {
			for _, seal := range state.Current.Seals {
				matched = matched || proto.Equal(&seal, p.Seal)
			}
		}
		if !matched {
			return fmt.Errorf("outbox differs from stored history")
		}
		if p.Part != nil {
			if err := VerifyHistoryPartProof(state.Config, p.Round, *p.Part, state.Previous); err != nil {
				return err
			}
		}
		seen[key] = true
	}
	return nil
}
