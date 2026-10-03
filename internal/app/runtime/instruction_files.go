package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V9-10 (gap G9, docs/design/12-v9-harness-alignment.md): the instruction files
// a provider CLI loads by itself.
//
// Claude Code reads CLAUDE.md, Codex reads AGENTS.md, from the worktree they are
// started in. That text reaches the agent outside the instruction artifact aw
// renders — it is in no budget and, before V9-10, in no ContextSnapshot — so a
// long one brought back the problem of lecture 04 (an entry file that swamps the
// agent) where nothing could see it. When a node is scheduled, the files its
// provider declares are looked up in the worktrees of the repositories the work
// item may touch and pinned in the snapshot by repository, path, SHA-256, size
// and warning limit — by reference, never content.
//
// The lookup is real I/O, so it happens before the scheduling transaction
// (docs/architecture/04-go-core-spec.md §11.1: no filesystem in a transaction):
// InstructionFileInspector.Inspect reads the pins in a read-only transaction,
// releases it, and only then touches the filesystem.

// DefaultInstructionFileWarnBytes is the size above which an instruction file is
// recorded as oversized unless the operator configured another: about 16 KiB, a
// few hundred lines — "entry files are short maps, not manuals" (lecture 04's
// default, a teaching number rather than a verified limit, so it is a setting).
const DefaultInstructionFileWarnBytes int64 = 16 << 10

// maxHashedInstructionFileBytes bounds how much of a file is read to hash it.
const maxHashedInstructionFileBytes = 64 << 20

// InstructionFileInspector finds the instruction files of a node's provider.
type InstructionFileInspector struct {
	// Directories resolves a repository workspace's handle to its worktree.
	Directories ports.WorkspaceDirectoryResolver
	// Declared returns the file names (relative to the worktree root) the
	// provider loads by itself; nil or empty means it declares none. The
	// composition root builds it from the provider adapters, which own the
	// answer (claude: CLAUDE.md, codex: AGENTS.md).
	Declared func(providerKey string) []string
	// WarnBytes is the oversize limit recorded with each file; <= 0 takes
	// DefaultInstructionFileWarnBytes.
	WarnBytes int64
}

// instructionFileTarget is one worktree to look in, read from the database.
type instructionFileTarget struct {
	repositoryID string
	locator      string
}

// Inspect returns the instruction files present in the worktrees of the
// repositories the work item behind (runID, nodeRunID) may touch, for the
// provider of that node's AgentProfile. It returns nil for a node that is not an
// AGENT, a provider that declares no file, or worktrees holding none.
//
// Everything about the lookup is advisory: a worktree that cannot be resolved or
// a file that cannot be read contributes nothing rather than failing the
// scheduling of the node.
func (i *InstructionFileInspector) Inspect(ctx context.Context, uow ports.UnitOfWork, runID, nodeRunID string) ([]contextsnapshot.InstructionFileRef, error) {
	if i == nil || i.Directories == nil || i.Declared == nil {
		return nil, nil
	}
	var names []string
	var targets []instructionFileTarget
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, nodeRunID)
		if err != nil {
			return err
		}
		if string(nodeRun.RunID) != runID || nodeRun.State != "PENDING" {
			return nil
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
		if err != nil {
			return err
		}
		node, ok := findNode(version.Document(), nodeRun.NodeKey)
		if !ok || node.Type != workflow.NodeAgent || node.Agent == nil {
			return nil
		}
		profileVersion, err := tx.Definitions().LoadVersion(ctx, node.Agent.ProfileRef.VersionID)
		if err != nil {
			return err
		}
		profile, err := decodeCompiledAgentProfile(profileVersion.CompiledSnapshot())
		if err != nil {
			return err
		}
		names = i.Declared(profile.ProviderKey)
		if len(names) == 0 {
			return nil
		}
		item, err := tx.Work().GetWorkItem(ctx, string(run.WorkItemID))
		if err != nil {
			return err
		}
		scopes, err := readinesscheck.AdmissionScopes(ctx, tx, string(item.ID), string(item.FamilyID))
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, scope := range scopes {
			repositoryID := string(scope.RepositoryID())
			if seen[repositoryID] {
				continue
			}
			seen[repositoryID] = true
			rw, found, err := readinesscheck.LatestRepositoryWorkspace(ctx, tx, string(item.FamilyID), repositoryID)
			if err != nil {
				return err
			}
			if found && rw.Locator != "" {
				targets = append(targets, instructionFileTarget{repositoryID: repositoryID, locator: rw.Locator})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	warn := i.WarnBytes
	if warn <= 0 {
		warn = DefaultInstructionFileWarnBytes
	}
	var files []contextsnapshot.InstructionFileRef
	for _, target := range targets {
		handle, err := ports.NewWorkspaceHandle(target.locator)
		if err != nil {
			continue
		}
		directory, err := i.Directories.WorkingDirectory(ctx, handle)
		if err != nil {
			continue
		}
		for _, name := range names {
			ref, ok := hashInstructionFile(directory, target.repositoryID, name, warn)
			if ok {
				files = append(files, ref)
			}
		}
	}
	sort.Slice(files, func(a, b int) bool {
		if files[a].RepositoryID != files[b].RepositoryID {
			return files[a].RepositoryID < files[b].RepositoryID
		}
		return files[a].Path < files[b].Path
	})
	return files, nil
}

// hashInstructionFile describes name under directory when it is a regular file.
// A symlink is not followed: the CLI would load what it points at, which may lie
// outside the worktree, and recording a hash of a file elsewhere would say
// nothing about the worktree.
func hashInstructionFile(directory, repositoryID, name string, warnBytes int64) (contextsnapshot.InstructionFileRef, bool) {
	full := filepath.Join(directory, filepath.FromSlash(name))
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		return contextsnapshot.InstructionFileRef{}, false
	}
	file, err := os.Open(full)
	if err != nil {
		return contextsnapshot.InstructionFileRef{}, false
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, maxHashedInstructionFileBytes)); err != nil {
		return contextsnapshot.InstructionFileRef{}, false
	}
	return contextsnapshot.InstructionFileRef{
		RepositoryID: repositoryID, Path: name, SHA256: "sha256:" + hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes: info.Size(), WarnLimitBytes: warnBytes,
	}, true
}

// NewNodeSchedulingHandlerWithInstructionFiles is NewNodeSchedulingHandler with
// V9-10's instruction-file lookup: the files the node's provider loads by itself
// are pinned in the snapshot of every attempt this handler schedules. An
// inspector without a directory resolver or a Declared function finds nothing.
func NewNodeSchedulingHandlerWithInstructionFiles(
	uow ports.UnitOfWork, ids idsource.Source, provider ports.RuntimeExecutionConfigProvider, inspector *InstructionFileInspector,
) *NodeSchedulingHandler {
	handler := NewNodeSchedulingHandler(uow, ids, provider)
	handler.instructionFiles = inspector
	return handler
}
