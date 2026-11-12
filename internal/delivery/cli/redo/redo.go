// Package redo is V11-10's CLI surface (ADR-041): `aw work-item retry`, `aw work-item recreate` and `aw work-item redo-preview`, the same
// operations as the HTTP routes retryWorkItem, recreateWorkItem and getWorkItemRedoPreview, over internal/app/workitemredo. A retry or a redo
// is one command: it sends the note, resolves the failed run's blockers, creates the replacement work item (for a redo) and starts the
// run in one transaction; nothing is left half done if one step is refused.
package redo

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/cliupgrade"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/workitemredo"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is what the subcommands need from the composition root.
type Dependencies struct {
	UoW     ports.UnitOfWork
	Service workitemredo.Service
	// Now overrides the envelope's time source; nil in production.
	Now func() time.Time
	// Agents (V12-05) measures the provider CLI for `work-item use-new-cli`.
	Agents *agentregistry.Registry
}

// Command types, byte-identical to the literals internal/delivery/httpapi/redo passes, so an HTTP call and a CLI call hash and replay
// the same way.
const (
	commandTypeRetry     = "RetryWorkItem"
	commandTypeRecreate  = "RecreateWorkItem"
	commandTypeUseNewCLI = "UseNewCli"
	appOpPreview         = "GetWorkItemRedoPreview"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "retry"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeRetry, HTTPOperationID: "retryWorkItem",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "recreate"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeRecreate, HTTPOperationID: "recreateWorkItem",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "use-new-cli"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeUseNewCLI, HTTPOperationID: "useNewCli",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "budget"}, Scope: cli.ScopeProject,
		AppOperation: "SetWorkItemBudget", HTTPOperationID: "setWorkItemBudget",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "queue"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeSetQueue, HTTPOperationID: "setWorkItemQueue",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "queue-show"}, Scope: cli.ScopeProject,
		AppOperation: appOpGetQueue, HTTPOperationID: "getWorkItemQueue",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "redo-preview"}, Scope: cli.ScopeProject,
		AppOperation: appOpPreview, HTTPOperationID: "getWorkItemRedoPreview",
	})
}

// The JSON the semantic hash covers: field names and order are the HTTP handlers' (hashed payload structs there).
type retryHash struct {
	WorkItemID string `json:"workItemId"`
	Note       string `json:"note"`
	OutOfScope string `json:"outOfScope"`
}

type recreateHash struct {
	WorkItemID    string `json:"workItemId"`
	Reason        string `json:"reason"`
	Note          string `json:"note"`
	CarryMessages bool   `json:"carryMessages"`
	OutOfScope    string `json:"outOfScope"`
}

func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(cli.Interspersed(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}

func loadPrincipal(path string) (config.LocalPrincipal, error) {
	principal, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		return config.LocalPrincipal{}, err
	}
	if err := config.ValidateLocalPrincipal(principal); err != nil {
		return config.LocalPrincipal{}, err
	}
	return principal, nil
}

// loadWorkItemProjectID reloads the work item read-only for its project: the stored row is the only source of the scope.
func loadWorkItemProjectID(ctx context.Context, uow ports.UnitOfWork, workItemID string) (string, error) {
	var projectID string
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		projectID = string(item.ProjectID)
		return nil
	})
	return projectID, err
}

// Retry implements `aw work-item retry <workItemId> [--note <text>] [--out-of-scope KEEP|PARK|PARK_ALL]`.
func Retry(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item retry", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	note := fs.String("note", "", "a message for the agent, sent before the new run starts (optional)")
	outOfScope := fs.String("out-of-scope", "", "KEEP or PARK files a previous run left outside the work item's scope (required when there are any), or PARK_ALL to park every uncommitted change")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return usageErrorf("usage: aw work-item retry <workItemId> [--note <text>] [--out-of-scope KEEP|PARK]")
	}
	workItemID := fs.Arg(0)
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	projectID, err := loadWorkItemProjectID(ctx, deps.UoW, workItemID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(retryHash{WorkItemID: workItemID, Note: *note, OutOfScope: *outOfScope})
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRetry, Scope: ports.ProjectScope(projectID),
		NormalizedPayload: payload, IdempotencyKey: *idempotencyKey, IDSource: deps.Service.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return deps.Service.Retry(ctx, envelope.Command, workitemredo.RetryRequest{
			ProjectID: projectID, WorkItemID: workItemID, Note: *note, OutOfScope: *outOfScope,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// Recreate implements `aw work-item recreate <workItemId> --reason <text> [--note <text>] [--carry-messages=false] [--out-of-scope KEEP|PARK|PARK_ALL]`.
func Recreate(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item recreate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	reason := fs.String("reason", "", "why the work item is redone (required; recorded on the cancellation and the link)")
	note := fs.String("note", "", "an extra message for the new work item's agent (optional)")
	carry := fs.Bool("carry-messages", true, "copy the operator's messages to the new work item (default true; --carry-messages=false to start clean)")
	outOfScope := fs.String("out-of-scope", "", "KEEP or PARK files a previous run left outside the work item's scope (required when there are any), or PARK_ALL to park every uncommitted change")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return usageErrorf("usage: aw work-item recreate <workItemId> --reason <text> [--note <text>] [--carry-messages=false] [--out-of-scope KEEP|PARK]")
	}
	if strings.TrimSpace(*reason) == "" {
		return usageErrorf("--reason is required")
	}
	workItemID := fs.Arg(0)
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	projectID, err := loadWorkItemProjectID(ctx, deps.UoW, workItemID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(recreateHash{WorkItemID: workItemID, Reason: *reason, Note: *note, CarryMessages: *carry, OutOfScope: *outOfScope})
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRecreate, Scope: ports.ProjectScope(projectID),
		NormalizedPayload: payload, IdempotencyKey: *idempotencyKey, IDSource: deps.Service.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return deps.Service.Recreate(ctx, envelope.Command, workitemredo.RecreateRequest{
			ProjectID: projectID, WorkItemID: workItemID, Reason: *reason, Note: *note, CarryMessages: *carry, OutOfScope: *outOfScope,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

type useNewCLIHash struct {
	WorkItemID string `json:"workItemId"`
	NodeRunID  string `json:"nodeRunId"`
	Redo       bool   `json:"redo"`
	Reason     string `json:"reason"`
	OutOfScope string `json:"outOfScope"`
}

// UseNewCLI implements `aw work-item use-new-cli <workItemId> --node-run-id <id> [--redo --reason <text>] [--out-of-scope ...]`
// (V12-05, ADR-048): measure the provider CLI the drift-blocked node is pinned to, register that build, publish the workflow
// again pinning it, and with --redo redo the work item on that version.
func UseNewCLI(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item use-new-cli", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	nodeRunID := fs.String("node-run-id", "", "the node run blocked by ADAPTER_BUILD_DRIFT (required)")
	redoFlag := fs.Bool("redo", false, "also redo the work item with the new workflow version once its run has finished")
	reason := fs.String("reason", "", "why the work item is redone (required with --redo)")
	outOfScope := fs.String("out-of-scope", "", "with --redo: KEEP, PARK or PARK_ALL for files a previous run left in the worktree")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(*nodeRunID) == "" {
		return usageErrorf("usage: aw work-item use-new-cli <workItemId> --node-run-id <nodeRunId> [--redo --reason <text>] [--out-of-scope KEEP|PARK|PARK_ALL]")
	}
	if *redoFlag && strings.TrimSpace(*reason) == "" {
		return usageErrorf("--reason is required with --redo")
	}
	workItemID := fs.Arg(0)
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	projectID, err := loadWorkItemProjectID(ctx, deps.UoW, workItemID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(useNewCLIHash{WorkItemID: workItemID, NodeRunID: *nodeRunID, Redo: *redoFlag, Reason: *reason, OutOfScope: *outOfScope})
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeUseNewCLI, Scope: ports.ProjectScope(projectID),
		NormalizedPayload: payload, IdempotencyKey: *idempotencyKey, IDSource: deps.Service.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return cliupgrade.UseNewCLI(ctx, cliupgrade.Deps{UnitOfWork: deps.UoW, IDs: deps.Service.IDs, Agents: deps.Agents, Redo: deps.Service},
			envelope.Command, cliupgrade.Request{
				ProjectID: projectID, WorkItemID: workItemID, NodeRunID: *nodeRunID, Redo: *redoFlag, Reason: *reason, OutOfScope: *outOfScope,
			})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// commandTypeSetQueue and appOpGetQueue name the V12-19 queue operations (HTTP setWorkItemQueue / getWorkItemQueue).
const (
	commandTypeSetQueue = "SetWorkItemQueue"
	appOpGetQueue       = "GetFamilyQueue"
)

// setQueueWire mirrors internal/delivery/httpapi/redo's SetQueueRequest, and setQueueHash its request hash, so the HTTP route and
// this command replay identically.
type setQueueWire struct {
	Queued             bool   `json:"queued"`
	Position           *int   `json:"position,omitempty"`
	WaitsForWorkItemID string `json:"waitsForWorkItemId,omitempty"`
}

type setQueueHash struct {
	WorkItemID string       `json:"workItemId"`
	Body       setQueueWire `json:"body"`
}

// Queue implements `aw work-item queue <workItemId> [--waits-for <workItemId>] [--position <n>] [--remove]` (V12-19, ADR-054): put
// the work item in its family's queue (the worker starts it when its turn comes) or take it out.
func Queue(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item queue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	waitsFor := fs.String("waits-for", "", "start only once this work item of the same family is DONE")
	position := fs.Int("position", 0, "place in the family queue (lower first); 0 = last")
	remove := fs.Bool("remove", false, "take the work item out of the queue")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return usageErrorf("usage: aw work-item queue <workItemId> [--waits-for <workItemId>] [--position <n>] [--remove]")
	}
	workItemID := fs.Arg(0)
	wire := setQueueWire{Queued: !*remove, WaitsForWorkItemID: *waitsFor}
	if *position > 0 {
		p := *position
		wire.Position = &p
	}
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	projectID, err := loadWorkItemProjectID(ctx, deps.UoW, workItemID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(setQueueHash{WorkItemID: workItemID, Body: wire})
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeSetQueue, Scope: ports.ProjectScope(projectID),
		NormalizedPayload: payload, IdempotencyKey: *idempotencyKey, IDSource: deps.Service.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return runtime.SetWorkItemQueue(ctx, deps.UoW, envelope.Command, runtime.SetQueueRequest{
			ProjectID: projectID, WorkItemID: workItemID, Queued: wire.Queued, Position: wire.Position, WaitsForWorkItemID: wire.WaitsForWorkItemID,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

type setBudgetWire struct {
	BudgetUSD *float64 `json:"budgetUsd,omitempty"`
}

type setBudgetHash struct {
	WorkItemID string        `json:"workItemId"`
	Body       setBudgetWire `json:"body"`
}

// Budget implements `aw work-item budget <rootWorkItemId> --usd <amount> | --clear` (V12-23, ADR-057): the spending cap of a batch.
func Budget(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item budget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	usd := fs.Float64("usd", 0, "the cap in US dollars (as the provider CLI reports cost)")
	clear := fs.Bool("clear", false, "remove the budget")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" || (*usd <= 0) == !*clear {
		return usageErrorf("usage: aw work-item budget <rootWorkItemId> (--usd <amount> | --clear)")
	}
	workItemID := fs.Arg(0)
	wire := setBudgetWire{}
	if !*clear {
		v := *usd
		wire.BudgetUSD = &v
	}
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	projectID, err := loadWorkItemProjectID(ctx, deps.UoW, workItemID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(setBudgetHash{WorkItemID: workItemID, Body: wire})
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: "SetWorkItemBudget", Scope: ports.ProjectScope(projectID),
		NormalizedPayload: payload, IdempotencyKey: *idempotencyKey, IDSource: deps.Service.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return runtime.SetWorkItemBudget(ctx, deps.UoW, envelope.Command, projectID, workItemID, wire.BudgetUSD)
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// QueueShow implements `aw work-item queue-show <workItemId>`: the queue of the work item's family and what it waits for.
func QueueShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item queue-show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return usageErrorf("usage: aw work-item queue-show <workItemId>")
	}
	workItemID := fs.Arg(0)
	projectID, err := loadWorkItemProjectID(ctx, deps.UoW, workItemID)
	if err != nil {
		return err
	}
	var familyID string
	if err := deps.UoW.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		familyID = string(item.FamilyID)
		return err
	}); err != nil {
		return err
	}
	queue, err := runtime.GetFamilyQueue(ctx, deps.UoW, projectID, familyID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, queue)
}

// Preview implements `aw work-item redo-preview <workItemId>`: what a retry or a redo would do and why one would refuse.
func Preview(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item redo-preview", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return usageErrorf("usage: aw work-item redo-preview <workItemId>")
	}
	workItemID := fs.Arg(0)
	projectID, err := loadWorkItemProjectID(ctx, deps.UoW, workItemID)
	if err != nil {
		return err
	}
	preview, err := deps.Service.Preview(ctx, projectID, workItemID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, preview)
}
