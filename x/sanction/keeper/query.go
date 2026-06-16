package keeper

import (
	"context"
	"encoding/hex"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/crossref/crossrefd/x/sanction/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type queryServer struct{ Keeper }

var _ types.QueryServer = queryServer{}

func NewQueryServerImpl(k Keeper) types.QueryServer { return queryServer{Keeper: k} }
func (q queryServer) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	p, err := q.Keeper.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryParamsResponse{Params: p}, nil
}
func (q queryServer) Agent(ctx context.Context, req *types.QueryAgentRequest) (*types.QueryAgentResponse, error) {
	v, err := q.Keeper.Agents.Get(ctx, req.AgentId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryAgentResponse{Agent: v}, nil
}
func (q queryServer) Agents(ctx context.Context, req *types.QueryAgentsRequest) (*types.QueryAgentsResponse, error) {
	vals, page, err := query.CollectionPaginate(ctx, q.Keeper.Agents, req.Pagination, func(_ string, v types.AgentInfo) (types.AgentInfo, error) { return v, nil })
	if err != nil {
		return nil, err
	}
	return &types.QueryAgentsResponse{Agents: vals, Pagination: page}, nil
}
func (q queryServer) RiskReport(ctx context.Context, req *types.QueryRiskReportRequest) (*types.QueryRiskReportResponse, error) {
	v, err := q.RiskReports.Get(ctx, req.ReportId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryRiskReportResponse{Report: v}, nil
}
func (q queryServer) SanctionCase(ctx context.Context, req *types.QuerySanctionCaseRequest) (*types.QuerySanctionCaseResponse, error) {
	v, err := q.SanctionCases.Get(ctx, req.CaseId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QuerySanctionCaseResponse{SanctionCase: v}, nil
}
func (q queryServer) SanctionVotes(ctx context.Context, req *types.QuerySanctionVotesRequest) (*types.QuerySanctionVotesResponse, error) {
	votes := []types.SanctionVote{}
	prefix := req.CaseId + "/"
	err := q.Keeper.SanctionVotes.Walk(ctx, nil, func(key string, v types.SanctionVote) (bool, error) {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			votes = append(votes, v)
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return &types.QuerySanctionVotesResponse{Votes: votes}, nil
}
func (q queryServer) ActiveTxSanction(ctx context.Context, req *types.QueryActiveTxSanctionRequest) (*types.QueryActiveTxSanctionResponse, error) {
	bz, err := hex.DecodeString(req.TxHash)
	if err != nil {
		return nil, err
	}
	v, err := q.ActiveTxSanctions.Get(ctx, txKey(bz))
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryActiveTxSanctionResponse{Sanction: v}, nil
}
func (q queryServer) FrozenAddress(ctx context.Context, req *types.QueryFrozenAddressRequest) (*types.QueryFrozenAddressResponse, error) {
	v, err := q.FrozenAddresses.Get(ctx, req.Address)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryFrozenAddressResponse{Record: v}, nil
}
func (q queryServer) ExecutionRecord(ctx context.Context, req *types.QueryExecutionRecordRequest) (*types.QueryExecutionRecordResponse, error) {
	v, err := q.ExecutionRecords.Get(ctx, req.CaseId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryExecutionRecordResponse{Record: v}, nil
}
