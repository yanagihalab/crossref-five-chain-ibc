package keeper

import (
	"context"
	"github.com/crossref/crossrefd/x/sanction/types"
)

func (k Keeper) InitGenesis(ctx context.Context, genesis types.GenesisState) error {
	if err := genesis.Validate(); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, genesis.Params); err != nil {
		return err
	}
	for _, v := range genesis.Agents {
		if err := k.Agents.Set(ctx, v.AgentId, v); err != nil {
			return err
		}
		if err := k.AgentBySigners.Set(ctx, v.SignerAddress, v.AgentId); err != nil {
			return err
		}
	}
	for _, v := range genesis.RiskReports {
		if err := k.RiskReports.Set(ctx, v.ReportId, v); err != nil {
			return err
		}
	}
	for _, v := range genesis.SanctionCases {
		if err := k.SanctionCases.Set(ctx, v.CaseId, v); err != nil {
			return err
		}
	}
	for _, v := range genesis.SanctionVotes {
		if err := k.SanctionVotes.Set(ctx, voteKey(v.CaseId, v.AgentId), v); err != nil {
			return err
		}
	}
	for _, v := range genesis.ActiveSanctions {
		if err := k.ActiveTxSanctions.Set(ctx, txKey(v.TxHash), v); err != nil {
			return err
		}
	}
	for _, v := range genesis.FrozenAddresses {
		if err := k.FrozenAddresses.Set(ctx, v.Address, v); err != nil {
			return err
		}
	}
	for _, v := range genesis.ExecutionRecords {
		if err := k.ExecutionRecords.Set(ctx, v.CaseId, v); err != nil {
			return err
		}
	}
	return nil
}
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		p = types.DefaultParams()
	}
	gs := &types.GenesisState{Params: p}
	if err := k.Agents.Walk(ctx, nil, func(_ string, v types.AgentInfo) (bool, error) { gs.Agents = append(gs.Agents, v); return false, nil }); err != nil {
		return nil, err
	}
	if err := k.RiskReports.Walk(ctx, nil, func(_ string, v types.RiskReport) (bool, error) {
		gs.RiskReports = append(gs.RiskReports, v)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.SanctionCases.Walk(ctx, nil, func(_ string, v types.SanctionCase) (bool, error) {
		gs.SanctionCases = append(gs.SanctionCases, v)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.SanctionVotes.Walk(ctx, nil, func(_ string, v types.SanctionVote) (bool, error) {
		gs.SanctionVotes = append(gs.SanctionVotes, v)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.ActiveTxSanctions.Walk(ctx, nil, func(_ string, v types.ActiveSanction) (bool, error) {
		gs.ActiveSanctions = append(gs.ActiveSanctions, v)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.FrozenAddresses.Walk(ctx, nil, func(_ string, v types.FreezeRecord) (bool, error) {
		gs.FrozenAddresses = append(gs.FrozenAddresses, v)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.ExecutionRecords.Walk(ctx, nil, func(_ string, v types.ExecutionRecord) (bool, error) {
		gs.ExecutionRecords = append(gs.ExecutionRecords, v)
		return false, nil
	}); err != nil {
		return nil, err
	}
	return gs, nil
}
