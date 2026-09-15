package evidence

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleListArtifacts implements
// GET /projects/{projectId}/work-items/{workItemId}/evidence/{evidenceId}/artifacts
// (operationId listArtifacts): the bounded metadata for every Artifact
// evidenceId's own ArtifactReferences names — this task's own "artifact
// inventory" line. See internal/app/runtime/queries.go's own top-of-file
// doc comment for why this is evidence-scoped rather than WorkItem-scoped
// (Artifact carries no WorkItem/Run column of its own). Every returned
// entry deliberately never carries a Locator — a caller fetches actual
// bytes through getArtifactContent below, by Artifact ID only.
func handleListArtifacts(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		evidenceID := r.PathValue("evidenceId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		if strings.TrimSpace(evidenceID) == "" {
			writeValidationError(w, "evidenceId", "is required")
			return
		}
		items, err := runtimeapp.ListArtifactsForEvidence(ctx, deps.UnitOfWork, ports.ProjectScope(projectID), workItemID, evidenceID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, listArtifactsResponse{Items: items}, "")
	}
}

// handleGetArtifactContent implements
// GET /projects/{projectId}/work-items/{workItemId}/evidence/{evidenceId}/artifacts/{artifactId}/content
// (operationId getArtifactContent): streams one Artifact's own real,
// content-addressed bytes — this task's own "GetArtifactContent" line.
//
// Authorization happens ENTIRELY before any ArtifactStore call:
// runtimeapp.ResolveEvidenceArtifactContent reloads the owning WorkItem,
// then evidenceId (scope-checked against both), then confirms artifactId is
// actually one of THAT Evidence row's own ArtifactReferences (never any
// other Artifact ID, even a real one this caller could otherwise see) —
// only then does it reload the Artifact row itself and hand back a real
// ports.ArtifactRef built from that row's own stored columns. The caller
// never supplies, and this handler never learns from the caller, a
// Locator/filesystem path (this task's own "Không làm: KHÔNG expose
// locator" line).
//
// Once authorized: Verify re-hashes the stored content BEFORE Open ever
// returns a reader (internal/adapters/artifactstore's own "verify-open as
// one guarantee" — a caller can never read tampered bytes without an
// error); Open's returned reader is streamed directly to the response via
// io.Copy/io.CopyN — never buffered whole into memory — so an
// arbitrarily large artifact never risks this process's own memory budget.
// A `Range` header is honored via httpapi.ParseRange/ApplyPartialContentHeaders
// (V6-02A, media.go); Content-Type/nosniff/Content-Disposition are set via
// httpapi.ApplyContentHeaders, whose own closed inline-safe allow-list
// forces anything not on it (text/html, image/svg+xml, ...) to download
// rather than render inline — this task's own "KHÔNG trusted HTML" line.
// The ETag is the artifact's own real content hash, never a version
// counter — a caller can use it to detect whether cached content is still
// byte-identical to what this server would serve today.
//
// Sensitivity redaction needs no extra step here: internal/app/message's
// own AppendMessage command (and any other real PrepareAttachment caller)
// already redacts BEFORE ever calling ports.ArtifactStore.Put — the bytes
// this handler streams back are exactly, and only, whatever was actually
// durably stored, which for a SECRET/SENSITIVE artifact is already the
// redacted form (see internal/app/message/commands.go's own "Redact BEFORE
// Put" ordering) — this route would misrepresent reality if it tried to
// redact AGAIN at read time.
func handleGetArtifactContent(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		evidenceID := r.PathValue("evidenceId")
		artifactID := r.PathValue("artifactId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		if strings.TrimSpace(evidenceID) == "" {
			writeValidationError(w, "evidenceId", "is required")
			return
		}
		if strings.TrimSpace(artifactID) == "" {
			writeValidationError(w, "artifactId", "is required")
			return
		}

		ref, _, err := runtimeapp.ResolveEvidenceArtifactContent(ctx, deps.UnitOfWork, ports.ProjectScope(projectID), workItemID, evidenceID, artifactID)
		if err != nil {
			writeQueryError(w, err)
			return
		}

		if err := deps.ArtifactStore.Verify(ctx, ref); err != nil {
			writeContentError(w, err)
			return
		}
		reader, err := deps.ArtifactStore.Open(ctx, ref)
		if err != nil {
			writeContentError(w, err)
			return
		}
		defer reader.Close()

		rng, present, rangeErr := httpapi.ParseRange(r.Header.Get("Range"), ref.Size)
		if rangeErr != nil {
			httpapi.WriteRangeNotSatisfiable(w, ref.Size)
			return
		}

		httpapi.ApplyContentHeaders(w, ref.ContentType, artifactID)
		w.Header().Set("ETag", fmt.Sprintf("%q", ref.SHA256))

		if present {
			if err := skipBytes(reader, rng.Start); err != nil {
				httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
				return
			}
			httpapi.ApplyPartialContentHeaders(w, rng, ref.Size)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.CopyN(w, reader, rng.Length())
			return
		}

		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(ref.Size, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, reader)
	}
}

// skipBytes advances reader past its first n bytes before the caller starts
// copying a Range response — via Seek when reader supports it (the real
// filesystem artifactstore.Store's own Open returns an *os.File, which
// does), falling back to a bounded discard-copy otherwise so this handler
// works correctly against any ports.ArtifactStore implementation, not only
// the production one.
func skipBytes(reader io.Reader, n int64) error {
	if n == 0 {
		return nil
	}
	if seeker, ok := reader.(io.Seeker); ok {
		_, err := seeker.Seek(n, io.SeekStart)
		return err
	}
	_, err := io.CopyN(io.Discard, reader, n)
	return err
}
