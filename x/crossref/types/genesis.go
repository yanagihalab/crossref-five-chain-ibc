package types

import (
	"fmt"

	host "github.com/cosmos/ibc-go/v11/modules/core/24-host"
)

// DefaultGenesis returns the default genesis state
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params: DefaultParams(),
		PortId: PortID}
}

// Validate performs basic genesis state validation returning an error upon any
// failure.
func (gs GenesisState) Validate() error {
	if err := ValidateHistoryGenesis(gs.HistoryState, gs.HistoryRounds); err != nil {
		return err
	}
	domains := map[string]bool{}
	for _, d := range gs.Domains {
		if d.DomainId == "" || d.ChainId == "" || domains[d.DomainId] {
			return fmt.Errorf("invalid or duplicate genesis domain")
		}
		domains[d.DomainId] = true
	}
	channels := map[string]bool{}
	for _, b := range gs.DomainChannels {
		key := b.PortId + "/" + b.ChannelId
		if !domains[b.LocalDomainId] || !domains[b.RemoteDomainId] || channels[key] {
			return fmt.Errorf("invalid or duplicate genesis domain channel")
		}
		if err := host.PortIdentifierValidator(b.PortId); err != nil {
			return err
		}
		if err := host.ChannelIdentifierValidator(b.ChannelId); err != nil {
			return err
		}
		channels[key] = true
	}
	if err := host.PortIdentifierValidator(gs.PortId); err != nil {
		return err
	}

	seenAcks := make(map[string]struct{}, len(gs.CrossReferenceAcks))
	for _, ack := range gs.CrossReferenceAcks {
		if err := host.PortIdentifierValidator(ack.PortId); err != nil {
			return fmt.Errorf("invalid cross reference ack port_id: %w", err)
		}
		if err := host.ChannelIdentifierValidator(ack.ChannelId); err != nil {
			return fmt.Errorf("invalid cross reference ack channel_id: %w", err)
		}
		if ack.PacketSequence == 0 {
			return fmt.Errorf("invalid cross reference ack packet_sequence: %d", ack.PacketSequence)
		}
		key := fmt.Sprintf("%s/%s/%020d", ack.PortId, ack.ChannelId, ack.PacketSequence)
		if _, found := seenAcks[key]; found {
			return fmt.Errorf("duplicate cross reference ack: %s", key)
		}
		seenAcks[key] = struct{}{}
	}

	seenTrustedStates := make(map[string]struct{}, len(gs.TrustedLightClientStates))
	for _, state := range gs.TrustedLightClientStates {
		if state.DomainId == "" || state.ChainId == "" {
			return fmt.Errorf("trusted light client state domain_id and chain_id are required")
		}
		if state.LatestHeight == 0 {
			return fmt.Errorf("trusted light client state latest_height is required for domain %s", state.DomainId)
		}
		if len(state.LatestBlockHash) == 0 || len(state.LatestValidatorSetHash) == 0 {
			return fmt.Errorf("trusted light client state hashes are required for domain %s", state.DomainId)
		}
		if _, found := seenTrustedStates[state.DomainId]; found {
			return fmt.Errorf("duplicate trusted light client state: %s", state.DomainId)
		}
		seenTrustedStates[state.DomainId] = struct{}{}
	}

	seenLightUpdates := make(map[string]struct{}, len(gs.LightClientUpdates))
	for _, update := range gs.LightClientUpdates {
		if update.DomainId == "" || update.ChainId == "" {
			return fmt.Errorf("light client update domain_id and chain_id are required")
		}
		if update.TargetHeight == 0 {
			return fmt.Errorf("light client update target_height is required for domain %s", update.DomainId)
		}
		if len(update.TargetBlockHash) == 0 || len(update.TargetValidatorSetHash) == 0 {
			return fmt.Errorf("light client update target hashes are required for domain %s height %d", update.DomainId, update.TargetHeight)
		}
		key := fmt.Sprintf("%s/%020d/%X", update.DomainId, update.TargetHeight, update.TargetBlockHash)
		if _, found := seenLightUpdates[key]; found {
			return fmt.Errorf("duplicate light client update: %s", key)
		}
		seenLightUpdates[key] = struct{}{}
	}

	return gs.Params.Validate()
}
