package workspacerelease

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// TestClassifyRepositoryWorkspaceRelease_DecisionTable exercises every
// state classifyRepositoryWorkspaceRelease's own doc comment names — pure,
// no I/O, mirroring workspacereconcile's own identical
// TestClassifyReconciliation_DecisionTable style for the sibling job. This
// file is package workspacerelease (not _test), the same "internal test
// file for an unexported pure decision function" convention this session's
// own V5-13 work already established
// (internal/app/runtime/recovery_reaper_internal_test.go), since
// classifyRepositoryWorkspaceRelease and its three actions are deliberately
// unexported — this job's own decision core is not part of the package's
// public surface any more than workspacereconcile.ClassifyReconciliation's
// own three Decision constants are exported to be called on their own from
// outside handler.go, they are just exported there because that
// package chose to (a difference of style, not of what is proven).
func TestClassifyRepositoryWorkspaceRelease_DecisionTable(t *testing.T) {
	tests := []struct {
		name    string
		state   workspace.RepositoryWorkspaceState
		want    repositoryWorkspaceReleaseAction
		wantErr bool
	}{
		{"released skips", workspace.RepositoryWorkspaceReleased, repositoryWorkspaceReleaseActionSkip, false},
		{"ready releases", workspace.RepositoryWorkspaceReady, repositoryWorkspaceReleaseActionRelease, false},
		{"quarantined blocks", workspace.RepositoryWorkspaceQuarantined, repositoryWorkspaceReleaseActionBlocked, false},
		{"provisioning errors", workspace.RepositoryWorkspaceProvisioning, "", true},
		{"releasing errors", workspace.RepositoryWorkspaceReleasing, "", true},
		{"failed errors", workspace.RepositoryWorkspaceFailed, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := classifyRepositoryWorkspaceRelease(tt.state)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("classifyRepositoryWorkspaceRelease(%s) error = nil, want an error", tt.state)
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyRepositoryWorkspaceRelease(%s) unexpected error: %v", tt.state, err)
			}
			if got != tt.want {
				t.Fatalf("classifyRepositoryWorkspaceRelease(%s) = %s, want %s", tt.state, got, tt.want)
			}
		})
	}
}
