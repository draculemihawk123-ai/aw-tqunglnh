// Authoritative Evidence/Artifact/ContextSnapshot read queries (V6-07B,
// docs/design/08-v6-api-projections.md V6-07B: "evidence list/detail/verify
// metadata, artifact inventory, GetArtifactContent, ContextSnapshot
// detail"). Every function in this file is read-only (uow.WithReadOnly,
// never WithSerializedWrite) and returns an HTTP-ready DTO — never a raw
// domain value, never a raw ports.ArtifactRef.Locator — the same "command
// Result struct with json tags lives beside the command that produces it"
// convention internal/app/work/queries.go (V6-04) already establishes for a
// query instead of a command, reused here per this task's own placement
// instruction (evidence/checkpoint/execution lineage all already live in
// this package's own domain, internal/domain/runtime).
//
// Every query here is project-scoped, mirroring internal/app/work/queries.go's
// own identical enforcement: a request naming the installation scope, or a
// project scope that does not match the loaded WorkItem's own real, stored
// ProjectID, is ports.ErrScopeMismatch — which the HTTP layer (this task's
// own internal/delivery/httpapi/evidence package) treats identically to
// ports.ErrPersistenceNotFound, V6-02A's own leakage-normalization policy.
//
// Every query reloads its own owning row from a real, already-existing
// production accessor — ports.Tx.Work()/Runtime()/Artifacts()/
// ContextSnapshots() — never trusting a client-supplied scope claim, and
// never touching ports.ArtifactStore itself (that is HTTP-layer streaming
// concern, since it needs a real Open/Verify I/O call outside any Tx —
// docs/architecture/04-go-core-spec.md §11.1's own "không gọi filesystem
// artifact store trong transaction" — this file only ever resolves WHICH
// ports.ArtifactRef a caller is authorized to open, never opens it itself).
//
// Artifact-inventory decision (recorded here since it was the one genuine
// domain-model investigation this task required, per its own instructions
// — see internal/domain/artifact/artifact.go's own Artifact struct, read in
// full before deciding this): an Artifact row carries NO WorkItemID/RunID/
// NodeRunID/MessageID column of its own — its only real ownership field is
// ProjectID. There is therefore no domain-modeled "artifacts owned by this
// WorkItem" query the database could ever answer directly; the ONLY real,
// already-durable link a WorkItem-scoped caller has to a specific set of
// Artifact IDs is an Evidence row's own ArtifactReferences (evidence.go's
// own "every durable Artifact this Evidence row's own manifest depends
// on"). ListArtifactsForEvidence below is therefore evidence-scoped, not
// WorkItem-scoped: "artifact inventory" for one Evidence row, reloading
// that Evidence row first (this task's own "Thực hiện: reload owning
// WorkItem/Run/Evidence/Message" line, satisfied literally). A
// WorkItem-wide artifact inventory across every Evidence row (or every
// Message attachment) is a legitimate future extension once a real UI
// caller needs it, but is NOT invented here against no real caller
// (00-roadmap.md §3's own "không chia chỉ để tạo file/field nếu phần đó
// chưa có contract test hoặc behavior quan sát được").
package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// requireProjectScope mirrors internal/app/work/queries.go's own identical
// helper (private to that package) — every query in this file needs the
// same precondition before it ever touches storage.
func requireProjectScope(scope ports.CommandScope) (string, error) {
	projectID, ok := scope.ProjectID()
	if !ok {
		return "", fmt.Errorf("runtime: %w: this query is project-scoped, not installation-scoped", ports.ErrScopeMismatch)
	}
	return projectID, nil
}

// scopeMismatch wraps ports.ErrScopeMismatch with a message naming what was
// loaded but rejected — mirrors internal/app/work/queries.go's own
// identical helper.
func scopeMismatch(kind, id string) error {
	return fmt.Errorf("runtime: %w: %s %s belongs to another project/work item", ports.ErrScopeMismatch, kind, id)
}

// verifyWorkItemInScope reloads workItemID and confirms it really belongs
// to projectID — every query below calls this FIRST, unconditionally,
// before ever touching Evidence/Artifact/ContextSnapshot storage, so a
// caller can never learn anything about a real Evidence/Artifact/
// ContextSnapshot row by supplying a WorkItemID that merely happens to
// parse, or that belongs to a different project.
func verifyWorkItemInScope(ctx context.Context, tx ports.Tx, projectID, workItemID string) error {
	item, err := tx.Work().GetWorkItem(ctx, workItemID)
	if err != nil {
		return err
	}
	if string(item.ProjectID) != projectID {
		return scopeMismatch("work item", workItemID)
	}
	return nil
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// --- Evidence ---

// RevisionView mirrors workspace.Revision's own fields exactly, with json
// tags (the domain type carries none) — shared by EvidenceDetail and
// ContextSnapshotDetail below, both of which surface a RevisionSet's own
// lineage the identical way.
type RevisionView struct {
	RepositoryID        string `json:"repositoryId"`
	VCSObjectID         string `json:"vcsObjectId"`
	WorkspaceGeneration uint64 `json:"workspaceGeneration"`
}

// EvidenceDetail is the bounded, read-only view ListEvidenceForWorkItem/
// GetEvidence return for one Evidence row — this task's own "evidence
// list/detail/verify metadata" line: ArtifactReferences/RevisionSetHash/
// Revisions/PolicyVersion are exactly the metadata a caller needs to
// independently verify this Evidence row's own lineage (via the separate,
// offline `aw evidence verify` CLI_LOCAL leaf, ADR-028 — see
// docs/design/11-v6-00-ux-artifact.md's own Screen 11 row 2 doc comment:
// "hai leaf khác nhau cho hai nhu cầu khác nhau" — GetEvidence never
// itself re-verifies artifact bytes; it only ever returns the metadata
// that verification needs). ArtifactReferences are bare Artifact IDs, never
// locators — a caller fetches actual bytes through
// ListArtifactsForEvidence/GetArtifactContent below, never from this
// struct.
type EvidenceDetail struct {
	EvidenceID         string         `json:"evidenceId"`
	ProjectID          string         `json:"projectId"`
	WorkItemID         string         `json:"workItemId"`
	RunID              string         `json:"runId"`
	NodeRunID          string         `json:"nodeRunId"`
	AttemptID          string         `json:"attemptId"`
	Kind               string         `json:"kind"`
	Verdict            string         `json:"verdict"`
	ArtifactReferences []string       `json:"artifactReferences,omitempty"`
	Revisions          []RevisionView `json:"revisions,omitempty"`
	RevisionSetHash    string         `json:"revisionSetHash"`
	PolicyVersion      string         `json:"policyVersion"`
	CreatedAt          time.Time      `json:"createdAt"`
}

func evidenceToDetail(e runtimedomain.Evidence) EvidenceDetail {
	detail := EvidenceDetail{
		EvidenceID: string(e.ID), ProjectID: string(e.ProjectID), WorkItemID: string(e.WorkItemID),
		RunID: string(e.RunID), NodeRunID: string(e.NodeRunID), AttemptID: string(e.AttemptID),
		Kind: e.Kind, Verdict: e.Verdict, ArtifactReferences: append([]string(nil), e.ArtifactReferences...),
		RevisionSetHash: e.Revisions.ContentHash(), PolicyVersion: e.PolicyVersion, CreatedAt: e.CreatedAt,
	}
	for _, rev := range e.Revisions.Entries() {
		detail.Revisions = append(detail.Revisions, RevisionView{
			RepositoryID: string(rev.RepositoryID), VCSObjectID: rev.VCSObjectID, WorkspaceGeneration: rev.WorkspaceGeneration,
		})
	}
	return detail
}

// EvidenceFilter narrows ListEvidenceForWorkItem's own result client-side
// (both RunID and Kind optional; "" matches everything) — this task's own
// "list evidence theo WorkItem/Run/criterion" line, applied the same
// "unfiltered query, caller classifies" way ListNodeRunsForRun/
// ListWorkflowRunsForWorkItem already establish, without adding a second
// SQL query shape for what is, per WorkItem, always a small row set.
type EvidenceFilter struct {
	RunID string
	Kind  string
}

// ListEvidenceForWorkItem returns workItemID's own Evidence rows, reloading
// and scope-checking the WorkItem first, optionally narrowed by filter.
func ListEvidenceForWorkItem(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, workItemID string, filter EvidenceFilter) ([]EvidenceDetail, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return nil, err
	}
	var result []EvidenceDetail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if err := verifyWorkItemInScope(ctx, tx, projectID, workItemID); err != nil {
			return err
		}
		rows, err := tx.Runtime().ListEvidenceForWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		result = make([]EvidenceDetail, 0, len(rows))
		for _, row := range rows {
			if filter.RunID != "" && string(row.RunID) != filter.RunID {
				continue
			}
			if filter.Kind != "" && row.Kind != filter.Kind {
				continue
			}
			result = append(result, evidenceToDetail(row))
		}
		return nil
	})
	return result, err
}

// GetEvidence returns evidenceID's own detail, reloading and scope-checking
// the owning WorkItem first, then the Evidence row itself — a real Evidence
// row that belongs to a different project/work item than the path claims
// is ports.ErrScopeMismatch (leakage-normalized by the HTTP layer),
// identical to a genuinely unknown evidenceID.
func GetEvidence(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, workItemID, evidenceID string) (EvidenceDetail, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return EvidenceDetail{}, err
	}
	var detail EvidenceDetail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if err := verifyWorkItemInScope(ctx, tx, projectID, workItemID); err != nil {
			return err
		}
		evidence, err := tx.Runtime().GetEvidence(ctx, evidenceID)
		if err != nil {
			return err
		}
		if string(evidence.ProjectID) != projectID || string(evidence.WorkItemID) != workItemID {
			return scopeMismatch("evidence", evidenceID)
		}
		detail = evidenceToDetail(evidence)
		return nil
	})
	return detail, err
}

// --- Artifact ---

// sensitivityWireName maps redact.Sensitivity to this codebase's own closed
// wire vocabulary (PUBLIC/SENSITIVE/SECRET) — mirrors
// internal/delivery/httpapi/message/dto.go's own sensitivityWire constants
// exactly (kept as a private duplicate rather than an exported shared type,
// since httpapi/message's own vocabulary is a request-parsing concern this
// package has no reason to depend on; this direction only ever marshals
// outward).
func sensitivityWireName(s redact.Sensitivity) string {
	switch s {
	case redact.Sensitive:
		return "SENSITIVE"
	case redact.Secret:
		return "SECRET"
	default:
		return "PUBLIC"
	}
}

// ArtifactSummary is the bounded, read-only metadata view
// ListArtifactsForEvidence returns for one Artifact — this task's own
// "Không làm: KHÔNG expose locator" line enforced by construction: Locator
// is simply never a field here. Sensitivity/Redacted/RetentionClass/
// AttachState/Hold let a caller decide whether/how to fetch content
// (GetArtifactContent) without ever learning where it physically lives.
type ArtifactSummary struct {
	ArtifactID     string     `json:"artifactId"`
	ProjectID      string     `json:"projectId"`
	ContentHash    string     `json:"contentHash"`
	Size           int64      `json:"size"`
	MediaType      string     `json:"mediaType"`
	Sensitivity    string     `json:"sensitivity"`
	Redacted       bool       `json:"redacted"`
	RetentionClass string     `json:"retentionClass"`
	AttachState    string     `json:"attachState"`
	Hold           bool       `json:"hold"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	Version        uint64     `json:"version"`
}

func toArtifactSummary(a artifact.Artifact) ArtifactSummary {
	return ArtifactSummary{
		ArtifactID: string(a.ID), ProjectID: string(a.ProjectID), ContentHash: a.ContentHash, Size: a.Size,
		MediaType: a.MediaType, Sensitivity: sensitivityWireName(a.Sensitivity), Redacted: a.Redacted,
		RetentionClass: string(a.RetentionClass), AttachState: string(a.AttachState), Hold: a.Hold,
		ExpiresAt: a.ExpiresAt, CreatedAt: a.CreatedAt, Version: a.Version,
	}
}

// loadEvidenceInScope reloads evidenceID after confirming workItemID
// belongs to projectID, then confirms the loaded Evidence row itself
// belongs to that same project/work item — the identical two-step reload
// GetEvidence above performs, factored out so ListArtifactsForEvidence and
// ResolveEvidenceArtifactContent below never duplicate it.
func loadEvidenceInScope(ctx context.Context, tx ports.Tx, projectID, workItemID, evidenceID string) (runtimedomain.Evidence, error) {
	if err := verifyWorkItemInScope(ctx, tx, projectID, workItemID); err != nil {
		return runtimedomain.Evidence{}, err
	}
	evidence, err := tx.Runtime().GetEvidence(ctx, evidenceID)
	if err != nil {
		return runtimedomain.Evidence{}, err
	}
	if string(evidence.ProjectID) != projectID || string(evidence.WorkItemID) != workItemID {
		return runtimedomain.Evidence{}, scopeMismatch("evidence", evidenceID)
	}
	return evidence, nil
}

// ListArtifactsForEvidence returns the bounded metadata for every Artifact
// evidenceID's own ArtifactReferences names — this task's own "artifact
// inventory" line, scoped to one Evidence row (see this file's own doc
// comment for why: Artifact carries no WorkItem/Run column of its own, so
// an Evidence row's own ArtifactReferences is the only real domain-modeled
// ownership link available). Every referenced Artifact is independently
// re-checked against projectID (defense in depth: an Evidence row's own
// manifest should never legitimately name a foreign-project artifact, but
// this function never trusts that invariant blindly).
func ListArtifactsForEvidence(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, workItemID, evidenceID string) ([]ArtifactSummary, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return nil, err
	}
	var result []ArtifactSummary
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		evidence, err := loadEvidenceInScope(ctx, tx, projectID, workItemID, evidenceID)
		if err != nil {
			return err
		}
		result = make([]ArtifactSummary, 0, len(evidence.ArtifactReferences))
		for _, artifactID := range evidence.ArtifactReferences {
			a, err := tx.Artifacts().GetArtifact(ctx, artifactID)
			if err != nil {
				return err
			}
			if string(a.ProjectID) != projectID {
				return scopeMismatch("artifact", artifactID)
			}
			result = append(result, toArtifactSummary(a))
		}
		return nil
	})
	return result, err
}

// ResolveEvidenceArtifactContent authorizes and resolves the real
// ports.ArtifactRef for artifactID, which MUST be one of evidenceID's own
// ArtifactReferences — never any other Artifact ID, even a real one this
// caller could otherwise see, and never a client-supplied Locator (this
// task's own "Không làm: KHÔNG expose locator" line: the caller only ever
// supplies an opaque platform-minted Artifact ID; this function is the one
// place that turns it into a real ArtifactRef, from the Artifact row's own
// stored columns). The HTTP layer (internal/delivery/httpapi/evidence)
// calls ports.ArtifactStore.Verify/Open against the returned ref itself —
// this function never touches ArtifactStore, since that is real I/O
// outside any Tx (this file's own doc comment).
func ResolveEvidenceArtifactContent(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, workItemID, evidenceID, artifactID string) (ports.ArtifactRef, ArtifactSummary, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return ports.ArtifactRef{}, ArtifactSummary{}, err
	}
	var ref ports.ArtifactRef
	var summary ArtifactSummary
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		evidence, err := loadEvidenceInScope(ctx, tx, projectID, workItemID, evidenceID)
		if err != nil {
			return err
		}
		if !containsString(evidence.ArtifactReferences, artifactID) {
			return scopeMismatch("artifact", artifactID)
		}
		a, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		if err != nil {
			return err
		}
		if string(a.ProjectID) != projectID {
			return scopeMismatch("artifact", artifactID)
		}
		summary = toArtifactSummary(a)
		ref = ports.ArtifactRef{
			Locator: a.Locator, SHA256: a.ContentHash, Size: a.Size,
			ContentType: a.MediaType, Sensitivity: a.Sensitivity, Redacted: a.Redacted,
		}
		return nil
	})
	return ref, summary, err
}

// --- ContextSnapshot ---

// MessageRefView/ResourceRefView/EvidenceRefView mirror
// contextsnapshot.MessageRef/ResourceRef/EvidenceRef's own fields exactly,
// with json tags (the domain types carry none) — every field here is
// itself a reference (an ID or a content hash), never inlined resource/
// message content.
type MessageRefView struct {
	MessageID string `json:"messageId"`
}

type ResourceRefView struct {
	OwnerVersionID string `json:"ownerVersionId,omitempty"`
	ResourceKey    string `json:"resourceKey"`
	ContentHash    string `json:"contentHash"`
}

type EvidenceRefView struct {
	EvidenceID string `json:"evidenceId"`
}

// ContextSnapshotDetail is the bounded, read-only view this package's own
// GetContextSnapshot returns for a V5-04 ContextSnapshot
// (internal/app/ports/contextsnapshot.go) — deliberately NOT the legacy
// internal/domain/runtime.ContextSnapshot (see that port's own doc comment
// for why the two are kept apart). Exported (moved here from its own
// original, package-private home in
// internal/delivery/httpapi/message/dto.go, V6-07) so this task's own
// broader, message-independent detail route
// (internal/delivery/httpapi/evidence) and V6-07's own narrower
// message-scoped route (getMessageContextSnapshot) share one identical
// conversion instead of maintaining two copies of the same reload/redaction
// logic — docs/design/11-v6-00-ux-artifact.md's own Screen 12 row 4:
// "authority dùng chung với Screen 11 hàng 5 cho chi tiết đầy đủ".
type ContextSnapshotDetail struct {
	SnapshotID      string            `json:"snapshotId"`
	ProjectID       string            `json:"projectId"`
	WorkItemID      string            `json:"workItemId"`
	AttemptID       string            `json:"attemptId"`
	MessageRefs     []MessageRefView  `json:"messageRefs,omitempty"`
	ResourceRefs    []ResourceRefView `json:"resourceRefs,omitempty"`
	EvidenceRefs    []EvidenceRefView `json:"evidenceRefs,omitempty"`
	Revisions       []RevisionView    `json:"revisions,omitempty"`
	RevisionSetHash string            `json:"revisionSetHash"`
	ManifestHash    string            `json:"manifestHash"`
	CreatedAt       time.Time         `json:"createdAt"`
}

// ContextSnapshotToDetail converts a real, already-loaded
// contextsnapshot.Snapshot into its bounded wire DTO — the single
// conversion both getMessageContextSnapshot (V6-07) and GetContextSnapshot
// (V6-07B) call.
func ContextSnapshotToDetail(s contextsnapshot.Snapshot) ContextSnapshotDetail {
	detail := ContextSnapshotDetail{
		SnapshotID: string(s.ID), ProjectID: string(s.ProjectID), WorkItemID: string(s.WorkItemID),
		AttemptID: string(s.AttemptID), ManifestHash: s.ManifestHash, RevisionSetHash: s.Revisions.ContentHash(),
		CreatedAt: s.CreatedAt,
	}
	for _, ref := range s.MessageRefs {
		detail.MessageRefs = append(detail.MessageRefs, MessageRefView{MessageID: ref.MessageID})
	}
	for _, ref := range s.ResourceRefs {
		detail.ResourceRefs = append(detail.ResourceRefs, ResourceRefView{
			OwnerVersionID: ref.OwnerVersionID, ResourceKey: ref.ResourceKey, ContentHash: ref.ContentHash,
		})
	}
	for _, ref := range s.EvidenceRefs {
		detail.EvidenceRefs = append(detail.EvidenceRefs, EvidenceRefView{EvidenceID: ref.EvidenceID})
	}
	for _, rev := range s.Revisions.Entries() {
		detail.Revisions = append(detail.Revisions, RevisionView{
			RepositoryID: string(rev.RepositoryID), VCSObjectID: rev.VCSObjectID, WorkspaceGeneration: rev.WorkspaceGeneration,
		})
	}
	return detail
}

// GetContextSnapshot returns snapshotID's own bounded detail, reloading and
// scope-checking the owning WorkItem first, then the Snapshot itself — a
// real Snapshot that belongs to a different project/work item than the
// path claims is ports.ErrScopeMismatch (leakage-normalized), identical to
// an unknown snapshotID. Distinct from V6-07's own
// getMessageContextSnapshot (message-scoped, resolves a Snapshot from a
// Message's own AttemptID via GetSnapshotByAttemptID): this is the
// standalone, message-independent detail route
// docs/design/11-v6-00-ux-artifact.md's own Screen 11 row 5 names.
func GetContextSnapshot(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, workItemID, snapshotID string) (ContextSnapshotDetail, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return ContextSnapshotDetail{}, err
	}
	var detail ContextSnapshotDetail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if err := verifyWorkItemInScope(ctx, tx, projectID, workItemID); err != nil {
			return err
		}
		snapshot, err := tx.ContextSnapshots().GetSnapshot(ctx, snapshotID)
		if err != nil {
			return err
		}
		if string(snapshot.ProjectID) != projectID || string(snapshot.WorkItemID) != workItemID {
			return scopeMismatch("context snapshot", snapshotID)
		}
		detail = ContextSnapshotToDetail(snapshot)
		return nil
	})
	return detail, err
}
