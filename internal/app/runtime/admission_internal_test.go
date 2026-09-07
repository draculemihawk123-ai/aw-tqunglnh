package runtime

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func scope(t *testing.T, repositoryID string, access workdomain.RepositoryAccess) workdomain.RepositoryScope {
	t.Helper()
	scope, err := workdomain.NewRepositoryScope("family-1", 1, project.RepositoryID(repositoryID), access, []string{"**"}, "test", "actor-1", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewRepositoryScope: %v", err)
	}
	return scope
}

func nodeRunWithScope(t *testing.T, effectiveScope []workdomain.RepositoryScope) runtimedomain.NodeRun {
	t.Helper()
	nodeRun, err := runtimedomain.NewNodeRun("node-run-1", "run-1", "implement", 1, 0, effectiveScope, "input-hash", "")
	if err != nil {
		t.Fatalf("NewNodeRun: %v", err)
	}
	return nodeRun
}

// TestBuildExecutionEnvelope_ExactlyOneReadWriteMountByDefault proves
// V5-08's own "immutable envelope với đúng một READ_WRITE mount mặc định
// và mounts read-only còn lại": each distinct repository in EffectiveScope
// becomes exactly one mount, WRITE-scoped repositories map to READ_WRITE
// and everything else to READ_ONLY — never more than the scope itself
// declares, never silently upgraded or downgraded.
func TestBuildExecutionEnvelope_ExactlyOneReadWriteMountByDefault(t *testing.T) {
	nodeRun := nodeRunWithScope(t, []workdomain.RepositoryScope{
		scope(t, "repo-1", workdomain.RepositoryWrite),
		scope(t, "repo-2", workdomain.RepositoryRead),
		scope(t, "repo-3", workdomain.RepositoryRead),
	})

	mounts := buildExecutionEnvelope(nodeRun)
	if len(mounts) != 3 {
		t.Fatalf("len(mounts) = %d, want 3", len(mounts))
	}

	readWriteCount := 0
	byRepository := make(map[string]ports.WorkspaceAccess, len(mounts))
	for _, mount := range mounts {
		byRepository[string(mount.RepositoryID)] = mount.Access
		if mount.Access == ports.WorkspaceReadWrite {
			readWriteCount++
		}
	}
	if readWriteCount != 1 {
		t.Fatalf("readWriteCount = %d, want exactly 1, mounts = %+v", readWriteCount, mounts)
	}
	if byRepository["repo-1"] != ports.WorkspaceReadWrite {
		t.Errorf("repo-1 access = %s, want READ_WRITE", byRepository["repo-1"])
	}
	if byRepository["repo-2"] != ports.WorkspaceReadOnly || byRepository["repo-3"] != ports.WorkspaceReadOnly {
		t.Errorf("repo-2/repo-3 access = %s/%s, want READ_ONLY/READ_ONLY", byRepository["repo-2"], byRepository["repo-3"])
	}
}

func TestBuildExecutionEnvelope_DeduplicatesRepeatedRepository(t *testing.T) {
	nodeRun := nodeRunWithScope(t, []workdomain.RepositoryScope{
		scope(t, "repo-1", workdomain.RepositoryWrite),
		scope(t, "repo-1", workdomain.RepositoryWrite),
	})
	mounts := buildExecutionEnvelope(nodeRun)
	if len(mounts) != 1 {
		t.Fatalf("len(mounts) = %d, want 1 (deduplicated)", len(mounts))
	}
}

func TestCheckMultiRepositoryWriteGrant(t *testing.T) {
	singleWrite := nodeRunWithScope(t, []workdomain.RepositoryScope{scope(t, "repo-1", workdomain.RepositoryWrite)})
	if !checkMultiRepositoryWriteGrant(singleWrite, resolvedExecutionProfileView{}) {
		t.Error("a single write-scoped repository must satisfy the check with no capability granted")
	}

	multiWrite := nodeRunWithScope(t, []workdomain.RepositoryScope{
		scope(t, "repo-1", workdomain.RepositoryWrite), scope(t, "repo-2", workdomain.RepositoryWrite),
	})
	if checkMultiRepositoryWriteGrant(multiWrite, resolvedExecutionProfileView{}) {
		t.Error("more than one write-scoped repository without the capability must fail the check")
	}
	granted := resolvedExecutionProfileView{AllowedCapabilities: []string{integrationMultiRepositoryWriteCapability}}
	if !checkMultiRepositoryWriteGrant(multiWrite, granted) {
		t.Error("more than one write-scoped repository WITH the capability granted must satisfy the check")
	}
}
