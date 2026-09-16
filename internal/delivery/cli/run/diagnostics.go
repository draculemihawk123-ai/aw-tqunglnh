package run

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"run", "diagnostics"}, Scope: cli.ScopeProject,
		AppOperation: "GetRunDiagnostics", HTTPOperationID: "getRunDiagnostics",
	})
}

// The DTOs below are hand-mapped, field-by-field, from
// internal/app/runtime.RunDiagnostics and its own nested types — never a
// verbatim struct embed of that internal/app type (whose own Go field
// names carry no json tags at all) and never a re-serialization of any
// deeper internal type (ports.AgentExecutionRequest, a COMMAND node's own
// command.Document, any OS-process type). This mirrors
// internal/delivery/httpapi/diagnostics/dto.go's own explicit-allowlist
// discipline exactly (that package's own doc comment on
// orphanedAttemptResponse: "deliberately hand-mapped field by field ...
// so a future field added to internal/app/runtime.OrphanedAttemptDiagnostic
// never silently reaches [a] response without this package's own author
// reviewing it against [the] PID/argv/cwd/secret prohibition first") — the
// same discipline applies here, for the identical reason, to a CLI
// response instead of an HTTP one. GetRunDiagnostics' own package doc
// comment already establishes that no PID/argv/cwd/secret-shaped field
// exists anywhere in internal/app/runtime.RunDiagnostics to begin with
// (every type there is a hand-written DTO with an explicit field
// allowlist of its own); this package's own diagnostics_test.go proves
// that guarantee survives this second hand-mapping unchanged.

type DiagnosticsBlockerView struct {
	BlockerID       string    `json:"blockerId"`
	Type            string    `json:"type"`
	State           string    `json:"state"`
	Reason          string    `json:"reason"`
	SourceNodeRunID string    `json:"sourceNodeRunId,omitempty"`
	SourceAttemptID string    `json:"sourceAttemptId,omitempty"`
	OpenedAt        time.Time `json:"openedAt"`
	Version         uint64    `json:"version"`
	AdmissionReason bool      `json:"admissionReason"`
}

func toDiagnosticsBlockerView(b runtime.BlockerDiagnostic) DiagnosticsBlockerView {
	return DiagnosticsBlockerView{
		BlockerID: b.BlockerID, Type: b.Type, State: b.State, Reason: b.Reason,
		SourceNodeRunID: b.SourceNodeRunID, SourceAttemptID: b.SourceAttemptID,
		OpenedAt: b.OpenedAt, Version: b.Version, AdmissionReason: b.AdmissionReason,
	}
}

type DiagnosticsOrphanedAttemptView struct {
	AttemptID             string     `json:"attemptId"`
	NodeRunID             string     `json:"nodeRunId"`
	AttemptNumber         uint32     `json:"attemptNumber"`
	ProviderKey           string     `json:"providerKey,omitempty"`
	StartedAt             *time.Time `json:"startedAt,omitempty"`
	RepositoryWorkspaceID string     `json:"repositoryWorkspaceId,omitempty"`
	HasWriteLease         bool       `json:"hasWriteLease"`
}

func toDiagnosticsOrphanedAttemptView(a runtime.OrphanedAttemptDiagnostic) DiagnosticsOrphanedAttemptView {
	return DiagnosticsOrphanedAttemptView{
		AttemptID: a.AttemptID, NodeRunID: a.NodeRunID, AttemptNumber: a.AttemptNumber, ProviderKey: a.ProviderKey,
		StartedAt: a.StartedAt, RepositoryWorkspaceID: a.RepositoryWorkspaceID, HasWriteLease: a.HasWriteLease,
	}
}

type DiagnosticsProviderView struct {
	AdapterBuildID     string   `json:"adapterBuildId"`
	ProviderKey        string   `json:"providerKey"`
	ProviderConfigured bool     `json:"providerConfigured"`
	NodeRunIDs         []string `json:"nodeRunIds,omitempty"`
}

func toDiagnosticsProviderView(p runtime.ProviderDiagnostic) DiagnosticsProviderView {
	return DiagnosticsProviderView{
		AdapterBuildID: p.AdapterBuildID, ProviderKey: p.ProviderKey,
		ProviderConfigured: p.ProviderConfigured, NodeRunIDs: p.NodeRunIDs,
	}
}

type DiagnosticsIsolationView struct {
	Tier        string `json:"tier"`
	Enforceable bool   `json:"enforceable"`
}

func toDiagnosticsIsolationView(i runtime.IsolationDiagnostic) DiagnosticsIsolationView {
	return DiagnosticsIsolationView{Tier: i.Tier, Enforceable: i.Enforceable}
}

type DiagnosticsRepositoryWorkspaceView struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	RepositoryID          string `json:"repositoryId"`
	State                 string `json:"state"`
	Generation            uint64 `json:"generation"`
	HasActiveWriteLease   bool   `json:"hasActiveWriteLease"`
}

func toDiagnosticsRepositoryWorkspaceView(rw runtime.RepositoryWorkspaceDiagnostic) DiagnosticsRepositoryWorkspaceView {
	return DiagnosticsRepositoryWorkspaceView{
		RepositoryWorkspaceID: rw.RepositoryWorkspaceID, RepositoryID: rw.RepositoryID,
		State: rw.State, Generation: rw.Generation, HasActiveWriteLease: rw.HasActiveWriteLease,
	}
}

// DiagnosticsResult is `run diagnostics`'s own JSON result — the CLI-owned
// wire vocabulary for runtime.RunDiagnostics.
type DiagnosticsResult struct {
	RunID           string `json:"runId"`
	ProjectID       string `json:"projectId"`
	WorkItemID      string `json:"workItemId"`
	WorkItemStatus  string `json:"workItemStatus"`
	WorkItemVersion uint64 `json:"workItemVersion"`
	RunState        string `json:"runState"`
	RunVersion      uint64 `json:"runVersion"`

	Blockers []DiagnosticsBlockerView `json:"blockers"`

	OrphanedAttempts          []DiagnosticsOrphanedAttemptView `json:"orphanedAttempts"`
	OrphanedAttemptsTruncated bool                             `json:"orphanedAttemptsTruncated"`

	Providers []DiagnosticsProviderView  `json:"providers"`
	Isolation []DiagnosticsIsolationView `json:"isolation"`

	RepositoryWorkspaces []DiagnosticsRepositoryWorkspaceView `json:"repositoryWorkspaces"`
}

func toDiagnosticsResult(diag runtime.RunDiagnostics) DiagnosticsResult {
	blockers := make([]DiagnosticsBlockerView, 0, len(diag.Blockers))
	for _, b := range diag.Blockers {
		blockers = append(blockers, toDiagnosticsBlockerView(b))
	}
	orphaned := make([]DiagnosticsOrphanedAttemptView, 0, len(diag.OrphanedAttempts))
	for _, a := range diag.OrphanedAttempts {
		orphaned = append(orphaned, toDiagnosticsOrphanedAttemptView(a))
	}
	providers := make([]DiagnosticsProviderView, 0, len(diag.Providers))
	for _, p := range diag.Providers {
		providers = append(providers, toDiagnosticsProviderView(p))
	}
	isolationViews := make([]DiagnosticsIsolationView, 0, len(diag.Isolation))
	for _, i := range diag.Isolation {
		isolationViews = append(isolationViews, toDiagnosticsIsolationView(i))
	}
	repoWorkspaces := make([]DiagnosticsRepositoryWorkspaceView, 0, len(diag.RepositoryWorkspaces))
	for _, rw := range diag.RepositoryWorkspaces {
		repoWorkspaces = append(repoWorkspaces, toDiagnosticsRepositoryWorkspaceView(rw))
	}
	return DiagnosticsResult{
		RunID: diag.RunID, ProjectID: diag.ProjectID, WorkItemID: diag.WorkItemID,
		WorkItemStatus: diag.WorkItemStatus, WorkItemVersion: diag.WorkItemVersion,
		RunState: diag.RunState, RunVersion: diag.RunVersion,
		Blockers: blockers, OrphanedAttempts: orphaned, OrphanedAttemptsTruncated: diag.OrphanedAttemptsTruncated,
		Providers: providers, Isolation: isolationViews, RepositoryWorkspaces: repoWorkspaces,
	}
}

// Diagnostics implements `aw run diagnostics <runId>`: a pure read dispatch
// over runtime.GetRunDiagnostics (diagnostics.go) — mirrors
// internal/delivery/httpapi/diagnostics's own handleGetRunDiagnostics
// exactly, including that query's own project-scoped signature (unlike
// show/graph/timeline, GetRunDiagnostics takes a ports.CommandScope, so
// --project-id is required here the same way the HTTP route's own
// {projectId} path segment is).
func Diagnostics(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run diagnostics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	runID := fs.Arg(0)
	if strings.TrimSpace(runID) == "" {
		return errors.New("cli: run diagnostics: <runId> argument is required")
	}
	if strings.TrimSpace(*projectID) == "" {
		return errors.New("cli: run diagnostics: --project-id is required")
	}

	diag, err := runtime.GetRunDiagnostics(ctx, deps.UOW, deps.Isolation, deps.Agents, ports.ProjectScope(*projectID), runID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, toDiagnosticsResult(diag))
}
