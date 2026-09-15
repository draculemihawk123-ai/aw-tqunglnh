package workspaceinspection

import (
	"fmt"
	gopath "path"
	"strconv"

	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Response metadata headers for handleGetSource — ports.SourceContent's own
// bounds/binary/truncation fields (V6-10D's own "map bounds/binary/
// truncation ... onto real HTTP semantics" line) that do not fit the raw
// content body itself. Every one is a plain, already-bounded scalar — never
// caller-controlled free text — so none of them needs escaping/quoting
// beyond strconv's own formatting.
const (
	headerTotalBytes          = "X-Aw-Source-Total-Bytes"
	headerLineCount           = "X-Aw-Source-Line-Count"
	headerByteLimit           = "X-Aw-Source-Byte-Limit"
	headerLineLimit           = "X-Aw-Source-Line-Limit"
	headerTruncated           = "X-Aw-Source-Truncated"
	headerBinary              = "X-Aw-Source-Binary"
	headerRevision            = "X-Aw-Source-Revision"
	headerWorkspaceGeneration = "X-Aw-Source-Workspace-Generation"
)

// handleGetSource implements GET
// /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/source
// (operationId getWorkspaceSource): streams req.Path's exact blob content at
// req.Revision as the raw HTTP response body — NOT a JSON envelope — the
// identical "stream real bytes, Content-Type/nosniff/Content-Disposition via
// httpapi.ApplyContentHeaders" convention
// internal/delivery/httpapi/evidence/artifact.go's own getArtifactContent
// already established for V6-07B, reused verbatim rather than reinvented
// (V6-10D's own "media contracts" line): a client that wants to open,
// render, or download one file's own content at one revision gets exactly
// that, with the correct browser-safety headers, rather than a base64
// string buried inside a JSON object.
//
// ports.SourceContent's own bounds/binary/truncation fields — TotalBytes,
// LineCount, ByteLimit, LineLimit, Truncated, Binary — have no natural home
// in a raw byte-stream response body, so they are surfaced as the
// headerXxx response headers declared above instead (this package's own
// concrete answer to V6-10D's own "truncation flag -> maybe a response
// header" example line). Binary controls Content-Type/Content-Disposition
// exactly the way it controls ApplyContentHeaders' own inline-safe decision
// for an Artifact: "application/octet-stream" (never inline-safe, always
// downloads) for Binary, "text/plain; charset=utf-8" (inline-safe) for
// everything else — this package never attempts to sniff or otherwise infer
// a more specific MIME type for arbitrary repository source content.
func handleGetSource(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID, ok := requirePathParam(w, r, "projectId")
		if !ok {
			return
		}
		repositoryWorkspaceID, ok := requirePathParam(w, r, "repositoryWorkspaceId")
		if !ok {
			return
		}

		query := r.URL.Query()
		repositoryID, ok := requireQueryParam(w, query, "repositoryId")
		if !ok {
			return
		}
		workspaceSetID, ok := requireQueryParam(w, query, "workspaceSetId")
		if !ok {
			return
		}
		treePath, ok := requireQueryParam(w, query, "path")
		if !ok {
			return
		}
		revisionID, ok := requireQueryParam(w, query, "revision")
		if !ok {
			return
		}
		generation, ok := requireUint64QueryParam(w, query, "generation")
		if !ok {
			return
		}
		byteLimit, ok := optionalInt64QueryParam(w, query, "byteLimit")
		if !ok {
			return
		}
		lineLimit, ok := optionalInt64QueryParam(w, query, "lineLimit")
		if !ok {
			return
		}

		result, err := deps.Queries.GetSource(ctx, appinspection.GetSourceRequest{
			Scope: appinspection.WorkspaceScope{
				ProjectID: projectID, RepositoryID: repositoryID,
				WorkspaceSetID: workspaceSetID, RepositoryWorkspaceID: repositoryWorkspaceID,
			},
			Revision: workspace.Revision{
				RepositoryID: project.RepositoryID(repositoryID), VCSObjectID: revisionID, WorkspaceGeneration: generation,
			},
			Path: treePath, ByteLimit: byteLimit, LineLimit: lineLimit,
		})
		if err != nil {
			writeQueryError(w, err)
			return
		}

		writeSourceContent(w, result)
	}
}

func writeSourceContent(w http.ResponseWriter, result ports.SourceContent) {
	contentType := "text/plain; charset=utf-8"
	if result.Binary {
		contentType = "application/octet-stream"
	}
	filename := gopath.Base(result.Path)
	if filename == "" || filename == "." || filename == "/" {
		filename = "source"
	}

	w.Header().Set(headerTotalBytes, strconv.FormatInt(result.TotalBytes, 10))
	w.Header().Set(headerLineCount, strconv.FormatInt(result.LineCount, 10))
	w.Header().Set(headerByteLimit, strconv.FormatInt(result.ByteLimit, 10))
	w.Header().Set(headerLineLimit, strconv.FormatInt(result.LineLimit, 10))
	w.Header().Set(headerTruncated, strconv.FormatBool(result.Truncated))
	w.Header().Set(headerBinary, strconv.FormatBool(result.Binary))
	w.Header().Set(headerRevision, result.Revision.VCSObjectID)
	w.Header().Set(headerWorkspaceGeneration, strconv.FormatUint(result.Revision.WorkspaceGeneration, 10))

	httpapi.ApplyContentHeaders(w, contentType, filename)
	// ETag is content-addressed by revision+path — content at a fixed,
	// already-committed revision never changes, so this is always safe to
	// cache against, mirroring evidence/artifact.go's own real-content-hash
	// ETag (never a version counter here either, since GetSource has no
	// "version" concept of its own).
	w.Header().Set("ETag", fmt.Sprintf("%q", result.Revision.VCSObjectID+":"+result.Path))
	w.Header().Set("Content-Length", strconv.Itoa(len(result.Content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Content)
}
