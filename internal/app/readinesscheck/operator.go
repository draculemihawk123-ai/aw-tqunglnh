package readinesscheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
)

// This file is V9-08's operator surface over the readiness capability V3-07
// built (docs/design/12-v9-harness-alignment.md V9-08; gap G8): the commands
// and queries that CLI, HTTP and the UI all call —
//
//   - SetRepositoryReadinessProfile declares (or changes) a repository's
//     setup/verification recipe and starts a baseline on every READY workspace
//     of the repository, because a baseline only vouches for the profile it ran;
//   - RequestBaselineCheck (`verify`) starts the baseline again on demand;
//   - AcceptBaselineException lets an operator admit writers on a repository
//     whose baseline failed, recorded with who, when and why;
//   - GetRepositoryReadiness shows the profile and, for every workspace, where
//     its baseline stands.
//
// The commands are idempotent through the command receipt like every other
// operator command (MarkWorkItemReady, RetryRepositoryProbe).

// ErrNoReadinessProfile is returned when a command needs the repository's
// readiness profile and it has none.
var ErrNoReadinessProfile = errors.New("readinesscheck: the repository has no readiness profile; set one first")

// ErrBaselineAttemptNotAcceptable is returned by AcceptBaselineException when
// the attempt cannot take an exception: it passed, or it ran for a readiness
// profile that has since changed.
var ErrBaselineAttemptNotAcceptable = errors.New("readinesscheck: this baseline attempt cannot take an exception")

// CommandInput is one profile command as an operator declares it: an
// executable plus argv (never a shell string), an optional working directory
// relative to the workspace and a timeout.
type CommandInput struct {
	Executable       string   `json:"executable"`
	Argv             []string `json:"argv,omitempty"`
	WorkingDirectory string   `json:"workingDirectory,omitempty"`
	TimeoutSeconds   uint32   `json:"timeoutSeconds"`
}

func (c CommandInput) spec() (readiness.CommandSpec, error) {
	return readiness.NewCommandSpec(c.Executable, c.Argv, c.WorkingDirectory, c.TimeoutSeconds)
}

func commandView(spec readiness.CommandSpec) CommandInput {
	return CommandInput{Executable: spec.Executable, Argv: spec.Argv, WorkingDirectory: spec.WorkingDirectory, TimeoutSeconds: spec.TimeoutSeconds}
}

// --- SetRepositoryReadinessProfile ---

// SetRepositoryReadinessProfileRequest is what a caller supplies.
type SetRepositoryReadinessProfileRequest struct {
	ProjectID    string
	RepositoryID string
	Setup        *CommandInput
	Verification CommandInput
}

// SetRepositoryReadinessProfileResult is the command's result (and what a
// replayed receipt reconstructs).
type SetRepositoryReadinessProfileResult struct {
	RepositoryID         string `json:"repositoryId"`
	ProfileVersion       uint64 `json:"profileVersion"`
	BaselineJobsEnqueued int    `json:"baselineJobsEnqueued"`
}

// SetRepositoryReadinessProfile declares repository's readiness profile and, in
// the same transaction, enqueues a baseline job on each of its READY
// workspaces for the new profile version. Workspaces that become READY later
// get theirs from workspace provisioning.
func SetRepositoryReadinessProfile(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req SetRepositoryReadinessProfileRequest) (SetRepositoryReadinessProfileResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.RepositoryID) == "" {
		return SetRepositoryReadinessProfileResult{}, errors.New("readinesscheck: ProjectID and RepositoryID are required")
	}
	verification, err := req.Verification.spec()
	if err != nil {
		return SetRepositoryReadinessProfileResult{}, fmt.Errorf("verification: %w", err)
	}
	var setup *readiness.CommandSpec
	if req.Setup != nil {
		spec, err := req.Setup.spec()
		if err != nil {
			return SetRepositoryReadinessProfileResult{}, fmt.Errorf("setup: %w", err)
		}
		setup = &spec
	}
	profile, err := readiness.NewProfile(project.RepositoryID(req.RepositoryID), setup, verification)
	if err != nil {
		return SetRepositoryReadinessProfileResult{}, err
	}

	var result SetRepositoryReadinessProfileResult
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replayReceipt(ctx, tx, cmd, &result); replayed || err != nil {
			return err
		}
		if err := requireRepositoryInProject(ctx, tx, req.RepositoryID, req.ProjectID); err != nil {
			return err
		}
		stored, err := tx.Readiness().SetReadinessProfile(ctx, profile)
		if err != nil {
			return err
		}
		enqueued, err := EnqueueBaselineForRepositoryWorkspaces(ctx, tx, ids, req.ProjectID, req.RepositoryID, stored.Version, "", cmd.RequestedAt)
		if err != nil {
			return err
		}
		result = SetRepositoryReadinessProfileResult{RepositoryID: req.RepositoryID, ProfileVersion: stored.Version, BaselineJobsEnqueued: enqueued}
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}

// --- RequestBaselineCheck ---

// RequestBaselineCheckRequest is what a caller supplies.
type RequestBaselineCheckRequest struct {
	ProjectID    string
	RepositoryID string
}

// RequestBaselineCheckResult is the command's result.
type RequestBaselineCheckResult struct {
	RepositoryID         string `json:"repositoryId"`
	ProfileVersion       uint64 `json:"profileVersion"`
	BaselineJobsEnqueued int    `json:"baselineJobsEnqueued"`
}

// RequestBaselineCheck starts the baseline again on every READY workspace of
// the repository (`aw repository readiness verify`): after the operator fixed
// the repository or its environment, or to see a failure reproduce. Each call
// enqueues its own jobs (the idempotency key of the command is the nonce), so
// a replayed command does not enqueue twice but a new one does.
func RequestBaselineCheck(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req RequestBaselineCheckRequest) (RequestBaselineCheckResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.RepositoryID) == "" {
		return RequestBaselineCheckResult{}, errors.New("readinesscheck: ProjectID and RepositoryID are required")
	}
	var result RequestBaselineCheckResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replayReceipt(ctx, tx, cmd, &result); replayed || err != nil {
			return err
		}
		if err := requireRepositoryInProject(ctx, tx, req.RepositoryID, req.ProjectID); err != nil {
			return err
		}
		profile, err := loadProfile(ctx, tx, req.RepositoryID)
		if err != nil {
			return err
		}
		if profile == nil {
			return ErrNoReadinessProfile
		}
		enqueued, err := EnqueueBaselineForRepositoryWorkspaces(ctx, tx, ids, req.ProjectID, req.RepositoryID, profile.Version, cmd.IdempotencyKey, cmd.RequestedAt)
		if err != nil {
			return err
		}
		result = RequestBaselineCheckResult{RepositoryID: req.RepositoryID, ProfileVersion: profile.Version, BaselineJobsEnqueued: enqueued}
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}

// --- AcceptBaselineException ---

// AcceptBaselineExceptionRequest is what a caller supplies.
type AcceptBaselineExceptionRequest struct {
	ProjectID         string
	RepositoryID      string
	BaselineAttemptID string
	Reason            string
}

// AcceptBaselineExceptionResult is the command's result.
type AcceptBaselineExceptionResult struct {
	ExceptionID           string    `json:"exceptionId"`
	BaselineAttemptID     string    `json:"baselineAttemptId"`
	RepositoryWorkspaceID string    `json:"repositoryWorkspaceId"`
	RepositoryID          string    `json:"repositoryId"`
	AcceptedBy            string    `json:"acceptedBy"`
	AcceptedAt            time.Time `json:"acceptedAt"`
}

// AcceptBaselineException records that the person behind cmd accepts the failure
// of one baseline attempt, for the stated reason, so that writers are admitted
// on that workspace despite it (HE-12-M03: a regression must not be hidden by
// a failure that was already there — the failure stays on the record and the
// acceptance is a separate, attributed fact). It names the attempt: it must be
// a failed attempt of the repository's current profile version, and a later
// failing attempt needs its own acceptance.
func AcceptBaselineException(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req AcceptBaselineExceptionRequest) (AcceptBaselineExceptionResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.RepositoryID) == "" || strings.TrimSpace(req.BaselineAttemptID) == "" {
		return AcceptBaselineExceptionResult{}, errors.New("readinesscheck: ProjectID, RepositoryID and BaselineAttemptID are required")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return AcceptBaselineExceptionResult{}, errors.New("readinesscheck: a reason is required to accept a baseline failure")
	}
	var result AcceptBaselineExceptionResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replayReceipt(ctx, tx, cmd, &result); replayed || err != nil {
			return err
		}
		if err := requireRepositoryInProject(ctx, tx, req.RepositoryID, req.ProjectID); err != nil {
			return err
		}
		attempt, err := findAttempt(ctx, tx, req.RepositoryID, req.BaselineAttemptID)
		if err != nil {
			return err
		}
		if attempt.Outcome.Passed() {
			return fmt.Errorf("%w: attempt %s passed, there is nothing to accept", ErrBaselineAttemptNotAcceptable, attempt.ID)
		}
		profile, err := loadProfile(ctx, tx, req.RepositoryID)
		if err != nil {
			return err
		}
		if profile == nil || attempt.ProfileVersion != profile.Version {
			return fmt.Errorf("%w: attempt %s ran for readiness profile version %d, which is no longer the repository's current one — run `verify` and accept the new result",
				ErrBaselineAttemptNotAcceptable, attempt.ID, attempt.ProfileVersion)
		}
		exception, err := tx.Readiness().RecordBaselineException(ctx, ports.RecordBaselineExceptionRequest{
			ID: ids.NewID(), ProjectID: req.ProjectID, BaselineAttemptID: attempt.ID,
			Reason: strings.TrimSpace(req.Reason), AcceptedBy: cmd.Actor, AcceptedAt: cmd.RequestedAt,
		})
		if err != nil {
			return err
		}
		result = AcceptBaselineExceptionResult{
			ExceptionID: exception.ID, BaselineAttemptID: exception.BaselineAttemptID,
			RepositoryWorkspaceID: exception.RepositoryWorkspaceID, RepositoryID: exception.RepositoryID,
			AcceptedBy: exception.AcceptedBy, AcceptedAt: exception.AcceptedAt,
		}
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}

// findAttempt finds attemptID among the baseline attempts of repositoryID's
// workspaces; ErrPersistenceNotFound when it is not one of them (so an id of
// another repository reads as absent, not as forbidden).
func findAttempt(ctx context.Context, tx ports.Tx, repositoryID, attemptID string) (ports.BaselineAttempt, error) {
	workspaces, err := tx.Work().ListRepositoryWorkspacesForRepository(ctx, repositoryID)
	if err != nil {
		return ports.BaselineAttempt{}, err
	}
	for _, rw := range workspaces {
		attempts, err := tx.Readiness().ListBaselineAttempts(ctx, string(rw.ID))
		if err != nil {
			return ports.BaselineAttempt{}, err
		}
		for _, attempt := range attempts {
			if attempt.ID == attemptID {
				return attempt, nil
			}
		}
	}
	return ports.BaselineAttempt{}, fmt.Errorf("%w: baseline attempt %s of repository %s", ports.ErrPersistenceNotFound, attemptID, repositoryID)
}

// --- GetRepositoryReadiness ---

// ProfileView is a readiness profile as the operator surfaces show it.
type ProfileView struct {
	Version      uint64       `json:"version"`
	Setup        *CommandInput `json:"setup,omitempty"`
	Verification CommandInput `json:"verification"`
}

// AttemptView is one baseline attempt: the outcome, its classification and the
// bounded output the check printed.
type AttemptView struct {
	AttemptID      string    `json:"attemptId"`
	Stage          string    `json:"stage"`
	Outcome        string    `json:"outcome"`
	FailureKind    string    `json:"failureKind,omitempty"`
	ExitCode       *int      `json:"exitCode,omitempty"`
	DurationMS     int64     `json:"durationMs"`
	StdoutExcerpt  string    `json:"stdoutExcerpt,omitempty"`
	StderrExcerpt  string    `json:"stderrExcerpt,omitempty"`
	ErrorCode      string    `json:"errorCode,omitempty"`
	ErrorMessage   string    `json:"errorMessage,omitempty"`
	ProfileVersion uint64    `json:"profileVersion"`
	CreatedAt      time.Time `json:"createdAt"`
}

// ExceptionView is an accepted baseline exception.
type ExceptionView struct {
	ExceptionID string    `json:"exceptionId"`
	Reason      string    `json:"reason"`
	AcceptedBy  string    `json:"acceptedBy"`
	AcceptedAt  time.Time `json:"acceptedAt"`
}

// WorkspaceBaselineView is where one RepositoryWorkspace stands.
type WorkspaceBaselineView struct {
	RepositoryWorkspaceID string         `json:"repositoryWorkspaceId"`
	WorkspaceSetID        string         `json:"workspaceSetId"`
	Generation            uint64         `json:"generation"`
	WorkspaceState        string         `json:"workspaceState"`
	BaselineState         string         `json:"baselineState"`
	AdmitsWriters         bool           `json:"admitsWriters"`
	Reason                string         `json:"reason,omitempty"`
	Attempt               *AttemptView   `json:"attempt,omitempty"`
	Exception             *ExceptionView `json:"exception,omitempty"`
}

// RepositoryReadinessView is GetRepositoryReadiness' result.
type RepositoryReadinessView struct {
	RepositoryID string                  `json:"repositoryId"`
	Profile      *ProfileView            `json:"profile,omitempty"`
	Workspaces   []WorkspaceBaselineView `json:"workspaces"`
}

// GetRepositoryReadiness is the read-only view: the repository's profile (nil
// when none) and, for each of its workspaces, the state of the baseline for the
// profile's current version.
func GetRepositoryReadiness(ctx context.Context, uow ports.UnitOfWork, projectID, repositoryID string) (RepositoryReadinessView, error) {
	view := RepositoryReadinessView{RepositoryID: repositoryID, Workspaces: []WorkspaceBaselineView{}}
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if err := requireRepositoryInProject(ctx, tx, repositoryID, projectID); err != nil {
			return err
		}
		profile, err := loadProfile(ctx, tx, repositoryID)
		if err != nil {
			return err
		}
		if profile != nil {
			profileView := ProfileView{Version: profile.Version, Verification: commandView(profile.Verification)}
			if profile.Setup != nil {
				setup := commandView(*profile.Setup)
				profileView.Setup = &setup
			}
			view.Profile = &profileView
		}
		workspaces, err := tx.Work().ListRepositoryWorkspacesForRepository(ctx, repositoryID)
		if err != nil {
			return err
		}
		for _, rw := range workspaces {
			evaluation, err := EvaluateWorkspaceBaseline(ctx, tx, profile, rw)
			if err != nil {
				return err
			}
			entry := WorkspaceBaselineView{
				RepositoryWorkspaceID: string(rw.ID), WorkspaceSetID: string(rw.WorkspaceSetID), Generation: rw.Generation,
				WorkspaceState: string(rw.State), BaselineState: string(evaluation.State),
				AdmitsWriters: evaluation.State.AdmitsWriters(), Reason: evaluation.Reason,
			}
			if evaluation.Attempt != nil {
				entry.Attempt = attemptView(*evaluation.Attempt)
			}
			if evaluation.Exception != nil {
				entry.Exception = &ExceptionView{
					ExceptionID: evaluation.Exception.ID, Reason: evaluation.Exception.Reason,
					AcceptedBy: evaluation.Exception.AcceptedBy, AcceptedAt: evaluation.Exception.AcceptedAt,
				}
			}
			view.Workspaces = append(view.Workspaces, entry)
		}
		return nil
	})
	return view, err
}

func attemptView(attempt ports.BaselineAttempt) *AttemptView {
	view := &AttemptView{
		AttemptID: attempt.ID, Stage: string(attempt.Stage), Outcome: string(attempt.Outcome),
		FailureKind: string(attempt.Outcome.FailureKind()), ExitCode: attempt.ExitCode, DurationMS: attempt.DurationMS,
		StdoutExcerpt: attempt.StdoutExcerpt, StderrExcerpt: attempt.StderrExcerpt,
		ProfileVersion: attempt.ProfileVersion, CreatedAt: attempt.CreatedAt,
	}
	if attempt.ErrorCode != nil {
		view.ErrorCode = *attempt.ErrorCode
	}
	if attempt.ErrorMessage != nil {
		view.ErrorMessage = *attempt.ErrorMessage
	}
	return view
}

// --- shared helpers ---

// requireRepositoryInProject loads the repository and checks it belongs to
// projectID. A repository of another project reads as absent (the caller has no
// access to it), the same leakage-normalized answer the catalog queries give.
func requireRepositoryInProject(ctx context.Context, tx ports.Tx, repositoryID, projectID string) error {
	repo, err := tx.Catalog().GetRepository(ctx, repositoryID)
	if err != nil {
		return err
	}
	if string(repo.ProjectID) != projectID {
		return fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, repositoryID)
	}
	return nil
}

func replayReceipt(ctx context.Context, tx ports.Tx, cmd ports.Command, result any) (replayed bool, err error) {
	existing, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if existing.RequestHash != cmd.RequestHash {
		return true, ports.ErrReceiptConflict
	}
	return true, json.Unmarshal([]byte(existing.ResultJSON), result)
}

func recordReceipt(ctx context.Context, tx ports.Tx, cmd ports.Command, result any) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal receipt result: %w", err)
	}
	return tx.Receipts().Record(ctx, ports.Receipt{
		Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
		CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
		CreatedAt: cmd.RequestedAt,
	})
}
