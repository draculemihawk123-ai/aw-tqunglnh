package evidence

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"artifact", "get"}, Scope: cli.ScopeProject,
		AppOperation: "ResolveEvidenceArtifactContent", HTTPOperationID: "getArtifactContent",
	})
}

// RunArtifactGet implements `aw artifact get <workItemId> <evidenceId>
// <artifactId> --project-id <id> --output <path|->` — this task's own
// "genuine gap 2" leaf: a query-shaped command whose own result IS a real
// byte stream, never a JSON document, so it cannot follow
// cli.EncodeQueryResult's own "one JSON document on stdout" contract the
// way every other leaf in this package does.
//
// Authorization and integrity mirror
// internal/delivery/httpapi/evidence/artifact.go's own
// handleGetArtifactContent exactly, in the exact same order: (1)
// runtime.ResolveEvidenceArtifactContent resolves and authorizes ref
// ENTIRELY server-side — artifactId must be one of evidenceId's own
// ArtifactReferences, never merely a real artifact in the same project
// (that check is centralized in ResolveEvidenceArtifactContent itself,
// never reimplemented here); (2) deps.ArtifactStore.Verify re-hashes the
// stored content BEFORE (3) deps.ArtifactStore.Open ever returns a reader
// — a tampered artifact is caught and returned as an error with NO output
// target ever created/written to, exactly like the HTTP route's own
// "verify-open as one guarantee" (that file's own doc comment). Only once
// all three succeed does cli.WriteBinaryOutput ever begin streaming, via
// io.Copy — never buffered whole into memory, matching that same
// handler's own streaming discipline.
//
// Output routing: when --output is "-", the artifact's raw bytes are
// written directly to stdout and NOTHING else is ever written there (no
// JSON wrapper, no trailing summary) — this task's own "binary stdout"
// Verify bullet requires an exact byte-for-byte stream, which a mixed-in
// JSON document would corrupt. The artifact's own bounded metadata
// (runtimeapp.ArtifactSummary — sensitivity/redacted/contentHash/size/
// mediaType, never a Locator) is instead written to stderr as one
// diagnostic line, so an operator piping stdout to a file still sees it.
// When --output names a real file, that metadata is instead the one JSON
// document written to stdout via cli.EncodeQueryResult (stdout is free in
// this branch, since the content itself went to the file) — this is the
// "dispatch via cli.EncodeQueryResult-style read-only flow" this task's own
// brief names for the non-stdout-content case.
func RunArtifactGet(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("artifact get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	output := cli.BindOutputFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	positional := fs.Args()
	if len(positional) != 3 {
		return usageErrorf("usage: aw artifact get <workItemId> <evidenceId> <artifactId> --project-id <projectId> --output <path|->")
	}
	workItemID, evidenceID, artifactID := positional[0], positional[1], positional[2]
	if strings.TrimSpace(workItemID) == "" || strings.TrimSpace(evidenceID) == "" || strings.TrimSpace(artifactID) == "" {
		return usageErrorf("<workItemId>, <evidenceId> and <artifactId> arguments are required")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	if strings.TrimSpace(*output) == "" {
		return usageErrorf("--output is required (a real file path, or - for stdout)")
	}

	scope := ports.ProjectScope(*projectID)
	ref, summary, err := runtimeapp.ResolveEvidenceArtifactContent(ctx, deps.UnitOfWork, scope, workItemID, evidenceID, artifactID)
	if err != nil {
		return err
	}

	// Verify BEFORE Open — a tampered artifact is caught here, before this
	// function ever creates/opens the --output target, never partially
	// written. Both errors are routed through sanitizeStoreError:
	// ArtifactStore's own concrete implementation embeds the real
	// Locator/filesystem path directly into its plain error text (see that
	// function's own doc comment) — a future composition root prints a
	// returned error's own .Error() text to stderr, so this leaf must never
	// hand it a Locator/path-bearing error, exactly like the JSON-facing
	// path in verify.go.
	if err := deps.ArtifactStore.Verify(ctx, ref); err != nil {
		return errors.New(sanitizeStoreError(err))
	}
	reader, err := deps.ArtifactStore.Open(ctx, ref)
	if err != nil {
		return errors.New(sanitizeStoreError(err))
	}
	defer reader.Close()

	if *output == "-" {
		// Content IS stdout in this branch — metadata goes to stderr
		// instead, never mixed into the same byte stream.
		cli.Diagnosticf(stderr, "artifactId=%s contentHash=%s size=%d mediaType=%s sensitivity=%s redacted=%t",
			summary.ArtifactID, summary.ContentHash, summary.Size, summary.MediaType, summary.Sensitivity, summary.Redacted)
		if _, err := cli.WriteBinaryOutput(stdout, *output, reader); err != nil {
			return err
		}
		return nil
	}

	if _, err := cli.WriteBinaryOutput(stdout, *output, reader); err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, summary)
}
