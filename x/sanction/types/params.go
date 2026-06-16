package types

import "fmt"

func DefaultParams() Params {
	return Params{WatchThreshold: 30, BlockThreshold: 70, FreezeThreshold: 80, RevertThreshold: 90, QuorumThreshold: 67, UnanimousRequiredForRevert: true, EvidenceTtlBlocks: 10000, VotingPeriodBlocks: 50, AcceptedRiskSources: []string{"chainalysis", "mock-risk-service"}, HighRiskCategories: []string{"scam", "fraud", "money_laundering", "sanctioned_entity"}, AllowedActions: []SanctionAction{SanctionAction_SANCTION_ACTION_WATCH, SanctionAction_SANCTION_ACTION_BLOCK_TX, SanctionAction_SANCTION_ACTION_FREEZE_ADDRESS, SanctionAction_SANCTION_ACTION_ESCROW_FUNDS, SanctionAction_SANCTION_ACTION_REVERT_TRANSFER}}
}
func (p Params) Validate() error {
	if p.WatchThreshold > 100 || p.BlockThreshold > 100 || p.FreezeThreshold > 100 || p.RevertThreshold > 100 || p.QuorumThreshold > 100 {
		return fmt.Errorf("thresholds must be percentages")
	}
	if p.WatchThreshold > p.BlockThreshold || p.BlockThreshold > p.FreezeThreshold || p.FreezeThreshold > p.RevertThreshold {
		return fmt.Errorf("thresholds must be ordered watch <= block <= freeze <= revert")
	}
	if p.QuorumThreshold == 0 {
		return fmt.Errorf("quorum threshold must be positive")
	}
	if p.VotingPeriodBlocks == 0 {
		return fmt.Errorf("voting period blocks must be positive")
	}
	return nil
}
func ActionName(action SanctionAction) string {
	switch action {
	case SanctionAction_SANCTION_ACTION_WATCH:
		return "watch"
	case SanctionAction_SANCTION_ACTION_BLOCK_TX:
		return "block_tx"
	case SanctionAction_SANCTION_ACTION_FREEZE_ADDRESS:
		return "freeze_address"
	case SanctionAction_SANCTION_ACTION_ESCROW_FUNDS:
		return "escrow_funds"
	case SanctionAction_SANCTION_ACTION_REVERT_TRANSFER:
		return "revert_transfer"
	default:
		return "unspecified"
	}
}
