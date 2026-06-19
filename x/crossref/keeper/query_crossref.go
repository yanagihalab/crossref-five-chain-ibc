package keeper

import (
	"context"

	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/crossref/crossrefd/x/crossref/types"
)

func (q queryServer) Domain(ctx context.Context, req *types.QueryDomainRequest) (*types.QueryDomainResponse, error) {
	if req == nil || req.DomainId == "" {
		return nil, status.Error(codes.InvalidArgument, "domain_id is required")
	}
	domain, found, err := q.k.GetDomain(ctx, req.DomainId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !found {
		return nil, status.Error(codes.NotFound, "domain not found")
	}
	return &types.QueryDomainResponse{Domain: domain}, nil
}

func (q queryServer) Checkpoint(ctx context.Context, req *types.QueryCheckpointRequest) (*types.QueryCheckpointResponse, error) {
	if req == nil || req.DomainId == "" || req.Height == 0 {
		return nil, status.Error(codes.InvalidArgument, "domain_id and height are required")
	}
	checkpoint, found, err := q.k.GetCheckpoint(ctx, req.DomainId, req.Height)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !found {
		return nil, status.Error(codes.NotFound, "checkpoint not found")
	}
	return &types.QueryCheckpointResponse{Checkpoint: checkpoint}, nil
}

func (q queryServer) LatestCheckpoint(ctx context.Context, req *types.QueryLatestCheckpointRequest) (*types.QueryLatestCheckpointResponse, error) {
	if req == nil || req.DomainId == "" {
		return nil, status.Error(codes.InvalidArgument, "domain_id is required")
	}
	checkpoint, found, err := q.k.GetLatestCheckpoint(ctx, req.DomainId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !found {
		return nil, status.Error(codes.NotFound, "checkpoint not found")
	}
	return &types.QueryLatestCheckpointResponse{Checkpoint: checkpoint}, nil
}

func (q queryServer) CrossReference(ctx context.Context, req *types.QueryCrossReferenceRequest) (*types.QueryCrossReferenceResponse, error) {
	if req == nil || req.LocalDomainId == "" || req.RemoteDomainId == "" || req.RemoteHeight == 0 {
		return nil, status.Error(codes.InvalidArgument, "local_domain_id, remote_domain_id and remote_height are required")
	}
	reference, found, err := q.k.GetCrossReference(ctx, req.LocalDomainId, req.RemoteDomainId, req.RemoteHeight)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !found {
		return nil, status.Error(codes.NotFound, "cross reference not found")
	}
	return &types.QueryCrossReferenceResponse{CrossReference: reference}, nil
}

func (q queryServer) AccountabilityEvent(ctx context.Context, req *types.QueryAccountabilityEventRequest) (*types.QueryAccountabilityEventResponse, error) {
	if req == nil || req.EventId == "" {
		return nil, status.Error(codes.InvalidArgument, "event_id is required")
	}
	event, found, err := q.k.GetAccountabilityEvent(ctx, req.EventId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !found {
		return nil, status.Error(codes.NotFound, "accountability event not found")
	}
	return &types.QueryAccountabilityEventResponse{Event: event}, nil
}

func (q queryServer) AccountabilityEvents(ctx context.Context, req *types.QueryAccountabilityEventsRequest) (*types.QueryAccountabilityEventsResponse, error) {
	if req == nil {
		req = &types.QueryAccountabilityEventsRequest{}
	}
	events, err := q.k.ListAccountabilityEvents(ctx, req.DomainId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	paged, pageRes := paginateAccountabilityEvents(events, req.Pagination)
	return &types.QueryAccountabilityEventsResponse{Events: paged, Pagination: pageRes}, nil
}

func paginateAccountabilityEvents(events []types.AccountabilityEvent, pageReq *querytypes.PageRequest) ([]types.AccountabilityEvent, *querytypes.PageResponse) {
	if pageReq == nil {
		return events, &querytypes.PageResponse{Total: uint64(len(events))}
	}
	offset := int(pageReq.Offset)
	limit := int(pageReq.Limit)
	if offset > len(events) {
		offset = len(events)
	}
	if limit == 0 {
		limit = len(events) - offset
	}
	end := offset + limit
	if end > len(events) {
		end = len(events)
	}
	var nextKey []byte
	if end < len(events) {
		nextKey = []byte(events[end].EventId)
	}
	return events[offset:end], &querytypes.PageResponse{
		NextKey: nextKey,
		Total:   uint64(len(events)),
	}
}
