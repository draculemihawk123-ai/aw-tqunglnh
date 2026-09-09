// This file holds AgentNodeExecutor's own two real-I/O staging phases —
// resolving real workspace resources before spawning a provider, and
// building the post-quiescence evidence bundle after one succeeds — kept
// out of agent_node_executor.go itself only to keep that file's own
// Execute/classify control flow readable.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// resolvedExecutionResources is resolveExecutionResources's own output —
// real WorkspaceHandle/WorkingDirectory per mount, any WriteLeaseGrant
// acquired for a WRITE-access mount, and whether any mount is WRITE at all
// (classify's own quiescence-severity check needs exactly this).
type resolvedExecutionResources struct {
	mounts           []ports.AgentWorkspaceMount
	sinkMounts       []agentevents.Mount
	writeLeaseGrants []ports.WriteLeaseGrant
	hasWriteMount    bool
	projectID        project.ProjectID
	// writeMounts carries exactly what V5-08C's own cancellation-reconciliation
	// path needs per WRITE mount (handleMutatingCancellation,
	// agent_node_executor_cancellation.go) — the real RepositoryWorkspaceID/
	// WorkspaceVersion/pinned revision this bridge already resolved once
	// during Phase 1 above, so that path never needs a second DB round trip
	// for the same rows.
	writeMounts []mutatingMountInfo
}

// mutatingMountInfo is one WRITE mount's own identity for reconciliation —
// see resolvedExecutionResources.writeMounts's own doc comment.
type mutatingMountInfo struct {
	repositoryWorkspaceID workspace.RepositoryWorkspaceID
	handle                ports.WorkspaceHandle
	pinnedRevision        string
	workspaceVersion      uint64
}

// gatheredMount is resolveExecutionResources's own Phase 1 output (real DB
// facts only, read inside one read-only transaction) — Phase 2 (real I/O:
// WorkingDirectory resolution, AcquireWriteLeases) runs entirely after
// that transaction has already closed.
type gatheredMount struct {
	repositoryID     project.RepositoryID
	locator          string
	workspaceID      workspace.RepositoryWorkspaceID
	workspaceVersion uint64
	access           ports.WorkspaceAccess
	vcsObjectID      string
	generation       uint64
}

// resolveExecutionResources resolves every one of request's own
// WorkspaceMounts (AssembleAgentExecutionRequest deliberately leaves
// Handle/WorkingDirectory unresolved — its own doc comment names this
// exact bridge as the caller that must) into a real WorkspaceHandle and
// working directory, and — for any mount with WRITE access — acquires a
// real WriteLeaseGrant fencing proof (execute.go's own package doc
// comment: "V5's own real executor is the first caller with a genuine
// reason to... call AcquireWriteLeases before Execute").
//
// A free function (V5-09: CommandNodeExecutor needs this EXACT same mount
// resolution/write-lease acquisition — a COMMAND node's own EffectiveScope
// resolves to real workspace mounts the identical way an AGENT node's
// does) rather than a method on *AgentNodeExecutor; e.resolveExecutionResources
// below is a thin, unchanged wrapper. request stays typed as
// ports.AgentExecutionRequest rather than a renamed/generalized shared
// type — it already carries exactly the two fields this function needs
// (WorkspaceMounts, Timeout), and CommandNodeExecutor constructs one of
// its own to pass in rather than this codebase inventing a second,
// differently-named struct with the identical shape.
func resolveExecutionResources(
	ctx context.Context, uow ports.UnitOfWork, workspaces ports.WorkspaceProvider, writeLeases ports.WriteLeaseManager,
	req ports.NodeExecutionRequest, request ports.AgentExecutionRequest,
) (resolvedExecutionResources, error) {
	var (
		gathered  []gatheredMount
		projectID project.ProjectID
	)
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
		if err != nil {
			return err
		}
		projectID = run.ProjectID
		if len(request.WorkspaceMounts) == 0 {
			return nil
		}
		workspaceSet, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, string(run.FamilyID))
		if err != nil {
			return err
		}
		for _, mount := range request.WorkspaceMounts {
			rw, err := tx.Work().GetRepositoryWorkspace(ctx, string(workspaceSet.ID), string(mount.RepositoryID), mount.WorkspaceGeneration)
			if err != nil {
				return fmt.Errorf("load repository workspace for %s generation %d: %w", mount.RepositoryID, mount.WorkspaceGeneration, err)
			}
			gathered = append(gathered, gatheredMount{
				repositoryID: mount.RepositoryID, locator: rw.Locator, workspaceID: rw.ID, workspaceVersion: rw.Version,
				access: mount.Access, vcsObjectID: mount.VCSObjectID, generation: mount.WorkspaceGeneration,
			})
		}
		return nil
	}); err != nil {
		return resolvedExecutionResources{}, err
	}

	resolved := resolvedExecutionResources{projectID: projectID}
	var writeTargets []ports.WorkspaceLeaseTarget
	for _, g := range gathered {
		handle, err := ports.NewWorkspaceHandle(g.locator)
		if err != nil {
			return resolvedExecutionResources{}, fmt.Errorf("build workspace handle for repository %s: %w", g.repositoryID, err)
		}
		workingDirectory, err := workspaces.WorkingDirectory(ctx, handle)
		if err != nil {
			return resolvedExecutionResources{}, fmt.Errorf("resolve working directory for repository %s: %w", g.repositoryID, err)
		}
		resolved.mounts = append(resolved.mounts, ports.AgentWorkspaceMount{
			RepositoryID: g.repositoryID, Handle: handle, WorkingDirectory: workingDirectory,
			Access: g.access, VCSObjectID: g.vcsObjectID, WorkspaceGeneration: g.generation,
		})
		resolved.sinkMounts = append(resolved.sinkMounts, agentevents.Mount{RepositoryID: g.repositoryID, Handle: handle})
		if g.access == ports.WorkspaceReadWrite {
			resolved.hasWriteMount = true
			writeTargets = append(writeTargets, ports.WorkspaceLeaseTarget{
				RepositoryID: g.repositoryID, RepositoryWorkspaceID: g.workspaceID, Generation: g.generation,
			})
			resolved.writeMounts = append(resolved.writeMounts, mutatingMountInfo{
				repositoryWorkspaceID: g.workspaceID, handle: handle, pinnedRevision: g.vcsObjectID, workspaceVersion: g.workspaceVersion,
			})
		}
	}

	if len(writeTargets) > 0 {
		grants, err := writeLeases.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
			JobLease: req.JobLease, AttemptID: ports.ExecutionAttemptID(req.AttemptID),
			Targets: writeTargets, TTL: request.Timeout + writeLeaseTTLGrace,
		})
		if err != nil {
			return resolvedExecutionResources{}, fmt.Errorf("acquire write leases: %w", err)
		}
		resolved.writeLeaseGrants = grants
	}
	return resolved, nil
}

func (e *AgentNodeExecutor) resolveExecutionResources(
	ctx context.Context, req ports.NodeExecutionRequest, request ports.AgentExecutionRequest,
) (resolvedExecutionResources, error) {
	return resolveExecutionResources(ctx, e.uow, e.workspaces, e.writeLeases, req, request)
}

// buildEvidence implements V5-08B's own locked 3-phase evidence protocol's
// first two phases (decision #2): Phase 1, real I/O entirely outside any
// transaction — re-measure each mount's own diff now that
// AgentExecutionResult.TreeQuiesced has already confirmed the process tree
// is quiesced (classify's own caller-side check, before this is ever
// invoked), then Put/Verify each diff as its own durable artifact. Phase
// 2, one short transaction inserting every one of those artifacts as
// ORPHAN — crash or reject after this point leaves an auditable orphan,
// never something silently accepted as evidence. Phase 3 (revalidate
// everything and promote ORPHAN->ATTACHED) is FinalizeExecutionAttempt's
// own job, atomically with everything else it commits — never repeated or
// duplicated here.
//
// A free function (V5-09: CommandNodeExecutor needs this EXACT same
// evidence-staging protocol — "diff/fence" is explicitly required of a
// mutating COMMAND too, and attachFinalizationEvidenceTx's own
// finalize-time re-validation, finalize.go, has no AGENT-specific
// coupling at all) rather than a method on *AgentNodeExecutor;
// e.buildEvidence below is a thin, unchanged wrapper. request stays typed
// as ports.AgentExecutionRequest for the identical reason
// resolveExecutionResources's own doc comment already gives.
func buildEvidence(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock, store ports.ArtifactStore, workspaces ports.WorkspaceProvider,
	req ports.NodeExecutionRequest, request ports.AgentExecutionRequest, resolved resolvedExecutionResources, proposedOutcome *ports.AgentProposedOutcome,
) (*ports.AttemptFinalizationEvidence, error) {
	diffs := make([]ports.WorkspaceDiff, 0, len(request.WorkspaceMounts))
	for _, mount := range request.WorkspaceMounts {
		base := workspace.Revision{RepositoryID: mount.RepositoryID, VCSObjectID: mount.VCSObjectID, WorkspaceGeneration: mount.WorkspaceGeneration}
		diff, err := workspaces.Diff(ctx, mount.Handle, base)
		if err != nil {
			return nil, fmt.Errorf("diff repository %s after quiescence: %w", mount.RepositoryID, err)
		}
		diffs = append(diffs, diff)
	}
	// The SAME scope-validation discipline agentevents.Sink's own
	// captureCheckpointLocked already applies to every mid-run checkpoint
	// — the final diff, measured only now that quiescence is confirmed,
	// gets the identical check before it is ever staged as evidence
	// (V5-08B's own locked decision #2's "kiểm... diff scope"). Real diff
	// bytes only ever exist here, outside any transaction — finalize.go's
	// own re-validation later can only check what's ALREADY persisted
	// (artifact existence/count), never re-derive scope correctness from
	// raw content without violating "no ArtifactStore call inside a
	// transaction."
	if err := scopeguard.ValidateDiffs(request.EffectiveScope, diffs); err != nil {
		return nil, err
	}

	revisions := make([]workspace.Revision, 0, len(diffs))
	diffArtifacts := make([]ports.DiffManifestArtifactRef, 0, len(diffs))
	var toInsert []artifact.Artifact

	for i, mount := range request.WorkspaceMounts {
		diff := diffs[i]
		revisions = append(revisions, diff.CurrentRevision)

		body, err := json.Marshal(diff)
		if err != nil {
			return nil, fmt.Errorf("encode diff manifest for repository %s: %w", mount.RepositoryID, err)
		}
		ref, err := store.Put(ctx, ports.ArtifactMetadata{ContentType: diffManifestArtifactMediaType, Sensitivity: redact.Sensitive}, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("put diff manifest artifact for repository %s: %w", mount.RepositoryID, err)
		}
		if err := store.Verify(ctx, ref); err != nil {
			return nil, fmt.Errorf("verify diff manifest artifact for repository %s: %w", mount.RepositoryID, err)
		}
		artifactID := ids.NewID()
		a, err := artifact.NewArtifact(
			artifact.ID(artifactID), resolved.projectID, ref.Locator, ref.SHA256, ref.Size, ref.ContentType,
			ref.Sensitivity, ref.Redacted, artifact.RetentionCanonicalContext, artifact.Orphan, false, nil, clk.Now(), 1,
		)
		if err != nil {
			return nil, fmt.Errorf("construct diff manifest artifact record for repository %s: %w", mount.RepositoryID, err)
		}
		toInsert = append(toInsert, a)
		diffArtifacts = append(diffArtifacts, ports.DiffManifestArtifactRef{RepositoryID: mount.RepositoryID, ArtifactID: artifactID})
	}

	if len(toInsert) > 0 {
		if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			for _, a := range toInsert {
				if _, err := tx.Artifacts().InsertArtifact(ctx, a); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("insert diff manifest artifacts as ORPHAN: %w", err)
		}
	}

	finalRevisionSet, err := workspace.NewRevisionSet(revisions)
	if err != nil {
		return nil, fmt.Errorf("build final revision set: %w", err)
	}

	terminalSequence, err := terminalEventSequence(ctx, uow, req.AttemptID)
	if err != nil {
		return nil, err
	}

	return &ports.AttemptFinalizationEvidence{
		SchemaVersion: 1, TerminalEventSequence: terminalSequence, CompletionCheckpointID: ids.NewID(),
		FinalRevisionSet: finalRevisionSet, DiffManifestArtifacts: diffArtifacts, ProposedOutcome: proposedOutcome,
	}, nil
}

func (e *AgentNodeExecutor) buildEvidence(
	ctx context.Context, req ports.NodeExecutionRequest, request ports.AgentExecutionRequest,
	resolved resolvedExecutionResources, proposedOutcome *ports.AgentProposedOutcome,
) (*ports.AttemptFinalizationEvidence, error) {
	return buildEvidence(ctx, e.uow, e.ids, e.clk, e.store, e.workspaces, req, request, resolved, proposedOutcome)
}

// terminalEventSequence returns the highest agent_events.Sequence durably
// persisted for attemptID — sink.Flush has already run (Execute's own
// caller-side ordering) by the time this is ever called, so the terminal
// EXECUTION_FINISHED event (always the last one any normalizer emits) is
// guaranteed already committed.
//
// A free function (V5-09: reused by buildEvidence above for either
// caller) rather than a method on *AgentNodeExecutor; e.terminalEventSequence
// below is a thin, unchanged wrapper.
func terminalEventSequence(ctx context.Context, uow ports.UnitOfWork, attemptID string) (uint64, error) {
	var sequence uint64
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		records, err := tx.AgentEvents().ListByAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.Sequence > sequence {
				sequence = record.Sequence
			}
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("load terminal agent event sequence: %w", err)
	}
	if sequence == 0 {
		return 0, errors.New("no agent events were persisted for this attempt — cannot build finalization evidence")
	}
	return sequence, nil
}

func (e *AgentNodeExecutor) terminalEventSequence(ctx context.Context, attemptID string) (uint64, error) {
	return terminalEventSequence(ctx, e.uow, attemptID)
}
