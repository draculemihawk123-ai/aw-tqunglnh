package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
)

// V9-10 (gap G9) — the instruction files a provider CLI loads by itself are
// pinned in the ContextSnapshot of the attempt that will run it: path, hash and
// size by reference, an oversize flag with the limit in force, and nothing at all
// (so the snapshot hash does not move) when there is no such file.

type fixedDirectory struct{ directory string }

func (f fixedDirectory) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return f.directory, nil
}

// scheduleWithInspector publishes the usual agent fixture, runs one scheduling
// job through a NodeSchedulingHandler that carries inspector, and returns the
// snapshot of the attempt it created.
func scheduleWithInspector(t *testing.T, inspector *runtime.InstructionFileInspector) contextsnapshot.Snapshot {
	t.Helper()
	ctx := context.Background()
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	payload, err := json.Marshal(runtime.ScheduleNodeRunJobPayload{RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1"})
	if err != nil {
		t.Fatal(err)
	}
	handler := runtime.NewNodeSchedulingHandlerWithInstructionFiles(uow, ids, fake.NewRuntimeExecutionConfigProvider(), inspector)
	if err := handler.Handle(ctx, ports.DurableJob{ID: "job-1", Kind: runtime.ScheduleNodeRunJobKind, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	attempts := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts()
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want exactly the one the job scheduled", len(attempts))
	}
	for id := range attempts {
		return snapshotOfAttempt(t, uow, id)
	}
	return contextsnapshot.Snapshot{}
}

func declaredFor(provider string, names ...string) func(string) []string {
	return func(providerKey string) []string {
		if providerKey == provider {
			return names
		}
		return nil
	}
}

func TestScheduling_PinsTheInstructionFilesTheProviderLoadsByItself(t *testing.T) {
	directory := t.TempDir()
	body := []byte(strings.Repeat("Use the project conventions.\n", 100)) // 2.9 KB
	if err := os.WriteFile(filepath.Join(directory, "CLAUDE.md"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "AGENTS.md"), []byte("not this provider's"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := scheduleWithInspector(t, &runtime.InstructionFileInspector{
		Directories: fixedDirectory{directory}, Declared: declaredFor("fake-provider", "CLAUDE.md", "MISSING.md"), WarnBytes: 1000,
	})

	sum := sha256.Sum256(body)
	want := contextsnapshot.InstructionFileRef{
		RepositoryID: "repo-1", Path: "CLAUDE.md", SHA256: "sha256:" + hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(body)), WarnLimitBytes: 1000,
	}
	if len(snapshot.RepositoryInstructionFiles) != 1 || snapshot.RepositoryInstructionFiles[0] != want {
		t.Fatalf("pinned files = %+v, want exactly CLAUDE.md %+v (AGENTS.md is not this provider's, MISSING.md is absent)", snapshot.RepositoryInstructionFiles, want)
	}
	if !snapshot.RepositoryInstructionFiles[0].Oversized() {
		t.Fatal("a 2.9 KB file against a 1000 byte limit is not reported oversized")
	}
	// The pin is part of the manifest hash: the same refs without it hash differently.
	without, err := contextsnapshot.NewSnapshot(snapshot.ID, snapshot.ProjectID, snapshot.WorkItemID, snapshot.AttemptID,
		snapshot.MessageRefs, snapshot.ResourceRefs, snapshot.EvidenceRefs, snapshot.Revisions, snapshot.CreatedAt,
		contextsnapshot.WithInstructionSchemaVersion(snapshot.InstructionSchemaVersion))
	if err != nil {
		t.Fatal(err)
	}
	if without.ManifestHash == snapshot.ManifestHash {
		t.Fatal("pinning an instruction file did not change the manifest hash")
	}
}

func TestScheduling_NoInstructionFile_LeavesTheSnapshotHashAsBefore(t *testing.T) {
	withEmptyWorktree := scheduleWithInspector(t, &runtime.InstructionFileInspector{
		Directories: fixedDirectory{t.TempDir()}, Declared: declaredFor("fake-provider", "CLAUDE.md"),
	})
	withoutInspector := scheduleWithInspector(t, nil)
	if len(withEmptyWorktree.RepositoryInstructionFiles) != 0 || len(withoutInspector.RepositoryInstructionFiles) != 0 {
		t.Fatalf("files pinned without any instruction file: %+v / %+v", withEmptyWorktree.RepositoryInstructionFiles, withoutInspector.RepositoryInstructionFiles)
	}
	// Rebuilt without the field, the snapshot hashes the same: absent, not empty.
	rebuilt, err := contextsnapshot.NewSnapshot(withEmptyWorktree.ID, withEmptyWorktree.ProjectID, withEmptyWorktree.WorkItemID, withEmptyWorktree.AttemptID,
		withEmptyWorktree.MessageRefs, withEmptyWorktree.ResourceRefs, withEmptyWorktree.EvidenceRefs, withEmptyWorktree.Revisions, withEmptyWorktree.CreatedAt,
		contextsnapshot.WithInstructionSchemaVersion(withEmptyWorktree.InstructionSchemaVersion))
	if err != nil || rebuilt.ManifestHash != withEmptyWorktree.ManifestHash {
		t.Fatalf("rebuilt hash = %s (%v), want %s", rebuilt.ManifestHash, err, withEmptyWorktree.ManifestHash)
	}
}

func TestScheduling_ASymlinkedInstructionFileIsNotFollowed(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := os.WriteFile(outside, []byte("outside the worktree"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "CLAUDE.md")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	snapshot := scheduleWithInspector(t, &runtime.InstructionFileInspector{
		Directories: fixedDirectory{directory}, Declared: declaredFor("fake-provider", "CLAUDE.md"),
	})
	if len(snapshot.RepositoryInstructionFiles) != 0 {
		t.Fatalf("a symlink out of the worktree was pinned: %+v", snapshot.RepositoryInstructionFiles)
	}
}

func TestScheduling_DefaultWarnLimit(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "CLAUDE.md"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := scheduleWithInspector(t, &runtime.InstructionFileInspector{
		Directories: fixedDirectory{directory}, Declared: declaredFor("fake-provider", "CLAUDE.md"),
	})
	if len(snapshot.RepositoryInstructionFiles) != 1 || snapshot.RepositoryInstructionFiles[0].WarnLimitBytes != runtime.DefaultInstructionFileWarnBytes ||
		snapshot.RepositoryInstructionFiles[0].Oversized() {
		t.Fatalf("pinned = %+v, want the default limit %d and not oversized", snapshot.RepositoryInstructionFiles, runtime.DefaultInstructionFileWarnBytes)
	}
}

func TestContextSnapshotToDetail_InstructionFilesCarryTheOversizeWarning(t *testing.T) {
	small := contextsnapshot.InstructionFileRef{RepositoryID: "repo-1", Path: "AGENTS.md", SHA256: "sha256:aa", SizeBytes: 100, WarnLimitBytes: 1000}
	large := contextsnapshot.InstructionFileRef{RepositoryID: "repo-1", Path: "CLAUDE.md", SHA256: "sha256:bb", SizeBytes: 5000, WarnLimitBytes: 1000}
	snapshot := scheduleWithInspector(t, nil)
	rebuilt, err := contextsnapshot.NewSnapshot(snapshot.ID, snapshot.ProjectID, snapshot.WorkItemID, snapshot.AttemptID,
		snapshot.MessageRefs, snapshot.ResourceRefs, snapshot.EvidenceRefs, snapshot.Revisions, snapshot.CreatedAt,
		contextsnapshot.WithRepositoryInstructionFiles([]contextsnapshot.InstructionFileRef{large, small}))
	if err != nil {
		t.Fatal(err)
	}
	detail := runtime.ContextSnapshotToDetail(rebuilt)
	if len(detail.RepositoryInstructionFiles) != 2 || detail.RepositoryInstructionFiles[0].FileName != "AGENTS.md" {
		t.Fatalf("files = %+v, want AGENTS.md then CLAUDE.md (sorted)", detail.RepositoryInstructionFiles)
	}
	if first := detail.RepositoryInstructionFiles[0]; first.Oversized || first.Warning != "" {
		t.Fatalf("the small file is flagged: %+v", first)
	}
	if second := detail.RepositoryInstructionFiles[1]; !second.Oversized || !strings.Contains(second.Warning, "5000 bytes, over the 1000") {
		t.Fatalf("the large file is not warned about: %+v", second)
	}
}
