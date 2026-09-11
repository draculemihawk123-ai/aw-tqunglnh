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
	"sort"
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
//
// OwnerVersionID completes ADR-012's own passive-resource identity triple
// (definition.ResourceIdentity: OwnerVersionID+ResourceKey+ContentHash) —
// V5-08B0 adds it because a real request-assembly caller must be able to
// re-load the EXACT SkillVersion/LayerVersion a resource came from (to
// re-verify its content hash before dispatch); ResourceKey+ContentHash
// alone cannot name which published version to re-load. This field is
// tagged `omitempty` deliberately: a Snapshot written before V5-08B0 was
// marshaled with a ResourceRef shape that never had this key at all, and
// computeManifestHash (below) must keep recomputing the IDENTICAL hash for
// those already-stored rows on every load (tamper-check re-verifies by
// recomputing from scratch every time) — omitempty is what makes an
// old row's JSON re-marshal byte-for-byte identical to what was hashed
// when it was first written. A NEW snapshot's own ResourceRef always has
// OwnerVersionID populated (the request assembler fails closed on any ref
// missing it — see internal/app/agentrequest), so this is never
// ambiguous in practice: an empty OwnerVersionID only ever means "a
// snapshot written before this field existed," never "a new ref that
// forgot to set it."
type ResourceRef struct {
	OwnerVersionID string `json:",omitempty"`
	ResourceKey    string
	ContentHash    string
}

// EvidenceRef is one runtime.Evidence row this Snapshot references, by
// reference only — mirrors MessageRef's own bare-ID shape exactly.
// V5-12's own checker input allowlist contract (docs/design/
// 07-v5-execution-evidence.md V5-12, confirmed with the user 2026-09-10):
// a CHECKER-role AGENT node's own Snapshot carries EvidenceRefs instead of
// a maker transcript — see NewSnapshot's own doc comment on ordering for
// why this field, unlike MessageRefs/ResourceRefs, is sorted rather than
// order-preserved.
type EvidenceRef struct {
	EvidenceID string
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
	// EvidenceRefs is V5-12's own checker input allowlist addition
	// (2026-09-10) — empty for every MAKER/COMMAND/MACHINE_GATE Attempt
	// (unchanged behavior), populated only for a CHECKER-role AGENT
	// Attempt, whose caller (internal/app/runtime/schedule.go) gathers it
	// instead of MessageRefs. Unlike MessageRefs/ResourceRefs, order
	// carries no meaning here (this is not a rendered transcript), so
	// NewSnapshot sorts it for canonicalization.
	EvidenceRefs []EvidenceRef
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
	evidenceRefs []EvidenceRef,
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
	evidenceRefsCopy := append([]EvidenceRef(nil), evidenceRefs...)
	for _, ref := range evidenceRefsCopy {
		if strings.TrimSpace(ref.EvidenceID) == "" {
			return Snapshot{}, errors.New("contextsnapshot: EvidenceRef.EvidenceID must not be blank")
		}
	}
	sort.Slice(evidenceRefsCopy, func(i, j int) bool { return evidenceRefsCopy[i].EvidenceID < evidenceRefsCopy[j].EvidenceID })
	revisionsCopy, err := workspace.NewRevisionSet(revisions.Entries())
	if err != nil {
		return Snapshot{}, err
	}

	manifestHash, err := computeManifestHash(messageRefsCopy, resourceRefsCopy, evidenceRefsCopy, revisionsCopy)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		ID: id, ProjectID: projectID, WorkItemID: workItemID, AttemptID: attemptID,
		MessageRefs: messageRefsCopy, ResourceRefs: resourceRefsCopy, EvidenceRefs: evidenceRefsCopy, Revisions: revisionsCopy,
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
	MessageRefs  []MessageRef  `json:"messageRefs"`
	ResourceRefs []ResourceRef `json:"resourceRefs"`
	// EvidenceRefs is tagged omitempty deliberately — the same reasoning
	// ResourceRef.OwnerVersionID's own doc comment already gives: a
	// Snapshot written before V5-12 was marshaled with no EvidenceRefs key
	// at all, and computeManifestHash must keep recomputing the IDENTICAL
	// hash for those already-stored rows on every load (loadSnapshotTx's
	// own tamper check). A NEW MAKER/COMMAND/MACHINE_GATE Snapshot always
	// has this nil too (unchanged behavior), so omitempty never hides a
	// real CHECKER Snapshot's own non-empty EvidenceRefs from the hash —
	// only the "no Evidence at all" case, which is exactly when omitting
	// the key changes nothing observable.
	EvidenceRefs    []EvidenceRef `json:"evidenceRefs,omitempty"`
	RevisionSetHash string        `json:"revisionSetHash"`
}

func computeManifestHash(messageRefs []MessageRef, resourceRefs []ResourceRef, evidenceRefs []EvidenceRef, revisions workspace.RevisionSet) (string, error) {
	data, err := json.Marshal(canonicalManifest{
		MessageRefs: messageRefs, ResourceRefs: resourceRefs, EvidenceRefs: evidenceRefs, RevisionSetHash: revisions.ContentHash(),
	})
	if err != nil {
		return "", fmt.Errorf("contextsnapshot: marshal canonical manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
