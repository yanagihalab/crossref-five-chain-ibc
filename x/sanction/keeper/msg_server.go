package keeper

import (
	"context"
	"cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/crossref/crossrefd/x/sanction/types"
)

type msgServer struct{ Keeper }

var _ types.MsgServer = msgServer{}

func NewMsgServerImpl(k Keeper) types.MsgServer { return msgServer{Keeper: k} }
func (m msgServer) RegisterAgent(ctx context.Context, msg *types.MsgRegisterAgent) (*types.MsgRegisterAgentResponse, error) {
	if err := m.requireAuthority(msg.Authority); err != nil {
		return nil, err
	}
	a := msg.Agent
	if a.AgentId == "" || a.SignerAddress == "" {
		return nil, errors.Wrap(types.ErrInvalidCase, "agent id and signer are required")
	}
	if _, err := m.addressCodec.StringToBytes(a.SignerAddress); err != nil {
		return nil, err
	}
	if found, err := m.Agents.Has(ctx, a.AgentId); err != nil {
		return nil, err
	} else if found {
		return nil, types.ErrDuplicateAgent
	}
	if found, err := m.AgentBySigners.Has(ctx, a.SignerAddress); err != nil {
		return nil, err
	} else if found {
		return nil, types.ErrDuplicateAgent
	}
	if !a.Active {
		a.Active = true
	}
	if err := m.Agents.Set(ctx, a.AgentId, a); err != nil {
		return nil, err
	}
	if err := m.AgentBySigners.Set(ctx, a.SignerAddress, a.AgentId); err != nil {
		return nil, err
	}
	return &types.MsgRegisterAgentResponse{}, nil
}
func (m msgServer) SubmitRiskReport(ctx context.Context, msg *types.MsgSubmitRiskReport) (*types.MsgSubmitRiskReportResponse, error) {
	if _, err := m.addressCodec.StringToBytes(msg.Submitter); err != nil {
		return nil, err
	}
	r := msg.Report
	if r.Submitter == "" {
		r.Submitter = msg.Submitter
	}
	if r.Submitter != msg.Submitter {
		return nil, types.ErrUnauthorized
	}
	if r.ReportId == "" || len(r.TxHash) == 0 || r.Source == "" {
		return nil, types.ErrRiskReportNotFound
	}
	if r.RiskScore > 100 {
		return nil, types.ErrInvalidRiskScore
	}
	if found, err := m.RiskReports.Has(ctx, r.ReportId); err != nil {
		return nil, err
	} else if found {
		return nil, types.ErrDuplicateCase
	}
	if err := m.RiskReports.Set(ctx, r.ReportId, r); err != nil {
		return nil, err
	}
	return &types.MsgSubmitRiskReportResponse{ReportId: r.ReportId}, nil
}
func (m msgServer) OpenSanctionCase(ctx context.Context, msg *types.MsgOpenSanctionCase) (*types.MsgOpenSanctionCaseResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.CaseId == "" || len(msg.ReportIds) == 0 {
		return nil, types.ErrInvalidCase
	}
	if found, err := m.SanctionCases.Has(ctx, msg.CaseId); err != nil {
		return nil, err
	} else if found {
		return nil, types.ErrDuplicateCase
	}
	if !actionAllowed(msg.RequestedAction, params) {
		return nil, types.ErrActionNotAllowed
	}
	reports := make([]types.RiskReport, 0, len(msg.ReportIds))
	for _, id := range msg.ReportIds {
		r, err := m.RiskReports.Get(ctx, id)
		if err != nil {
			return nil, types.ErrRiskReportNotFound
		}
		reports = append(reports, r)
	}
	expected := ComputePolicyAction(reports, params, msg.TargetType)
	if expected == types.SanctionAction_SANCTION_ACTION_UNSPECIFIED || expected != msg.RequestedAction {
		return nil, types.ErrActionNotAllowed
	}
	c := types.SanctionCase{CaseId: msg.CaseId, TxHash: msg.TxHash, TargetAddress: msg.TargetAddress, TargetType: msg.TargetType, RequestedAction: msg.RequestedAction, Status: types.CaseStatus_CASE_STATUS_PENDING, ReportIds: msg.ReportIds, OpenedHeight: uint64(sdkCtx.BlockHeight()), VotingDeadlineHeight: uint64(sdkCtx.BlockHeight()) + params.VotingPeriodBlocks, PolicyVersion: msg.PolicyVersion}
	c.DecisionHash = DecisionHash(c)
	if err := m.SanctionCases.Set(ctx, c.CaseId, c); err != nil {
		return nil, err
	}
	return &types.MsgOpenSanctionCaseResponse{CaseId: c.CaseId}, nil
}
func (m msgServer) SubmitSanctionVote(ctx context.Context, msg *types.MsgSubmitSanctionVote) (*types.MsgSubmitSanctionVoteResponse, error) {
	v := msg.Vote
	c, err := m.SanctionCases.Get(ctx, v.CaseId)
	if err != nil {
		return nil, types.ErrCaseNotFound
	}
	if c.Status != types.CaseStatus_CASE_STATUS_PENDING {
		return nil, types.ErrCaseNotPending
	}
	a, err := m.Agents.Get(ctx, v.AgentId)
	if err != nil {
		return nil, types.ErrAgentNotFound
	}
	if !a.Active {
		return nil, types.ErrAgentInactive
	}
	if a.SignerAddress != msg.Submitter {
		return nil, types.ErrUnauthorized
	}
	if found, err := m.SanctionVotes.Has(ctx, voteKey(v.CaseId, v.AgentId)); err != nil {
		return nil, err
	} else if found {
		return nil, types.ErrVoteAlreadySubmitted
	}
	if err := m.SanctionVotes.Set(ctx, voteKey(v.CaseId, v.AgentId), v); err != nil {
		return nil, err
	}
	approved, rejected, err := m.recomputeCaseStatus(ctx, &c)
	if err != nil {
		return nil, err
	}
	if err := m.SanctionCases.Set(ctx, c.CaseId, c); err != nil {
		return nil, err
	}
	return &types.MsgSubmitSanctionVoteResponse{Approved: approved, Rejected: rejected}, nil
}
func (m msgServer) ExecuteSanction(ctx context.Context, msg *types.MsgExecuteSanction) (*types.MsgExecuteSanctionResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	c, err := m.SanctionCases.Get(ctx, msg.CaseId)
	if err != nil {
		return nil, types.ErrCaseNotFound
	}
	if c.Status != types.CaseStatus_CASE_STATUS_APPROVED {
		return nil, types.ErrCaseNotPending
	}
	switch c.RequestedAction {
	case types.SanctionAction_SANCTION_ACTION_BLOCK_TX:
		if err := m.ActiveTxSanctions.Set(ctx, txKey(c.TxHash), types.ActiveSanction{CaseId: c.CaseId, TxHash: c.TxHash, TargetAddress: c.TargetAddress, Action: c.RequestedAction, ActivatedHeight: uint64(sdkCtx.BlockHeight())}); err != nil {
			return nil, err
		}
	case types.SanctionAction_SANCTION_ACTION_FREEZE_ADDRESS:
		if err := m.FrozenAddresses.Set(ctx, c.TargetAddress, types.FreezeRecord{Address: c.TargetAddress, CaseId: c.CaseId, FrozenHeight: uint64(sdkCtx.BlockHeight()), Reason: msg.ResultMessage}); err != nil {
			return nil, err
		}
	case types.SanctionAction_SANCTION_ACTION_ESCROW_FUNDS:
		if err := m.executeEscrow(ctx, c); err != nil {
			return nil, err
		}
	case types.SanctionAction_SANCTION_ACTION_REVERT_TRANSFER:
		if err := m.executeRevert(ctx, c); err != nil {
			return nil, err
		}
	}
	c.Status = types.CaseStatus_CASE_STATUS_EXECUTED
	c.ExecutedHeight = uint64(sdkCtx.BlockHeight())
	rec := types.ExecutionRecord{CaseId: c.CaseId, Action: c.RequestedAction, TxHash: c.TxHash, TargetAddress: c.TargetAddress, Executor: msg.Executor, ExecutedHeight: uint64(sdkCtx.BlockHeight()), ResultCode: "ok", ResultMessage: msg.ResultMessage}
	rec.StateChangeHash = stateChangeHash(rec)
	if err := m.ExecutionRecords.Set(ctx, rec.CaseId, rec); err != nil {
		return nil, err
	}
	if err := m.SanctionCases.Set(ctx, c.CaseId, c); err != nil {
		return nil, err
	}
	return &types.MsgExecuteSanctionResponse{Record: rec}, nil
}
func (m msgServer) RevokeSanction(ctx context.Context, msg *types.MsgRevokeSanction) (*types.MsgRevokeSanctionResponse, error) {
	if err := m.requireAuthority(msg.Authority); err != nil {
		return nil, err
	}
	c, err := m.SanctionCases.Get(ctx, msg.CaseId)
	if err != nil {
		return nil, types.ErrCaseNotFound
	}
	c.Status = types.CaseStatus_CASE_STATUS_REVOKED
	_ = m.ActiveTxSanctions.Remove(ctx, txKey(c.TxHash))
	if c.TargetAddress != "" {
		_ = m.FrozenAddresses.Remove(ctx, c.TargetAddress)
	}
	return &types.MsgRevokeSanctionResponse{}, m.SanctionCases.Set(ctx, c.CaseId, c)
}
func (m msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if err := m.requireAuthority(msg.Authority); err != nil {
		return nil, err
	}
	if err := msg.Params.Validate(); err != nil {
		return nil, err
	}
	return &types.MsgUpdateParamsResponse{}, m.Params.Set(ctx, msg.Params)
}
func (m msgServer) recomputeCaseStatus(ctx context.Context, c *types.SanctionCase) (bool, bool, error) {
	var active, approvals, rejects uint64
	if err := m.Agents.Walk(ctx, nil, func(_ string, a types.AgentInfo) (bool, error) {
		if a.Active {
			active++
		}
		return false, nil
	}); err != nil {
		return false, false, err
	}
	prefix := c.CaseId + "/"
	if err := m.SanctionVotes.Walk(ctx, nil, func(key string, v types.SanctionVote) (bool, error) {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			if v.Option == types.VoteOption_VOTE_OPTION_APPROVE {
				approvals++
			}
			if v.Option == types.VoteOption_VOTE_OPTION_REJECT {
				rejects++
			}
		}
		return false, nil
	}); err != nil {
		return false, false, err
	}
	params, err := m.Params.Get(ctx)
	if err != nil {
		return false, false, err
	}
	if roundedPercent(approvals, active) >= params.QuorumThreshold {
		c.Status = types.CaseStatus_CASE_STATUS_APPROVED
		return true, false, nil
	}
	if roundedPercent(rejects, active) > 100-params.QuorumThreshold {
		c.Status = types.CaseStatus_CASE_STATUS_REJECTED
		return false, true, nil
	}
	return false, false, nil
}
func roundedPercent(n, d uint64) uint32 {
	if d == 0 {
		return 0
	}
	return uint32((n*100 + d/2) / d)
}
func (m msgServer) requireAuthority(authority string) error {
	if authority != m.AuthorityString() {
		return types.ErrUnauthorized
	}
	return nil
}
