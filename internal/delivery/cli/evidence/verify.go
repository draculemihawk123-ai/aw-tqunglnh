package evidence

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"evidence", "verify"}, Scope: cli.ScopeProject,
		AppOperation: "VerifyEvidence", HTTPOperationID: cli.CLILocalOperation,
	})
}

// ArtifactVerification is one Artifact's own pass/fail outcome within an
// `evidence verify` run — this task's own "per-artifact pass/fail summary"
// line: just the artifact ID and a verified/tampered status, never a
// locator, never the artifact's own content bytes. Reason is populated only
// on failure: a resolve/authorization failure (runtime.
// ResolveEvidenceArtifactContent's own errors are always ID-shaped,
// scopeMismatch/ports.ErrPersistenceNotFound — never a Locator, see that
// function's own doc comment) surfaces its own .Error() text verbatim; a
// real tamper (ports.ArtifactStore.Verify's own re-hash mismatch) is passed
// through sanitizeStoreError first — that primitive's own concrete
// implementation is confirmed to embed the real Locator/filesystem path
// directly into its plain error text (storeerror.go's own doc comment), so
// this field must never carry that text unsanitized.
type ArtifactVerification struct {
	ArtifactID string `json:"artifactId"`
	Verified   bool   `json:"verified"`
	Reason     string `json:"reason,omitempty"`
}

// VerifyResult is `aw evidence verify`'s own JSON result: Verdict is
// "VERIFIED" only when every one of evidenceID's own ArtifactReferences
// re-hashed correctly, or "TAMPERED" the moment even one fails — never a
// silent pass, per this task's own "never silently passing" Verify bullet.
// (runtimedomain.NewEvidence itself refuses to construct an Evidence row
// with zero ArtifactReferences, so the empty-list case never occurs against
// a real, durably-created Evidence row; verifyEvidenceArtifacts below still
// handles it correctly — VERIFIED, vacuously — rather than assuming it
// cannot happen.)
type VerifyResult struct {
	EvidenceID string                 `json:"evidenceId"`
	WorkItemID string                 `json:"workItemId"`
	Verdict    string                 `json:"verdict"`
	Artifacts  []ArtifactVerification `json:"artifacts"`
}

const (
	verifyVerdictVerified = "VERIFIED"
	verifyVerdictTampered = "TAMPERED"
)

// verifyEvidenceArtifacts is this task's own "genuine gap 1" orchestration:
// no existing function anywhere in this codebase re-verifies every Artifact
// a single Evidence row's own manifest references. It composes three
// already-real primitives, in the same order and for the same reason
// internal/delivery/httpapi/evidence/artifact.go's own
// handleGetArtifactContent already establishes for a single artifact
// (Resolve-then-Verify, auth before I/O): runtime.GetEvidence (reload +
// scope-check the Evidence row itself), then, for each of its own
// ArtifactReferences, runtime.ResolveEvidenceArtifactContent (the SAME
// centralized "artifactId must be one of THIS Evidence row's own
// references" guard every other artifact-content path in this codebase
// goes through — never reimplemented here) followed by
// ports.ArtifactStore.Verify (a real, local re-hash against the stored
// content — no network round trip, this task's own "typed CLI_LOCAL
// through an injected verifier" line: ArtifactStore is injected via
// Dependencies exactly like ArtifactStore.Open is for `artifact get`).
//
// This function lives in the DELIVERY layer (this package), never beside
// internal/app/runtime/queries.go, for the identical reason that file's own
// doc comment already gives for why it never touches ArtifactStore itself:
// real store I/O must happen outside any database transaction
// (docs/architecture/04-go-core-spec.md §11.1) — queries.go only ever
// resolves WHICH ports.ArtifactRef a caller is authorized to open/verify,
// never performs the verify itself. A single failed Resolve/Verify call
// never aborts the whole loop: every reference is still attempted, so one
// tampered artifact's own failure is reported ALONGSIDE every other
// artifact's own real pass/fail status, not hidden behind an early return.
func verifyEvidenceArtifacts(ctx context.Context, deps Dependencies, scope ports.CommandScope, workItemID, evidenceID string) (VerifyResult, error) {
	evidenceDetail, err := runtimeapp.GetEvidence(ctx, deps.UnitOfWork, scope, workItemID, evidenceID)
	if err != nil {
		return VerifyResult{}, err
	}

	result := VerifyResult{
		EvidenceID: evidenceDetail.EvidenceID, WorkItemID: evidenceDetail.WorkItemID,
		Verdict: verifyVerdictVerified, Artifacts: make([]ArtifactVerification, 0, len(evidenceDetail.ArtifactReferences)),
	}
	for _, artifactID := range evidenceDetail.ArtifactReferences {
		entry := ArtifactVerification{ArtifactID: artifactID}
		ref, _, resolveErr := runtimeapp.ResolveEvidenceArtifactContent(ctx, deps.UnitOfWork, scope, workItemID, evidenceID, artifactID)
		if resolveErr != nil {
			entry.Verified = false
			entry.Reason = resolveErr.Error()
		} else if verifyErr := deps.ArtifactStore.Verify(ctx, ref); verifyErr != nil {
			entry.Verified = false
			entry.Reason = sanitizeStoreError(verifyErr)
		} else {
			entry.Verified = true
		}
		if !entry.Verified {
			result.Verdict = verifyVerdictTampered
		}
		result.Artifacts = append(result.Artifacts, entry)
	}
	return result, nil
}

// RunEvidenceVerify implements `aw evidence verify <workItemId>
// <evidenceId> --project-id <id>` — this package's one CLI_LOCAL leaf (see
// this file's own top-of-file doc comment and doc.go for why): it composes
// verifyEvidenceArtifacts above, always writes the full VerifyResult to
// stdout FIRST (so an operator/script gets the complete per-artifact
// picture regardless of outcome), then, if the aggregate Verdict is
// TAMPERED, returns a plain (non-cli.UsageError) error so a future
// composition root's own cli.ExitCodeFor maps this invocation to
// cli.ExitFailure — mirroring internal/delivery/cli/health's own RunReady
// exactly (its own doc comment: "returns a non-nil ... error whenever the
// report is not ready, AFTER writing the report").
func RunEvidenceVerify(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("evidence verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw evidence verify <workItemId> <evidenceId> --project-id <projectId>")
	}
	workItemID, evidenceID := positional[0], positional[1]
	if strings.TrimSpace(workItemID) == "" || strings.TrimSpace(evidenceID) == "" {
		return usageErrorf("<workItemId> and <evidenceId> arguments are required")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}

	scope := ports.ProjectScope(*projectID)
	result, err := verifyEvidenceArtifacts(ctx, deps, scope, workItemID, evidenceID)
	if err != nil {
		return err
	}
	if encodeErr := cli.EncodeQueryResult(stdout, result); encodeErr != nil {
		return encodeErr
	}
	if result.Verdict != verifyVerdictVerified {
		failed := 0
		for _, a := range result.Artifacts {
			if !a.Verified {
				failed++
			}
		}
		return fmt.Errorf("evidence verify: %d of %d artifact(s) failed verification", failed, len(result.Artifacts))
	}
	return nil
}
