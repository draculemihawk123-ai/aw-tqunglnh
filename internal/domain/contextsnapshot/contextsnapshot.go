// Package contextsnapshot is V5-04's own durable, immutable
// message/resource/RevisionSet manifest for one ExecutionAttempt
// (docs/design/07-v5-execution-evidence.md V5-04; HE-04-M06, GC-INV-08,
// HE-02-M01, HE-03-M02, HE-05-M04): "mỗi Attempt có immutable
// message/resource/RevisionSet manifest".
//
// This is a NEW, separate type from the pre-existing
// internal/domain/runtime.ContextSnapshot (a spike-era shape that inlines
// message content directly and is wired only to crash-recovery/
// checkpoint code, confirmed unused by the real scheduling/dispatch path)
// — this package does not touch that type, its table, or any of its
// existing call sites at all. The two coexist deliberately: legacy
// recovery keeps using runtime.ContextSnapshot; real scheduling/dispatch
// uses this package's own Snapshot from here on. There is no bridge
// between them, and no ID of one kind is ever loaded through the other
// kind's own repository.
//
// This package deliberately does NOT import internal/domain/runtime:
// ExecutionAttempt needs to reference a Snapshot's own ID (via its
// ContextSnapshotID field), so this package cannot import runtime back
// without creating an import cycle. AttemptID is therefore this package's
// own local string-typed identity, not runtime.ExecutionAttemptID — the
// application layer, which imports both packages freely, is what
// cross-checks a Snapshot's AttemptID actually names the ExecutionAttempt
// a caller believes it does.
package contextsnapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ID is a platform-minted identity for one Snapshot row.
type ID string

// AttemptID mirrors runtime.ExecutionAttemptID's own string identity —
// see this package's own doc comment for why it is declared fresh here
// rather than imported.
type AttemptID string

// MessageRef is one V5-02 Message this Snapshot pins, by reference only —
// content is never inlined here (HE-11-S06: DB metadata only, content
// stays in ArtifactStore).
type MessageRef struct {
	MessageID string
}

// ResourceRef is one resolved resource (a contextassembler.ResolvedCandidate's
// own identity, or any other resource kind a caller pins) this Snapshot
// includes, by reference only.
type ResourceRef struct {
	ResourceKey string
	ContentHash string
}

// Snapshot is the immutable manifest go-core-spec §4.6 names:
// "ContextSnapshot { ID, ProjectID, WorkItemID, AttemptID, MessageRefs[],
// ResourceRefs[], RevisionSet, ManifestHash, CreatedAt }". MessageRefs and
// ResourceRefs preserve the EXACT order they were given in — unlike
// RevisionSet (a set, canonically sorted since two different orderings of
// the same revisions are semantically identical), HE-04-M06 requires this
// manifest to record "thứ tự" (the order) resources were actually
// rendered in, so two Snapshots with the identical refs in a different
// order are semantically DIFFERENT manifests and must hash differently.
type Snapshot struct {
	ID           ID
	ProjectID    project.ProjectID
	WorkItemID   work.WorkItemID
	AttemptID    AttemptID
	MessageRefs  []MessageRef
	ResourceRefs []ResourceRef
	Revisions    workspace.RevisionSet
	ManifestHash string
	CreatedAt    time.Time
}

// NewSnapshot validates fields and computes ManifestHash itself — the same
// "constructor self-computes its own canonical hash" discipline
// workspace.NewRevisionSet and workflow.Compile's own ContentHash already
// use (docs/design/07-v5-execution-evidence.md V5-04 research: "Pattern
// B" — sort-where-order-is-not-meaningful, marshal, sha256 — is the
// established precedent for a runtime fact-record's own hash, as opposed
// to authoring.Canonicalize, which exists specifically for ambiguous
// user-authored YAML/JSON).
func NewSnapshot(
	id ID,
	projectID project.ProjectID,
	workItemID work.WorkItemID,
	attemptID AttemptID,
	messageRefs []MessageRef,
	resourceRefs []ResourceRef,
	revisions workspace.RevisionSet,
	createdAt time.Time,
) (Snapshot, error) {
	if strings.TrimSpace(string(id)) == "" {
		return Snapshot{}, errors.New("contextsnapshot: ID is required")
	}
	if strings.TrimSpace(string(projectID)) == "" {
		return Snapshot{}, errors.New("contextsnapshot: ProjectID is required")
	}
	if strings.TrimSpace(string(workItemID)) == "" {
		return Snapshot{}, errors.New("contextsnapshot: WorkItemID is required")
	}
	if strings.TrimSpace(string(attemptID)) == "" {
		return Snapshot{}, errors.New("contextsnapshot: AttemptID is required")
	}
	if createdAt.IsZero() {
		return Snapshot{}, errors.New("contextsnapshot: CreatedAt is required")
	}

	messageRefsCopy := append([]MessageRef(nil), messageRefs...)
	for _, ref := range messageRefsCopy {
		if strings.TrimSpace(ref.MessageID) == "" {
			return Snapshot{}, errors.New("contextsnapshot: MessageRef.MessageID must not be blank")
		}
	}
	resourceRefsCopy := append([]ResourceRef(nil), resourceRefs...)
	for _, ref := range resourceRefsCopy {
		if strings.TrimSpace(ref.ResourceKey) == "" || strings.TrimSpace(ref.ContentHash) == "" {
			return Snapshot{}, errors.New("contextsnapshot: ResourceRef.ResourceKey and ContentHash must not be blank")
		}
	}
	revisionsCopy, err := workspace.NewRevisionSet(revisions.Entries())
	if err != nil {
		return Snapshot{}, err
	}

	manifestHash, err := computeManifestHash(messageRefsCopy, resourceRefsCopy, revisionsCopy)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		ID: id, ProjectID: projectID, WorkItemID: workItemID, AttemptID: attemptID,
		MessageRefs: messageRefsCopy, ResourceRefs: resourceRefsCopy, Revisions: revisionsCopy,
		ManifestHash: manifestHash, CreatedAt: createdAt.UTC(),
	}, nil
}

// canonicalManifest is exactly what ManifestHash is computed over — never
// ID/AttemptID/CreatedAt (identity/timestamp, not manifest content), so
// re-verifying an already-stored Snapshot's own hash (tamper detection)
// never depends on anything but its own message/resource/revision
// content. RevisionSet contributes its own already-canonical ContentHash
// rather than raw entries, reusing that type's own established
// canonicalization instead of re-deriving it.
type canonicalManifest struct {
	MessageRefs     []MessageRef  `json:"messageRefs"`
	ResourceRefs    []ResourceRef `json:"resourceRefs"`
	RevisionSetHash string        `json:"revisionSetHash"`
}

func computeManifestHash(messageRefs []MessageRef, resourceRefs []ResourceRef, revisions workspace.RevisionSet) (string, error) {
	data, err := json.Marshal(canonicalManifest{
		MessageRefs: messageRefs, ResourceRefs: resourceRefs, RevisionSetHash: revisions.ContentHash(),
	})
	if err != nil {
		return "", fmt.Errorf("contextsnapshot: marshal canonical manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
