// This file holds the V9-01 (ADR-030) machinery that lets a read-only attempt
// — a CHECKER-role AGENT or a MACHINE_GATE — run AFTER a MAKER in the same
// run: the InputTree snapshot taken before spawn, its set-once persistence,
// and the strict "changed nothing since I started" comparison buildEvidence
// applies afterwards.
//
// Why a snapshot at all: a MAKER's changes stay uncommitted in the worktree
// until ReleaseSet/local commit (ADR-014), so any later read-only attempt
// sees a non-empty diff against the pinned commit that it did not make, and
// the pre-V9-01 rule ("the diff must be empty") failed it with
// SCOPE_VIOLATION every single time. "Read-only" instead means "nothing
// differs from the moment THIS attempt started": the content tree of the
// working tree at spawn time (InputTree) must equal the one measured after
// quiescence (OutputTree).
package runtime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ErrInputTreeMissing is returned (wrapped) when the InputTree an attempt
// recorded — or inherited from its predecessor — can no longer be used: the
// tree object was pruned by `git gc` (it has no ref), or the recorded set
// does not cover one of the attempt's mounts. ADR-030 makes this a technical
// failure of the attempt (FAILED, errorcode.CodePreconditionFailed, then the
// ordinary retry/FAILED path): the engine never re-snapshots to replace a
// recorded InputTree, because whatever the workspace holds now may include
// the very change the strict check exists to catch.
var ErrInputTreeMissing = errors.New("runtime: recorded input tree is no longer available")

// inputTreeUnavailableResult is the FAILED proposal both executors return for
// ErrInputTreeMissing. errorcode.CodePreconditionFailed rather than
// CodeExecutionFailed because nothing failed while RUNNING: the recorded
// input the strict check needs is gone, which is a precondition of the
// attempt (and not SCOPE_VIOLATION — the engine does not know that anything
// was changed, and must not claim it). The error-code set is closed
// (go-core-spec §18), and the attempt still goes through the ordinary
// retry/FAILED path; a retry inherits the same unavailable tree and fails
// the same way until the operator re-runs the node (ADR-030, ADR-033).
func inputTreeUnavailableResult() ports.NodeExecutionResult {
	return ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
		ErrorCode: errorcode.CodePreconditionFailed,
	}
}

// maxReportedChangedPaths caps how many changed paths a strict read-only
// violation message names; the rest are summarized as a count.
const maxReportedChangedPaths = 20

// treeSnapshotterOf returns the ports.WorkspaceTreeSnapshotter behind
// workspaces, or nil when the provider does not implement it (test doubles,
// future providers). nil leaves a strict attempt on the stricter pre-V9-01
// rule — the diff against the pinned commit must be empty — which is
// fail-closed, so a provider without tree support can never make a
// read-only attempt more permissive.
func treeSnapshotterOf(workspaces ports.WorkspaceProvider) ports.WorkspaceTreeSnapshotter {
	snapshotter, _ := workspaces.(ports.WorkspaceTreeSnapshotter)
	return snapshotter
}

// ensureInputTrees returns the InputTree of every one of mounts for the
// attempt req names, recording them first when the attempt has none yet
// (ADR-030 decisions 1-2). It runs after the mounts are resolved and BEFORE
// the process is spawned, and it keeps git I/O out of every transaction:
//
//  1. read the attempt in a short read-only transaction;
//  2. recorded already (a first run of this attempt that crashed after
//     recording, or a retry/recovery attempt that inherited its
//     predecessor's trees) -> reuse them, only verifying each tree object
//     still exists (ErrInputTreeMissing if not) — never re-snapshot;
//  3. otherwise snapshot every mount outside any transaction, then record
//     the result set-once in one short serialized-write transaction fenced
//     by the job lease (tx.Jobs().ValidateActiveJob, the same fencing the
//     event Sink applies to its own writes). If a concurrent writer
//     recorded first, its value wins and is returned.
//
// It returns (nil, nil) — meaning "no InputTree, fall back to the pre-V9-01
// diff-must-be-empty rule" — when the provider has no tree support or there
// are no mounts. A lost job lease is reported as ErrIndeterminateExecution
// (nothing was spawned; crash recovery owns the attempt).
func ensureInputTrees(
	ctx context.Context, uow ports.UnitOfWork, workspaces ports.WorkspaceProvider,
	req ports.NodeExecutionRequest, mounts []ports.AgentWorkspaceMount,
) (map[project.RepositoryID]string, error) {
	snapshotter := treeSnapshotterOf(workspaces)
	if snapshotter == nil || len(mounts) == 0 {
		return nil, nil
	}

	var recorded map[project.RepositoryID]string
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, req.AttemptID)
		if err != nil {
			return err
		}
		recorded = attempt.InputTrees
		return nil
	}); err != nil {
		return nil, fmt.Errorf("runtime: load recorded input trees for attempt %s: %w", req.AttemptID, err)
	}
	if len(recorded) > 0 {
		if err := verifyRecordedInputTrees(ctx, snapshotter, mounts, recorded); err != nil {
			return nil, err
		}
		return recorded, nil
	}

	snapshots := make(map[project.RepositoryID]string, len(mounts))
	for _, mount := range mounts {
		treeID, err := snapshotter.SnapshotTree(ctx, mount.Handle)
		if err != nil {
			return nil, fmt.Errorf("runtime: snapshot input tree of repository %s: %w", mount.RepositoryID, err)
		}
		snapshots[mount.RepositoryID] = treeID
	}

	var authoritative map[project.RepositoryID]string
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if err := tx.Jobs().ValidateActiveJob(ctx, req.JobLease, "ExecutionAttempt", req.AttemptID); err != nil {
			return err
		}
		if _, err := tx.Runtime().RecordAttemptInputTrees(ctx, req.AttemptID, snapshots); err != nil {
			return err
		}
		// Re-read rather than trust `applied`: a concurrent writer that
		// recorded first is the single source of truth.
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, req.AttemptID)
		if err != nil {
			return err
		}
		authoritative = attempt.InputTrees
		return nil
	}); err != nil {
		if errors.Is(err, ports.ErrJobLeaseLost) {
			return nil, fmt.Errorf("%w: job lease lost before the input tree of attempt %s was recorded: %v", ErrIndeterminateExecution, req.AttemptID, err)
		}
		return nil, fmt.Errorf("runtime: record input trees for attempt %s: %w", req.AttemptID, err)
	}
	for _, mount := range mounts {
		if authoritative[mount.RepositoryID] == "" {
			return nil, fmt.Errorf("%w: no tree recorded for repository %s", ErrInputTreeMissing, mount.RepositoryID)
		}
	}
	return authoritative, nil
}

// verifyRecordedInputTrees checks that recorded names a tree for every mount
// and that each tree object still exists (ports.WorkspaceTreeSnapshotter.
// DiffTrees(x, x) reports ErrTreeNotFound for a missing object and an empty
// diff otherwise).
func verifyRecordedInputTrees(
	ctx context.Context, snapshotter ports.WorkspaceTreeSnapshotter, mounts []ports.AgentWorkspaceMount,
	recorded map[project.RepositoryID]string,
) error {
	for _, mount := range mounts {
		treeID := recorded[mount.RepositoryID]
		if treeID == "" {
			return fmt.Errorf("%w: no tree recorded for repository %s", ErrInputTreeMissing, mount.RepositoryID)
		}
		if _, err := snapshotter.DiffTrees(ctx, mount.Handle, treeID, treeID); err != nil {
			if errors.Is(err, ports.ErrTreeNotFound) {
				return fmt.Errorf("%w: tree %s of repository %s: %v", ErrInputTreeMissing, treeID, mount.RepositoryID, err)
			}
			return fmt.Errorf("runtime: verify recorded input tree %s of repository %s: %w", treeID, mount.RepositoryID, err)
		}
	}
	return nil
}

// validateStrictlyReadOnlyTrees is buildEvidence's strictReadOnly check when
// the attempt has InputTrees (ADR-030 decision 3): after quiescence, snapshot
// each mount's OutputTree the same way the InputTree was taken; the attempt
// passes only if every OutputTree equals its InputTree. A difference wraps
// scopeguard.ErrScopeViolation (so every caller's existing
// errors.Is(err, scopeguard.ErrScopeViolation) handling covers it) and names
// the differing paths, capped at maxReportedChangedPaths.
//
// A missing InputTree object at this point (pruned between the pre-spawn
// check and now) is ErrInputTreeMissing, not a scope violation: the engine
// could not decide either way.
func validateStrictlyReadOnlyTrees(
	ctx context.Context, snapshotter ports.WorkspaceTreeSnapshotter, mounts []ports.AgentWorkspaceMount,
	inputTrees map[project.RepositoryID]string,
) error {
	for _, mount := range mounts {
		inputTree := inputTrees[mount.RepositoryID]
		if inputTree == "" {
			return fmt.Errorf("%w: no tree recorded for repository %s", ErrInputTreeMissing, mount.RepositoryID)
		}
		outputTree, err := snapshotter.SnapshotTree(ctx, mount.Handle)
		if err != nil {
			return fmt.Errorf("snapshot output tree of repository %s after quiescence: %w", mount.RepositoryID, err)
		}
		if outputTree == inputTree {
			continue
		}
		changed, err := snapshotter.DiffTrees(ctx, mount.Handle, inputTree, outputTree)
		if err != nil {
			if errors.Is(err, ports.ErrTreeNotFound) {
				return fmt.Errorf("%w: tree %s of repository %s: %v", ErrInputTreeMissing, inputTree, mount.RepositoryID, err)
			}
			return fmt.Errorf("diff input and output trees of repository %s: %w", mount.RepositoryID, err)
		}
		if len(changed) == 0 {
			continue
		}
		// V9-09: the same message as before, but typed — a
		// *scopeguard.ViolationsError (still errors.Is ErrScopeViolation) —
		// so the executor records the changed paths as the attempt's
		// operator-visible failureDetail (agentevents.RecordScopeViolation).
		violations := make([]scopeguard.Violation, 0, len(changed))
		for _, path := range changed {
			violations = append(violations, scopeguard.Violation{
				RepositoryID: mount.RepositoryID, Path: path,
				Reason: "changed since this attempt started despite the mount being read-only by design",
			})
		}
		return scopeguard.NewViolationsErrorWithSummary(violations,
			fmt.Sprintf("mount %s changed %d path(s) since this attempt started despite being read-only by design: %s",
				mount.RepositoryID, len(changed), formatChangedPaths(changed)))
	}
	return nil
}

// formatChangedPaths renders at most maxReportedChangedPaths quoted paths and
// a "(and N more)" tail for the rest.
func formatChangedPaths(paths []string) string {
	shown := paths
	if len(shown) > maxReportedChangedPaths {
		shown = shown[:maxReportedChangedPaths]
	}
	quoted := make([]string, len(shown))
	for i, path := range shown {
		quoted[i] = strconv.Quote(path)
	}
	text := strings.Join(quoted, ", ")
	if extra := len(paths) - len(shown); extra > 0 {
		text += fmt.Sprintf(" (and %d more)", extra)
	}
	return text
}

// inheritInputTreesTx copies the InputTrees of the attempt predecessorID onto
// next — the attempt that replaces it within the SAME NodeRun (technical
// retry, crash recovery; ADR-030 decision 2). It re-reads the predecessor
// inside the creating transaction rather than trusting an earlier read: the
// recovery paths classify the predecessor in a separate, earlier pass. No
// I/O beyond that read. A NEW NodeRun's first attempt must not call this.
func inheritInputTreesTx(ctx context.Context, tx ports.Tx, predecessorID string, next *runtimedomain.ExecutionAttempt) error {
	predecessor, err := tx.Runtime().GetExecutionAttempt(ctx, predecessorID)
	if err != nil {
		return fmt.Errorf("runtime: load predecessor attempt %s to inherit its input trees: %w", predecessorID, err)
	}
	next.InputTrees = predecessor.InheritedInputTrees()
	return nil
}
