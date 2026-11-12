package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/forgerelease"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// V12-28 (ADR-060): the remote of a repository workspace — push its branch, open a pull request, merge it — through the machine's own
// git credential helper and GitHub CLI.

type pushBranchBody struct {
	// RemoteBranch is the name on origin, only at the first push (default: the family branch).
	RemoteBranch string `json:"remoteBranch,omitempty"`
}

type createPullRequestBody struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Draft bool   `json:"draft,omitempty"`
}

type mergePullRequestBody struct {
	// Method is merge (default), squash or rebase.
	Method string `json:"method,omitempty"`
	// AcknowledgeNoChecks confirms "this pull request has no CI"; required when it has no check.
	AcknowledgeNoChecks bool `json:"acknowledgeNoChecks,omitempty"`
}

// RegisterForgeRoutes registers the four routes. git is the Git workspace provider (nil: the routes answer UNAVAILABLE), forge the
// forge client (nil: same).
func RegisterForgeRoutes(routes *RouteRegistry, uow ports.UnitOfWork, auditor ports.WorkspaceHeadAuditor, forge ports.ForgeClient) {
	git, _ := auditor.(ports.ForgeGit)
	deps := forgerelease.Deps{UnitOfWork: uow, Auditor: auditor, Git: git, Forge: forge}
	base := "/projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}"
	routes.Register(RouteDescriptor{
		Method: http.MethodGet, Path: base + "/remote", OperationID: "getWorkspaceRemote", ScopeKind: ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: forgerelease.RemoteView{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			view, err := forgerelease.GetRemote(r.Context(), deps, r.PathValue("projectId"), r.PathValue("repositoryWorkspaceId"))
			if err != nil {
				writeForgeError(w, err)
				return
			}
			_ = EncodeResult(w, http.StatusOK, view, "")
		},
	})
	routes.Register(RouteDescriptor{
		Method: http.MethodPost, Path: base + "/push", OperationID: "pushWorkspaceBranch", ScopeKind: ScopeProject,
		RequestSchema: pushBranchBody{}, ResponseSchema: forgerelease.PushResult{},
		Handler: forgeMutation("PushFamilyBranch", func() any { return &pushBranchBody{} }, func(r *http.Request, cmd ports.Command, body any) (any, error) {
			b := body.(*pushBranchBody)
			return forgerelease.Push(r.Context(), deps, cmd, forgerelease.PushRequest{
				ProjectID: r.PathValue("projectId"), RepositoryWorkspaceID: r.PathValue("repositoryWorkspaceId"), RemoteBranch: b.RemoteBranch,
			})
		}),
	})
	routes.Register(RouteDescriptor{
		Method: http.MethodPost, Path: base + "/pull-request", OperationID: "createWorkspacePullRequest", ScopeKind: ScopeProject,
		RequestSchema: createPullRequestBody{}, ResponseSchema: ports.PullRequest{},
		Handler: forgeMutation("CreateWorkspacePullRequest", func() any { return &createPullRequestBody{} }, func(r *http.Request, cmd ports.Command, body any) (any, error) {
			b := body.(*createPullRequestBody)
			return forgerelease.CreatePullRequest(r.Context(), deps, cmd, forgerelease.CreatePullRequestRequest{
				ProjectID: r.PathValue("projectId"), RepositoryWorkspaceID: r.PathValue("repositoryWorkspaceId"), Title: b.Title, Body: b.Body, Draft: b.Draft,
			})
		}),
	})
	routes.Register(RouteDescriptor{
		Method: http.MethodPost, Path: base + "/pull-request/merge", OperationID: "mergeWorkspacePullRequest", ScopeKind: ScopeProject,
		RequestSchema: mergePullRequestBody{}, ResponseSchema: forgerelease.MergeResult{},
		Handler: forgeMutation("MergeWorkspacePullRequest", func() any { return &mergePullRequestBody{} }, func(r *http.Request, cmd ports.Command, body any) (any, error) {
			b := body.(*mergePullRequestBody)
			return forgerelease.Merge(r.Context(), deps, cmd, forgerelease.MergeRequest{
				ProjectID: r.PathValue("projectId"), RepositoryWorkspaceID: r.PathValue("repositoryWorkspaceId"), Method: b.Method, AcknowledgeNoChecks: b.AcknowledgeNoChecks,
			})
		}),
	})
}

// forgeMutation is the mutating-route flow (Idempotency-Key, canonical body, request hash); the receipt is the application command's.
func forgeMutation(commandType string, newBody func() any, run func(r *http.Request, cmd ports.Command, body any) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := strings.TrimSpace(r.PathValue("projectId"))
		idempotencyKey, err := RequireIdempotencyKey(r)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error(), nil)
			return
		}
		body := newBody() // one per request: never shared between concurrent requests
		canonical, err := CanonicalizeJSON(r, maxWorkspaceCommandBodyBytes, body)
		if err != nil {
			WriteDecodeError(w, err)
			return
		}
		principal := PrincipalFromContext(r.Context())
		scope := ports.ProjectScope(projectID)
		hash := SemanticHash(commandType, scope, canonical, r.PathValue("repositoryWorkspaceId"), 0)
		cmd := newWorkspaceCommand(commandType, idempotencyKey, principal, scope, 0, hash)
		result, err := run(r, cmd, body)
		if err != nil {
			writeForgeError(w, err)
			return
		}
		_ = EncodeResult(w, http.StatusOK, result, "")
	}
}

func writeForgeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrForgeUnavailable):
		WriteAppError(w, apperror.New(errorcode.CodeUnavailable, err.Error(), false))
	case errors.Is(err, ports.ErrReceiptConflict):
		WriteError(w, http.StatusConflict, ErrorCodeConflict, "Idempotency-Key was already used for a request with a different body", nil)
	case errors.Is(err, forgerelease.ErrUnapprovedCommits), errors.Is(err, forgerelease.ErrBaseBranch), errors.Is(err, forgerelease.ErrBranchFixed),
		errors.Is(err, forgerelease.ErrNotPushed), errors.Is(err, forgerelease.ErrPullRequestExists), errors.Is(err, forgerelease.ErrNoPullRequest),
		errors.Is(err, forgerelease.ErrPullRequestNotOpen), errors.Is(err, forgerelease.ErrHeadMoved), errors.Is(err, forgerelease.ErrChecksNotGreen),
		errors.Is(err, forgerelease.ErrNoChecksNotAcked), errors.Is(err, forgerelease.ErrConflicting), errors.Is(err, forgerelease.ErrHeadNotRecorded),
		errors.Is(err, forgerelease.ErrNotReady), errors.Is(err, forgerelease.ErrWriterActive), errors.Is(err, ports.ErrRemoteBranchExists),
		errors.Is(err, ports.ErrNoRemote):
		WriteError(w, http.StatusConflict, ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, forgerelease.ErrNoTitle):
		WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error(), []ErrorDetail{{Field: "title", Message: "is required"}})
	case errors.Is(err, forgerelease.ErrInvalidMethod):
		WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error(), []ErrorDetail{{Field: "method", Message: "must be merge, squash or rebase"}})
	default:
		writeWorkspaceCommandError(w, err)
	}
}
