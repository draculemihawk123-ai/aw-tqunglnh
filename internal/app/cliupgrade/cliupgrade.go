// Package cliupgrade is V12-05's "Use the new CLI" operation (UX-05, ADR-048). When the provider CLI updates itself (Claude
// 2.1.316 at chochuyennha), every AGENT node pinned to the old AdapterBuildVersion is blocked ADAPTER_BUILD_DRIFT, and using the new
// CLI took four separate steps, three of them in a terminal: probe and register the new build, publish the workflow again so it pins
// that build, cancel the run, recreate the task. This package does them as one operator decision taken on the blocked node:
//
//  1. measure the executable the blocked node is pinned to, as it is now (adapterbuild.MeasureCurrent) and register that build;
//  2. publish a new version of the run's workflow definition whose AGENT nodes pinned to the old build pin the new one (every other
//     byte of the document is the published version's own);
//  3. optionally redo the work item with that newest version (workitemredo.Service.Recreate), when its run has finished.
//
// It never repins a running run (core requirement 16, ADR-011): the run that was blocked keeps its old pins. Step 3 is the
// existing recreate command, which refuses while the run is live; the result then says to cancel the run and redo.
// Every step is its own idempotent command derived from the caller's key, so a retried request replays what already happened.
package cliupgrade

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/workitemredo"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

var (
	// ErrNotDriftBlocked is returned when the node run is not blocked by an open ADAPTER_BUILD_DRIFT blocker of the work item.
	ErrNotDriftBlocked = errors.New("cliupgrade: the node run is not blocked by an adapter build drift")
	// ErrNoDrift is returned when the CLI measures exactly as the pinned build: nothing changed, retry the node instead.
	ErrNoDrift = errors.New("cliupgrade: the provider CLI still matches the pinned build; retry the blocked node instead")
	// ErrNoProvider is returned when this process has no executor for the pinned build's provider.
	ErrNoProvider = errors.New("cliupgrade: this process has no executor for the pinned provider")
)

// Deps is what the operation needs.
type Deps struct {
	UnitOfWork ports.UnitOfWork
	IDs        idsource.Source
	Agents     *agentregistry.Registry
	Redo       workitemredo.Service
}

// Request names the blocked node and what to do after publishing.
type Request struct {
	ProjectID  string
	WorkItemID string
	NodeRunID  string
	// Redo recreates the work item with the new workflow version when its run has finished.
	Redo bool
	// Reason is recorded on the recreate (required when Redo).
	Reason string
	// OutOfScope is the recreate's choice for files a previous run left outside the scope (KEEP, PARK, PARK_ALL).
	OutOfScope string
}

// Result is what happened.
type Result struct {
	PreviousAdapterBuildID string   `json:"previousAdapterBuildId"`
	AdapterBuildID         string   `json:"adapterBuildId"`
	AdapterBuildExisted    bool     `json:"adapterBuildExisted"`
	WorkflowDefinitionID   string   `json:"workflowDefinitionId"`
	WorkflowVersionID      string   `json:"workflowVersionId"`
	WorkflowVersionNumber  uint64   `json:"workflowVersionNumber"`
	RepinnedNodes          []string `json:"repinnedNodes"`
	// NewWorkItemID is set when the work item was redone.
	NewWorkItemID string `json:"newWorkItemId,omitempty"`
	// NextStep says what is left for the operator, in words; empty when nothing is.
	NextStep string `json:"nextStep,omitempty"`
}

type target struct {
	run      runtimedomain.WorkflowRun
	version  workflow.WorkflowVersion
	pinned   domainadapterbuild.Build
	nodeKey  string
	fields   definition.Fields
	versions int
}

// UseNewCLI runs the operation. See the package comment.
func UseNewCLI(ctx context.Context, deps Deps, cmd ports.Command, req Request) (Result, error) {
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.WorkItemID) == "" || strings.TrimSpace(req.NodeRunID) == "" {
		return Result{}, errors.New("cliupgrade: ProjectID, WorkItemID and NodeRunID are required")
	}
	if req.Redo && strings.TrimSpace(req.Reason) == "" {
		return Result{}, errors.New("cliupgrade: Reason is required to redo the work item")
	}
	t, err := load(ctx, deps.UnitOfWork, req)
	if err != nil {
		return Result{}, err
	}
	if deps.Agents == nil {
		return Result{}, ErrNoProvider
	}
	executor, _, err := deps.Agents.Resolve(ports.ProviderKey(t.pinned.Tuple().ProviderKey), agentregistry.Requirements{})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrNoProvider, err)
	}

	// 1. Measure and register (real I/O, outside any transaction).
	measured, err := adapterbuild.MeasureCurrent(ctx, executor, t.pinned)
	if err != nil {
		return Result{}, err
	}
	if id, err := measured.Tuple.ID(); err == nil && id == t.pinned.ID() {
		return Result{}, ErrNoDrift
	}
	installation := step(cmd, "probe")
	installation.Scope = ports.InstallationScope()
	token, err := adapterbuild.ProbeAdapterBuild(ctx, deps.UnitOfWork, installation, adapterbuild.ProbeRequest{
		ProviderKey: measured.Tuple.ProviderKey, ExecutablePath: measured.Tuple.ExecutablePath, ProtocolVersion: measured.Tuple.ProtocolVersion,
		CapabilityManifest: measured.Manifest, OS: measured.Tuple.OS, Toolchain: measured.Tuple.Toolchain, ConfigIdentity: measured.Tuple.ConfigIdentity,
	})
	if err != nil {
		return Result{}, fmt.Errorf("cliupgrade: probe the new build: %w", err)
	}
	register := step(cmd, "register")
	register.Scope = ports.InstallationScope()
	registered, err := adapterbuild.RegisterAdapterBuild(ctx, deps.UnitOfWork, register, adapterbuild.RegisterRequest{Token: token, CapabilityManifest: measured.Manifest})
	if err != nil {
		return Result{}, fmt.Errorf("cliupgrade: register the new build: %w", err)
	}
	newBuildID := registered.Build.ID()

	// 2. Publish the workflow again with the new pin.
	document, repinned := repin(t.version.Document(), t.pinned.ID(), newBuildID)
	definitionID := string(t.version.DefinitionID())
	wfDefinition := workflow.WorkflowDefinition{
		ID: t.version.DefinitionID(), Name: t.fields.Name, Status: t.fields.Status, Version: t.fields.Version,
	}
	if !t.fields.Scope.IsGlobal() {
		pid := *t.fields.Scope.ProjectID
		wfDefinition.ProjectID = &pid
	}
	publish := step(cmd, "publish")
	published, err := definitions.PublishDefinitionVersion(ctx, deps.UnitOfWork, publish, definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindWorkflow, WorkflowDefinition: wfDefinition,
		WorkflowRequest: workflow.PublishRequest{
			VersionID: workflow.WorkflowVersionID(deps.IDs.NewID()), VersionNumber: uint64(t.versions) + 1, Document: document,
			PublishedBy: cmd.Actor, PublishedAt: cmd.RequestedAt,
		},
	})
	if err != nil {
		return Result{}, fmt.Errorf("cliupgrade: publish %s with the new build: %w", definitionID, err)
	}
	result := Result{
		PreviousAdapterBuildID: t.pinned.ID(), AdapterBuildID: newBuildID, AdapterBuildExisted: registered.AlreadyExisted,
		WorkflowDefinitionID: definitionID, WorkflowVersionID: published.ID(), WorkflowVersionNumber: published.VersionNumber(),
		RepinnedNodes: repinned,
	}

	// 3. Redo with the newest version, when asked and allowed.
	if !req.Redo {
		result.NextStep = "the run keeps the old pin: cancel it, then Redo with Latest Version"
		return result, nil
	}
	recreated, err := deps.Redo.Recreate(ctx, step(cmd, "recreate"), workitemredo.RecreateRequest{
		ProjectID: req.ProjectID, WorkItemID: req.WorkItemID, Reason: req.Reason, CarryMessages: true, OutOfScope: req.OutOfScope,
	})
	if code := runtime.RedoRefusalCode(err); code == runtime.RedoRefusalRunInProgress {
		result.NextStep = "the new version is published; the run is still live with the old pin — cancel it, then Redo with Latest Version"
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("cliupgrade: redo the work item: %w", err)
	}
	result.NewWorkItemID = recreated.NewWorkItemID
	return result, nil
}

func load(ctx context.Context, uow ports.UnitOfWork, req Request) (target, error) {
	var t target
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
		if err != nil {
			return err
		}
		if t.run, err = tx.Runtime().GetWorkflowRun(ctx, string(nodeRun.RunID)); err != nil {
			return err
		}
		if string(t.run.ProjectID) != req.ProjectID || string(t.run.WorkItemID) != req.WorkItemID {
			return fmt.Errorf("%w: node run %s does not belong to work item %s", ports.ErrPersistenceNotFound, req.NodeRunID, req.WorkItemID)
		}
		blockers, err := tx.Work().ListWorkItemBlockersForWorkItem(ctx, req.WorkItemID)
		if err != nil {
			return err
		}
		drift := false
		for _, b := range blockers {
			if b.Type == workdomain.BlockerAdapterBuildDrift && b.State == workdomain.BlockerOpen && b.SourceNodeRunID == req.NodeRunID {
				drift = true
			}
		}
		if !drift {
			return ErrNotDriftBlocked
		}
		if t.version, err = tx.Definitions().GetWorkflowVersion(ctx, string(t.run.WorkflowVersionID)); err != nil {
			return err
		}
		var buildID string
		for _, node := range t.version.Document().Nodes {
			if node.Key == nodeRun.NodeKey && node.Agent != nil && node.Agent.AdapterBuildID != nil {
				buildID = *node.Agent.AdapterBuildID
			}
		}
		if buildID == "" {
			return fmt.Errorf("%w: node %s pins no adapter build", ErrNotDriftBlocked, nodeRun.NodeKey)
		}
		t.nodeKey = nodeRun.NodeKey
		if t.pinned, err = tx.AdapterBuilds().Get(ctx, buildID); err != nil {
			return err
		}
		if t.fields, err = tx.Definitions().GetDefinition(ctx, definition.KindWorkflow, string(t.version.DefinitionID())); err != nil {
			return err
		}
		versions, err := tx.Definitions().ListVersions(ctx, definition.KindWorkflow, string(t.version.DefinitionID()))
		if err != nil {
			return err
		}
		t.versions = len(versions)
		return nil
	})
	return t, err
}

// repin returns document with every AGENT node pinned to from pinned to to instead, and the keys of those nodes.
func repin(document workflow.WorkflowDocument, from, to string) (workflow.WorkflowDocument, []string) {
	var repinned []string
	for i := range document.Nodes {
		agent := document.Nodes[i].Agent
		if agent == nil || agent.AdapterBuildID == nil || *agent.AdapterBuildID != from {
			continue
		}
		cloned := *agent // Document() already returned a deep copy
		id := to
		cloned.AdapterBuildID = &id
		document.Nodes[i].Agent = &cloned
		repinned = append(repinned, document.Nodes[i].Key)
	}
	return document, repinned
}

func step(cmd ports.Command, name string) ports.Command {
	sub := cmd
	sub.ID = cmd.ID + "-" + name
	sub.IdempotencyKey = cmd.IdempotencyKey + "-" + name
	sub.Type = cmd.Type + "." + name
	return sub
}
