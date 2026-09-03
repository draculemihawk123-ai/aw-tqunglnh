// Package readinesscheck is V3-07's workerpool.Handler for the
// BASELINE_EVIDENCE durable job (docs/design/05-v3-project-workspace.md
// V3-07: "writer không chạy trước clean known base và baseline check").
// It mirrors internal/app/repositoryprobe.Handler and
// internal/app/workspaceprovision.Handler's own established structure
// exactly: claim a job, an idempotent early-return for a job whose real
// evidence has already been recorded (crash-recovery reclaim), real I/O
// (here: resolving the RepositoryWorkspace's own real working directory
// via ports.WorkspaceDirectoryResolver, then running the readiness
// profile's setup/verification commands as real subprocesses via
// ports.ProcessSupervisor) entirely OUTSIDE any open transaction, then a
// finish step that persists the resulting BaselineAttempt evidence (and,
// only for an ENVIRONMENT_ERROR outcome, opens this task's own typed
// EnvironmentBlocker) inside one transaction.
//
// # Trigger
//
// Unlike repositoryprobe (enqueued by internal/app/catalog.RegisterRepository)
// and workspaceprovision (enqueued by internal/app/work.CreateRootWorkItem),
// this package owns its own trigger end to end:
// EnqueueBaselineEvidenceJob (enqueue.go) is a plain, reusable function a
// caller invokes once it knows a RepositoryWorkspace has reached READY —
// the same "V3-06's own terminal success state" trigger point this task's
// own brief names, since baseline evidence only makes sense once a real,
// provisioned workspace exists to run commands against.
//
// This package deliberately does NOT hook into
// internal/app/workspaceprovision.Handler.finishReady to enqueue
// automatically: internal/app/workspaceprovision/handler_sqlite_test.go
// runs real workerpool.Pool machinery with tight timing assertions
// (500ms LeaseTTL, 20ms PollInterval, 2 concurrent workers) for its own
// crash-recovery/restart scenarios, and workerpool.Pool.runJob claims ANY
// available durable job regardless of kind before looking up a handler
// (internal/app/workerpool/pool.go) — enqueuing a BASELINE_EVIDENCE job
// there, with no handler registered in that test's own Pool, would sit
// permanently reclaimable (retried up to MaxClaims, then DEAD) and could
// contend for one of only 2 concurrent worker slots during an
// already-merged, timing-sensitive test. Keeping the trigger entirely
// within this package (call EnqueueBaselineEvidenceJob explicitly, from a
// test or from whatever later task wires a real composition root — none
// exists yet for repositoryprobe or workspaceprovision either) avoids that
// risk entirely at zero cost to V3-06's already-merged code.
//
// # Job success vs. business outcome
//
// Mirrors V3-02/V3-06's own identical distinction: GREEN, RED and
// ENVIRONMENT_ERROR are all business/evidence outcomes this handler
// records as data, never a handler failure — Handle returns nil for all
// three (workerpool marks the durable job itself SUCCEEDED). Only a
// genuine unexpected condition (a malformed job payload, a persistence
// failure, resolving the RepositoryWorkspace's own working directory
// failing on an already-READY workspace — see resolveWorkingDirectory's
// own doc comment) returns a real error, leaving the job un-completed for
// workerpool's own lease-expiry/retry mechanism.
package readinesscheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// generation is always 1: baseline evidence, like workspaceprovision's own
// first-provision scope, only ever concerns the first, READY generation of
// a RepositoryWorkspace. Regeneration after QUARANTINED is V3-09/V3-10's
// own separate concern (see internal/app/workspaceprovision's own
// identical `generation = 1` constant and doc comment) — entirely out of
// this task's own scope.
const generation = 1

// excerptLimit bounds how much of a real command's stdout/stderr this
// handler ever persists as evidence — mirroring
// internal/adapters/gitworktree's own runGitWithExitCode, which caps a
// captured git stderr message at the identical 4096 bytes for the
// identical reason: real command output can be arbitrarily large, and
// evidence exists to prove what happened, not to store a full log.
const excerptLimit = 4096

// defaultInheritedEnvironment is the minimal, explicit allow-list of
// parent environment keys passed to a readiness recipe's own subprocess
// (ports.ProcessSpec.InheritedEnvironment is deliberately an explicit
// allow-list, never a bulk "inherit everything"): PATH so a bare
// executable name (e.g. "npm", "make") resolves the same way it would in
// a real shell, plus the platform baseline os/exec itself needs to launch
// anything at all on Windows (SystemRoot, ComSpec, PATHEXT) — the same
// practical necessity internal/adapters/gitworktree's own real git
// subprocess calls already accept by passing the full os.Environ().
// Toolchain/dependency version pinning itself (HE-06-M04) is explicitly
// out of this task's own scope.
var defaultInheritedEnvironment = []string{"PATH", "HOME", "USERPROFILE", "SystemRoot", "ComSpec", "PATHEXT", "TEMP", "TMP"}

// Handler implements workerpool.Handler for BaselineEvidenceJobKind. It
// depends only on ports.ProcessSupervisor/ports.WorkspaceDirectoryResolver
// (never a concrete adapter package directly), mirroring
// repositoryprobe.Handler/workspaceprovision.Handler's own identical
// discipline.
type Handler struct {
	uow        ports.UnitOfWork
	ids        idsource.Source
	processes  ports.ProcessSupervisor
	workspaces ports.WorkspaceDirectoryResolver
}

// New returns a ready-to-register Handler.
func New(uow ports.UnitOfWork, ids idsource.Source, processes ports.ProcessSupervisor, workspaces ports.WorkspaceDirectoryResolver) *Handler {
	return &Handler{uow: uow, ids: ids, processes: processes, workspaces: workspaces}
}

var _ workerpool.Handler = (*Handler)(nil)

// jobPayload mirrors the exact JSON shape EnqueueBaselineEvidenceJob
// marshals.
type jobPayload struct {
	ProjectID             string `json:"projectId"`
	RepositoryID          string `json:"repositoryId"`
	WorkspaceSetID        string `json:"workspaceSetId"`
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
}

// Handle implements workerpool.Handler. See this package's own doc
// comment for the full contract.
func (h *Handler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload jobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("readinesscheck: unmarshal job %s payload: %w", job.ID, err)
	}
	if payload.ProjectID == "" || payload.RepositoryID == "" || payload.WorkspaceSetID == "" || payload.RepositoryWorkspaceID == "" {
		return fmt.Errorf("readinesscheck: job %s payload missing projectId/repositoryId/workspaceSetId/repositoryWorkspaceId", job.ID)
	}

	// Idempotent early-return, mirroring repositoryprobe/workspaceprovision's
	// own identical "already resolved" reclaim check: a previous claim of
	// this exact job already recorded real evidence, then crashed before
	// this job was itself marked SUCCEEDED.
	existing, err := h.loadExistingAttempt(ctx, string(job.ID))
	if err != nil {
		return fmt.Errorf("readinesscheck: load existing baseline attempt for job %s: %w", job.ID, err)
	}
	if existing != nil {
		return nil
	}

	rw, err := h.loadRepositoryWorkspace(ctx, payload.WorkspaceSetID, payload.RepositoryID)
	if err != nil {
		return fmt.Errorf("readinesscheck: load repository workspace for job %s: %w", job.ID, err)
	}
	if rw.State != workspace.RepositoryWorkspaceReady {
		// This job is only ever enqueued once a RepositoryWorkspace has
		// just reached READY (this package's own doc comment). Finding
		// anything else here means a later lifecycle event (e.g.
		// quarantine — V3-09/V3-10's own separate scope) has already moved
		// it past the one generation this task's own scope concerns: there
		// is nothing left for a baseline check to usefully do against a
		// workspace that is no longer the authoritative READY generation.
		return nil
	}

	profile, err := h.loadReadinessProfile(ctx, payload.RepositoryID)
	if err != nil {
		return fmt.Errorf("readinesscheck: load readiness profile for job %s: %w", job.ID, err)
	}
	if profile == nil {
		// No readiness profile has ever been set for this repository — see
		// internal/domain/readiness's own package doc comment: HE-06-M02
		// asks for a canonical recipe to exist, it does not ask this task
		// to invent automatic recipe discovery (that is HE-06-M05's own,
		// separate "component discovery" territory, already V3-02's job).
		// Nothing to check yet; no evidence is recorded, none is faked.
		return nil
	}

	// Real I/O: entirely outside any open transaction, mirroring
	// repositoryprobe.Handler/workspaceprovision.Handler's own identical
	// discipline.
	workingDirectory, err := h.resolveWorkingDirectory(ctx, rw)
	if err != nil {
		return fmt.Errorf("readinesscheck: resolve working directory for job %s: %w", job.ID, err)
	}

	record := h.runProfile(ctx, string(job.ID), workingDirectory, *profile)
	return h.finish(ctx, job, payload, record)
}

func (h *Handler) loadExistingAttempt(ctx context.Context, jobID string) (*ports.BaselineAttempt, error) {
	var result *ports.BaselineAttempt
	err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Readiness().GetBaselineAttemptByJobID(ctx, jobID)
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &attempt
		return nil
	})
	return result, err
}

func (h *Handler) loadRepositoryWorkspace(ctx context.Context, workspaceSetID, repositoryID string) (workspace.RepositoryWorkspace, error) {
	var rw workspace.RepositoryWorkspace
	err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		result, err := tx.Work().GetRepositoryWorkspace(ctx, workspaceSetID, repositoryID, generation)
		rw = result
		return err
	})
	return rw, err
}

func (h *Handler) loadReadinessProfile(ctx context.Context, repositoryID string) (*readiness.Profile, error) {
	var result *readiness.Profile
	err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		profile, err := tx.Readiness().GetReadinessProfile(ctx, repositoryID)
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &profile
		return nil
	})
	return result, err
}

// resolveWorkingDirectory resolves rw's own opaque Locator to a real,
// spawnable filesystem path. A failure here is treated as a genuine
// handler error (left for retry), never ENVIRONMENT_ERROR evidence — this
// mirrors workspaceprovision.Handler's own identical treatment of a
// CaptureRevision failure immediately after a successful Provision
// ("inspects a workspace this same call just created — a real, unexpected
// condition, not a business outcome"): rw is already READY, meaning
// workspaceprovision's own CaptureRevision already proved this exact
// workspace resolvable once; failing to resolve it again now means the
// workspace itself became unreachable/corrupted after being marked READY,
// not that the readiness recipe encountered a normal environment fault.
func (h *Handler) resolveWorkingDirectory(ctx context.Context, rw workspace.RepositoryWorkspace) (string, error) {
	handle, err := ports.NewWorkspaceHandle(rw.Locator)
	if err != nil {
		return "", fmt.Errorf("build workspace handle: %w", err)
	}
	return h.workspaces.WorkingDirectory(ctx, handle)
}

// attemptRecord is what runProfile computes — everything finish needs to
// persist one BaselineAttempt (and, if Outcome.Blocking(), open an
// EnvironmentBlocker) without runProfile itself touching a transaction
// (real I/O and persistence stay strictly separated, per this package's
// own doc comment).
type attemptRecord struct {
	Stage         readiness.Stage
	Outcome       readiness.BaselineOutcome
	ExitCode      *int
	DurationMS    int64
	StdoutExcerpt string
	StderrExcerpt string
	ErrorCode     *string
	ErrorMessage  *string
}

// runProfile runs profile's Setup command (if any) followed by its
// Verification command against workingDirectory, entirely outside any
// transaction. If Setup is present and does not itself produce a GREEN
// outcome, Verification never runs — there is nothing valid to verify
// against a repository whose own setup step did not succeed — and the
// returned attemptRecord reports Setup's own outcome (Stage SETUP).
// Otherwise the returned attemptRecord reports Verification's own outcome
// (Stage VERIFICATION).
func (h *Handler) runProfile(ctx context.Context, jobID string, workingDirectory string, profile readiness.Profile) attemptRecord {
	if profile.Setup != nil {
		setupRecord := h.runCommand(ctx, jobID+"-setup", workingDirectory, readiness.StageSetup, *profile.Setup)
		if setupRecord.Outcome != readiness.BaselineGreen {
			return setupRecord
		}
	}
	return h.runCommand(ctx, jobID+"-verification", workingDirectory, readiness.StageVerification, profile.Verification)
}

func (h *Handler) runCommand(ctx context.Context, processID string, workingDirectory string, stage readiness.Stage, spec readiness.CommandSpec) attemptRecord {
	commandDirectory := workingDirectory
	if spec.WorkingDirectory != "" {
		commandDirectory = filepath.Join(workingDirectory, spec.WorkingDirectory)
	}

	var stdout, stderr bytes.Buffer
	start := time.Now()
	result, runErr := h.processes.Run(ctx, ports.ProcessSpec{
		ID:                   ports.ProcessID(processID),
		Executable:           spec.Executable,
		Argv:                 spec.Argv,
		WorkingDirectory:     commandDirectory,
		InheritedEnvironment: defaultInheritedEnvironment,
		Timeout:              time.Duration(spec.TimeoutSeconds) * time.Second,
	}, &stdout, &stderr)
	duration := time.Since(start)

	record := attemptRecord{
		Stage: stage, DurationMS: duration.Milliseconds(),
		StdoutExcerpt: excerpt(stdout.Bytes()), StderrExcerpt: excerpt(stderr.Bytes()),
	}
	classify(result, runErr, &record)
	return record
}

// classify draws the exact three-way split
// internal/domain/readiness.BaselineOutcome's own doc comment documents:
// runErr != nil, or the process TimedOut/was Cancelled, is always
// ENVIRONMENT_ERROR — the command could not be observed to run to
// completion at all — never conflated with a genuine non-zero exit (RED).
func classify(result ports.ProcessResult, runErr error, record *attemptRecord) {
	if runErr != nil {
		code := string(apperror.CodeUnavailable)
		message := runErr.Error()
		record.Outcome = readiness.BaselineEnvironmentError
		record.ErrorCode = &code
		record.ErrorMessage = &message
		return
	}
	if result.TimedOut || result.Cancelled {
		code := string(apperror.CodeUnavailable)
		message := "command did not complete: "
		if result.TimedOut {
			message += "timed out"
		} else {
			message += "cancelled"
		}
		record.Outcome = readiness.BaselineEnvironmentError
		record.ErrorCode = &code
		record.ErrorMessage = &message
		return
	}
	exitCode := result.ExitCode
	record.ExitCode = &exitCode
	if exitCode == 0 {
		record.Outcome = readiness.BaselineGreen
		return
	}
	record.Outcome = readiness.BaselineRed
}

func excerpt(data []byte) string {
	if len(data) <= excerptLimit {
		return string(data)
	}
	return string(data[:excerptLimit])
}

// finish persists record as one BaselineAttempt row and, only for a
// Blocking outcome, opens this task's own typed EnvironmentBlocker — or,
// for a non-Blocking outcome, resolves any EnvironmentBlocker already OPEN
// for this RepositoryWorkspace (forward-compatible with a future retry
// path: no code in this task's own scope ever enqueues a second
// BASELINE_EVIDENCE job for the same RepositoryWorkspace, but a fresh
// non-ENVIRONMENT_ERROR attempt correctly closing out a stale blocker,
// should one ever exist, costs nothing and keeps EnvironmentBlocker.Status
// meaningful rather than a write-only dead end).
func (h *Handler) finish(ctx context.Context, job ports.DurableJob, payload jobPayload, record attemptRecord) error {
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Readiness().RecordBaselineAttempt(ctx, ports.RecordBaselineAttemptRequest{
			ID: h.ids.NewID(), ProjectID: payload.ProjectID, RepositoryWorkspaceID: payload.RepositoryWorkspaceID,
			RepositoryID: payload.RepositoryID, JobID: string(job.ID), Stage: record.Stage, Outcome: record.Outcome,
			ExitCode: record.ExitCode, DurationMS: record.DurationMS,
			StdoutExcerpt: record.StdoutExcerpt, StderrExcerpt: record.StderrExcerpt,
			ErrorCode: record.ErrorCode, ErrorMessage: record.ErrorMessage,
		})
		if err != nil {
			return err
		}

		if record.Outcome.Blocking() {
			reason := string(record.Stage) + " could not be observed to run"
			if record.ErrorMessage != nil {
				reason = *record.ErrorMessage
			}
			_, _, err := tx.Readiness().OpenEnvironmentBlocker(ctx, ports.OpenEnvironmentBlockerRequest{
				ID: h.ids.NewID(), ProjectID: payload.ProjectID, RepositoryWorkspaceID: payload.RepositoryWorkspaceID,
				RepositoryID: payload.RepositoryID, JobID: string(job.ID), Reason: reason,
			})
			return err
		}
		return tx.Readiness().ResolveOpenEnvironmentBlocker(ctx, payload.RepositoryWorkspaceID, time.Now().UTC())
	})
}
