package ports

import (
	"context"
	"errors"
	"time"
)

// Manifest publish modes (V12-21, ADR-056).
const (
	// ManifestCheck runs the kit's checks of the manifest and its templates; nothing is published.
	ManifestCheck = "CHECK"
	// ManifestPublish publishes every definition the manifest declares, in dependency order (idempotent by content).
	ManifestPublish = "PUBLISH"
)

// ErrManifestNotConfigured: the operator gave this server no manifest for the project (aw serve --project-manifest).
var ErrManifestNotConfigured = errors.New("ports: no definition manifest is configured for this project")

// ErrManifestRunBusy: a run for the project is already going.
var ErrManifestRunBusy = errors.New("ports: a manifest run for this project is already going")

// ManifestPublisher runs the kit's publisher (kit/scripts/aw-publish.py) over the manifest (aw-project.json) the operator
// configured for a project when starting the server. The path never comes from a request.
type ManifestPublisher interface {
	ManifestStatus(projectID string) ManifestStatus
	RunManifest(ctx context.Context, projectID, mode string) (ManifestRun, error)
}

// ManifestStatus is whether a manifest is configured, where, and the last run since the server started.
type ManifestStatus struct {
	ProjectID    string       `json:"projectId"`
	Configured   bool         `json:"configured"`
	ManifestPath string       `json:"manifestPath,omitempty"`
	ScriptPath   string       `json:"scriptPath,omitempty"`
	LastRun      *ManifestRun `json:"lastRun,omitempty"`
}

// ManifestRun is one run of the publisher: its exit code and its (bounded, redacted) output.
type ManifestRun struct {
	Mode       string    `json:"mode"`
	ExitCode   int       `json:"exitCode"`
	Succeeded  bool      `json:"succeeded"`
	Output     string    `json:"output"`
	Truncated  bool      `json:"truncated,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMs int64     `json:"durationMs"`
}
