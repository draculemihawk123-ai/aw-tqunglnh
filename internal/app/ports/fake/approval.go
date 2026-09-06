package fake

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ApprovalRepository is an in-memory ports.ApprovalRepository — V4-09 gives
// this concern its first real behavior, mirroring WaitRepository's own
// "sqlite-free app-layer test" discipline.
type ApprovalRepository struct {
	requests      map[string]runtime.ApprovalRequest // by ID
	requestByNode map[string]string                  // NodeRunID -> ApprovalRequestID, UNIQUE(node_run_id)
}

var _ ports.ApprovalRepository = (*ApprovalRepository)(nil)

func (r *ApprovalRepository) clone() *ApprovalRepository {
	requests := make(map[string]runtime.ApprovalRequest, len(r.requests))
	for k, v := range r.requests {
		requests[k] = v
	}
	requestByNode := make(map[string]string, len(r.requestByNode))
	for k, v := range r.requestByNode {
		requestByNode[k] = v
	}
	return &ApprovalRepository{requests: requests, requestByNode: requestByNode}
}

func (r *ApprovalRepository) CreateApprovalRequest(_ context.Context, request runtime.ApprovalRequest) (runtime.ApprovalRequest, error) {
	key := string(request.ID)
	if _, exists := r.requests[key]; exists {
		return runtime.ApprovalRequest{}, fmt.Errorf("fake: %w: approval request %s", ports.ErrPersistenceAlreadyExists, key)
	}
	nodeRunKey := string(request.NodeRunID)
	if _, exists := r.requestByNode[nodeRunKey]; exists {
		return runtime.ApprovalRequest{}, fmt.Errorf("fake: %w: node run %s already has an approval request", ports.ErrPersistenceAlreadyExists, nodeRunKey)
	}
	if r.requests == nil {
		r.requests = map[string]runtime.ApprovalRequest{}
	}
	if r.requestByNode == nil {
		r.requestByNode = map[string]string{}
	}
	r.requests[key] = request
	r.requestByNode[nodeRunKey] = key
	return request, nil
}

func (r *ApprovalRepository) GetApprovalRequest(_ context.Context, id string) (runtime.ApprovalRequest, error) {
	request, ok := r.requests[id]
	if !ok {
		return runtime.ApprovalRequest{}, fmt.Errorf("fake: %w: approval request %s", ports.ErrPersistenceNotFound, id)
	}
	return request, nil
}

// ListApprovalRequestsForRun mirrors sqlite's ListApprovalRequestsForRun
// (V4-12B).
func (r *ApprovalRepository) ListApprovalRequestsForRun(_ context.Context, runID string) ([]runtime.ApprovalRequest, error) {
	var requests []runtime.ApprovalRequest
	for _, request := range r.requests {
		if string(request.RunID) == runID {
			requests = append(requests, request)
		}
	}
	return requests, nil
}

// TransitionApprovalRequest mirrors sqlite's identical fenced CAS — a
// stale caller (wrong ExpectedState/ExpectedVersion) gets
// ErrOptimisticConflict, never a silent overwrite.
func (r *ApprovalRepository) TransitionApprovalRequest(_ context.Context, req ports.TransitionApprovalRequestRequest) (runtime.ApprovalRequest, error) {
	request, ok := r.requests[req.ApprovalRequestID]
	if !ok {
		return runtime.ApprovalRequest{}, fmt.Errorf("fake: %w: approval request %s", ports.ErrPersistenceNotFound, req.ApprovalRequestID)
	}
	if request.State != req.ExpectedState || request.Version != req.ExpectedVersion {
		return runtime.ApprovalRequest{}, fmt.Errorf(
			"fake: %w: approval request %s expected %s@%d",
			ports.ErrOptimisticConflict, req.ApprovalRequestID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	request.State = req.NextState
	if req.DecidedBy != "" {
		request.DecidedBy = req.DecidedBy
	}
	if req.DecidedRole != "" {
		request.DecidedRole = req.DecidedRole
	}
	if req.DecidedOutcome != "" {
		request.DecidedOutcome = req.DecidedOutcome
	}
	if req.Reason != "" {
		request.Reason = req.Reason
	}
	if !req.DecidedAt.IsZero() {
		decidedAt := req.DecidedAt
		request.DecidedAt = &decidedAt
	}
	request.Version++
	r.requests[req.ApprovalRequestID] = request
	return request, nil
}
