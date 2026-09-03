package workspacereconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// TestClassifyReconciliation_DecisionTable exercises every cell of
// ClassifyReconciliation's own doc-comment table directly — pure, no I/O,
// mirroring worker.ReconcileMutatingAttempt's own equally pure decision
// core for the sibling automatic crash-recovery flow.
func TestClassifyReconciliation_DecisionTable(t *testing.T) {
	boom := errors.New("boom: provider failure")
	tests := []struct {
		name         string
		currentState workspace.RepositoryWorkspaceState
		inspectErr   error
		released     bool
		dirty        bool
		want         workspacereconcile.Decision
	}{
		{"ready inspect error blocks", workspace.RepositoryWorkspaceReady, boom, false, false, workspacereconcile.DecisionBlock},
		{"ready released blocks", workspace.RepositoryWorkspaceReady, nil, true, false, workspacereconcile.DecisionBlock},
		{"ready dirty blocks", workspace.RepositoryWorkspaceReady, nil, false, true, workspacereconcile.DecisionBlock},
		{"ready clean accepts", workspace.RepositoryWorkspaceReady, nil, false, false, workspacereconcile.DecisionAccept},
		{"quarantined inspect error recreates", workspace.RepositoryWorkspaceQuarantined, boom, false, false, workspacereconcile.DecisionRecreate},
		{"quarantined released recreates", workspace.RepositoryWorkspaceQuarantined, nil, true, false, workspacereconcile.DecisionRecreate},
		{"quarantined dirty (unexplainable) blocks", workspace.RepositoryWorkspaceQuarantined, nil, false, true, workspacereconcile.DecisionBlock},
		{"quarantined clean recreates", workspace.RepositoryWorkspaceQuarantined, nil, false, false, workspacereconcile.DecisionRecreate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workspacereconcile.ClassifyReconciliation(tt.currentState, tt.inspectErr, tt.released, tt.dirty)
			if got != tt.want {
				t.Fatalf("ClassifyReconciliation(%s, err=%v, released=%v, dirty=%v) = %s, want %s",
					tt.currentState, tt.inspectErr, tt.released, tt.dirty, got, tt.want)
			}
		})
	}
}

// stubUnreachableProvider fails the test outright if any of its methods is
// ever called — for Handle() tests that must never reach real I/O at all
// (a malformed/incomplete payload is rejected before any provider call).
type stubUnreachableProvider struct{ t *testing.T }

func (s stubUnreachableProvider) Provision(context.Context, ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	s.t.Fatal("Provision must not be called")
	return ports.WorkspaceHandle{}, nil
}
func (s stubUnreachableProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	s.t.Fatal("Inspect must not be called")
	return ports.WorkspaceInspection{}, nil
}
func (s stubUnreachableProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	s.t.Fatal("CaptureRevision must not be called")
	return workspace.Revision{}, nil
}
func (s stubUnreachableProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	s.t.Fatal("Diff must not be called")
	return ports.WorkspaceDiff{}, nil
}
func (s stubUnreachableProvider) Release(context.Context, ports.WorkspaceHandle) error {
	s.t.Fatal("Release must not be called")
	return nil
}

// stubUnreachableLifecycle fails the test outright if any of its methods
// is ever called.
type stubUnreachableLifecycle struct{ t *testing.T }

func (s stubUnreachableLifecycle) QuarantineRepositoryWorkspace(context.Context, ports.QuarantineRepositoryWorkspaceUpdate) error {
	s.t.Fatal("QuarantineRepositoryWorkspace must not be called")
	return nil
}
func (s stubUnreachableLifecycle) ReleaseRepositoryWorkspace(context.Context, ports.ReleaseRepositoryWorkspaceUpdate) error {
	s.t.Fatal("ReleaseRepositoryWorkspace must not be called")
	return nil
}
func (s stubUnreachableLifecycle) RecreateRepositoryWorkspace(context.Context, ports.RecreateRepositoryWorkspaceRequest) (workspace.RepositoryWorkspace, error) {
	s.t.Fatal("RecreateRepositoryWorkspace must not be called")
	return workspace.RepositoryWorkspace{}, nil
}

func TestHandle_MalformedPayload_RejectedBeforeAnyRealIO(t *testing.T) {
	handler := workspacereconcile.New(nil, nil, stubUnreachableProvider{t}, stubUnreachableLifecycle{t})
	err := handler.Handle(context.Background(), ports.DurableJob{ID: "job-1", Payload: json.RawMessage(`{not-json`)})
	if err == nil {
		t.Fatal("Handle() with malformed JSON payload error = nil, want an error")
	}
}

func TestHandle_IncompletePayload_RejectedBeforeAnyRealIO(t *testing.T) {
	handler := workspacereconcile.New(nil, nil, stubUnreachableProvider{t}, stubUnreachableLifecycle{t})
	payload, _ := json.Marshal(struct {
		RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	}{RepositoryWorkspaceID: "rw-1"})
	err := handler.Handle(context.Background(), ports.DurableJob{ID: "job-1", Payload: payload})
	if err == nil {
		t.Fatal("Handle() with incomplete payload error = nil, want an error")
	}
}

func TestExecuteWorkspaceReconciliation_RequiresAllDeps(t *testing.T) {
	err := workspacereconcile.ExecuteWorkspaceReconciliation(context.Background(), workspacereconcile.ExecuteWorkspaceReconciliationDeps{}, workspacereconcile.ExecuteWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: "rw-1", WorkspaceSetID: "ws-1", RepositoryID: "repo-1", Generation: 1, ExpectedVersion: 1,
	})
	if err == nil {
		t.Fatal("ExecuteWorkspaceReconciliation() with zero deps error = nil, want an error")
	}
}
