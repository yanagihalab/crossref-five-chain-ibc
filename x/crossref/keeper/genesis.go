package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"github.com/crossref/crossrefd/x/crossref/types"
)

// InitGenesis initializes the module's state from a provided genesis state.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	if err := genState.Validate(); err != nil {
		return err
	}
	if genState.HistoryState != nil {
		if err := k.HistoryState.Set(ctx, *genState.HistoryState); err != nil {
			return err
		}
	}
	for _, r := range genState.HistoryRounds {
		if err := k.HistoryRounds.Set(ctx, r.Number, r); err != nil {
			return err
		}
	}
	for _, d := range genState.Domains {
		if err := k.SetDomain(ctx, d); err != nil {
			return err
		}
	}
	for _, b := range genState.DomainChannels {
		if err := k.SetDomainChannel(ctx, b); err != nil {
			return err
		}
	}
	if err := k.Port.Set(ctx, genState.PortId); err != nil {
		return err
	}

	for _, ack := range genState.CrossReferenceAcks {
		if err := k.SetCrossReferenceAck(ctx, ack); err != nil {
			return err
		}
	}
	for _, state := range genState.TrustedLightClientStates {
		if err := k.SetTrustedLightClientState(ctx, state); err != nil {
			return err
		}
	}
	for _, update := range genState.LightClientUpdates {
		if err := k.RecordLightClientUpdate(ctx, update); err != nil {
			return err
		}
	}

	return k.Params.Set(ctx, genState.Params)
}

// ExportGenesis returns the module's exported genesis.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	var err error

	genesis := types.DefaultGenesis()
	state, historyErr := k.GetHistory(ctx)
	if historyErr != nil {
		return nil, historyErr
	}
	if state.Config.SessionId != "" {
		genesis.HistoryState = &state
	}
	if err := k.HistoryRounds.Walk(ctx, nil, func(_ uint64, r types.HistoryRound) (bool, error) {
		genesis.HistoryRounds = append(genesis.HistoryRounds, r)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Domains.Walk(ctx, nil, func(_ string, d types.DomainInfo) (bool, error) {
		genesis.Domains = append(genesis.Domains, d)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.DomainChannels.Walk(ctx, nil, func(_ string, b types.DomainChannel) (bool, error) {
		genesis.DomainChannels = append(genesis.DomainChannels, b)
		return false, nil
	}); err != nil {
		return nil, err
	}
	genesis.Params, err = k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	genesis.PortId, err = k.Port.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	if err := k.CrossReferenceAcks.Walk(ctx, nil, func(_ string, ack types.CrossReferenceAck) (bool, error) {
		genesis.CrossReferenceAcks = append(genesis.CrossReferenceAcks, ack)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.TrustedLightClients.Walk(ctx, nil, func(_ string, state types.TrustedLightClientState) (bool, error) {
		genesis.TrustedLightClientStates = append(genesis.TrustedLightClientStates, state)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.LightClientUpdates.Walk(ctx, nil, func(_ string, update types.LightClientUpdate) (bool, error) {
		genesis.LightClientUpdates = append(genesis.LightClientUpdates, update)
		return false, nil
	}); err != nil {
		return nil, err
	}

	return genesis, nil
}
