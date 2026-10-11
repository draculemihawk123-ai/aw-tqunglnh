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

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workitemredo"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is what the subcommands need from the composition root.
type Dependencies struct {
	UoW     ports.UnitOfWork
	Service workitemredo.Service
	// Now overrides the envelope's time source; nil in production.
	Now func() time.Time
}

// Command types, byte-identical to the literals internal/delivery/httpapi/redo passes, so an HTTP call and a CLI call hash and replay
// the same way.
const (
	commandTypeRetry    = "RetryWorkItem"
	commandTypeRecreate = "RecreateWorkItem"
	appOpPreview        = "GetWorkItemRedoPreview"
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
	if err := fs.Parse(args); err != nil {
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

// Retry implements `aw work-item retry <workItemId> [--note <text>] [--out-of-scope KEEP|PARK]`.
func Retry(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item retry", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	note := fs.String("note", "", "a message for the agent, sent before the new run starts (optional)")
	outOfScope := fs.String("out-of-scope", "", "KEEP or PARK files a previous run left outside the work item's scope (required when there are any)")
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

// Recreate implements `aw work-item recreate <workItemId> --reason <text> [--note <text>] [--carry-messages=false] [--out-of-scope KEEP|PARK]`.
func Recreate(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item recreate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	reason := fs.String("reason", "", "why the work item is redone (required; recorded on the cancellation and the link)")
	note := fs.String("note", "", "an extra message for the new work item's agent (optional)")
	carry := fs.Bool("carry-messages", true, "copy the operator's messages to the new work item (default true; --carry-messages=false to start clean)")
	outOfScope := fs.String("out-of-scope", "", "KEEP or PARK files a previous run left outside the work item's scope (required when there are any)")
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
