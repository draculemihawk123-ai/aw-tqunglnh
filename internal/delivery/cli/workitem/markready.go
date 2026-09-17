package workitem

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunWorkItemMarkReady implements
// `aw work-item mark-ready <workItemId> --expected-version <version>` — a
// mutation over workapp.MarkWorkItemReady, the single narrow command that
// closes BACKLOG->READY (ADR-028 §30: "Command không nhận target status" —
// workapp.MarkWorkItemReadyRequest carries only a WorkItemID, nothing this
// leaf's own flags could even attempt to name a different transition
// with).
//
// Deliberately no --project-id flag: mirroring
// internal/delivery/httpapi/workitem/mark_ready_command.go's own route
// shape exactly (that route has no {projectId} path segment at all — "the
// design doc's own route fragment is verbatim
// POST /work-items/{id}/mark-ready"), ProjectID is derived SOLELY by
// reloading the WorkItem's own real, stored row (loadWorkItemRaw,
// helpers.go) — contract point 3's "không tin ID shape, payload hoặc
// projection" satisfied the same way that HTTP handler already is.
//
// --expected-version is required (cli.BindExpectedVersionFlag's own "CLI
// equivalent of HTTP's If-Match"), following
// internal/delivery/cli/catalog/repository.go's own RunRepositoryRetryProbe
// template exactly: the version comparison against the freshly reloaded
// WorkItem lives INSIDE the cli.Dispatch execute closure, reached only when
// no receipt already exists for this exact idempotency key — a genuinely
// fresh (non-replayed) attempt observing a stale --expected-version is
// rejected (this task's own "stale" Verify line), while a replay of an
// earlier successful mark-ready always returns the original accepted
// result regardless of what version the WorkItem has moved to since. The
// real, authoritative CAS still lives one level deeper, inside
// workapp.MarkWorkItemReady itself (TransitionWorkItemStatus's own
// ExpectedStatus=BACKLOG/ExpectedVersion=<freshly reloaded inside that
// command's own transaction> CAS) — this leaf's own version check is a
// fast-reject only, exactly like RunRepositoryRetryProbe's own "is this
// BLOCKED" precondition check is not authoritative on its own. That
// authoritative CAS is also what gives a concurrent double `mark-ready`
// race exactly one winner (this task's own "concurrency" Verify line): two
// concurrent calls can both pass this leaf's own fast-reject check, but
// only one of workapp.MarkWorkItemReady's own two resulting
// TransitionWorkItemStatus calls can win the underlying optimistic-
// concurrency CAS.
func RunWorkItemMarkReady(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item mark-ready", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw work-item mark-ready <workItemId> --expected-version <version>")
	}
	workItemID := positional[0]
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the WorkItem version you observed, the CLI equivalent of HTTP's If-Match)")
	}

	item, err := loadWorkItemRaw(ctx, deps.UoW, workItemID)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	scope := ports.ProjectScope(string(item.ProjectID))
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeMarkWorkItemReady, Scope: scope,
		NormalizedPayload: []byte("{}"), ExpectedVersion: *expectedVersion,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		// Route-level fast-reject only, not authoritative — see this
		// function's own doc comment.
		if item.Version != *expectedVersion {
			return nil, fmt.Errorf("work item %s is at version %d, not the expected %d — reload and retry", workItemID, item.Version, *expectedVersion)
		}
		return workapp.MarkWorkItemReady(ctx, deps.UoW, envelope.Command, workapp.MarkWorkItemReadyRequest{WorkItemID: workItemID})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
