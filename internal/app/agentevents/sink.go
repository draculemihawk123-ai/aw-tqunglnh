package agentevents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// MaxEventPayloadBytes bounds one event's encoded, redacted payload — the
// Sink's own fail-closed check, run before an oversized event is ever
// buffered. Mirrors the sqlite adapter's own maxAgentEventPayloadBytes
// (internal/adapters/sqlite/agent_events.go): agentevents cannot import
// that adapter package (internal/archtest's own domain/app-never-imports-
// adapters boundary), so the bound is intentionally duplicated as a
// constant rather than shared.
const MaxEventPayloadBytes = 256 * 1024

// MaxBatchEvents bounds how many events Accept buffers in memory before an
// eager flush — V5-08A's own "batching có bound": a long-running, chatty
// agent must never grow this Sink's memory without limit between the
// checkpoints that would otherwise trigger a flush.
const MaxBatchEvents = 100

var (
	// ErrSequenceGap is returned by Accept when event.Sequence skips ahead
	// of the next sequence this Sink expects — a real adapter's own
	// normalizer.emit always increments by exactly one (claude.go/codex.go),
	// so a gap means a genuine protocol violation, never a legitimate retry.
	ErrSequenceGap = errors.New("agentevents: event sequence is not the next expected sequence")
	// ErrDuplicateEvent is returned by Accept when event.Sequence has
	// already been accepted by this Sink instance. Unlike Checkpoint (whose
	// StoreCheckpoint may legitimately see the same row from more than one
	// caller and treats identical content as an idempotent no-op), one Sink
	// instance is scoped to exactly one live Start/Resume call for one
	// AttemptID — a replacement Attempt after any failure always gets a new
	// AttemptID and a fresh Sink (V5-08's own confirmed invariant, "replacement
	// Attempt luôn Start từ snapshot"), so this Sink never legitimately
	// re-observes a Sequence it has already seen; a duplicate reaching here
	// is a real bug, surfaced fail-closed rather than silently absorbed.
	ErrDuplicateEvent = errors.New("agentevents: event sequence was already accepted")
	// ErrPayloadTooLarge is returned by Accept when an event's encoded,
	// redacted payload exceeds MaxEventPayloadBytes.
	ErrPayloadTooLarge = errors.New("agentevents: event payload exceeds the maximum size")
	// ErrWrongAttempt is returned by Accept when event.AttemptID does not
	// match the AttemptID this Sink was constructed for. Audit finding
	// (2026-09-09, V5-08A remediation): a Sink is scoped to exactly one live
	// Start/Resume call for one AttemptID (this package's own doc comment,
	// above) — an event carrying a DIFFERENT AttemptID reaching Accept would
	// otherwise be silently persisted under this Sink's own AttemptID,
	// mis-attributing it.
	ErrWrongAttempt = errors.New("agentevents: event AttemptID does not match this Sink's own AttemptID")
	// ErrMissingMount is returned by NewSink when cfg.EffectiveScope names a
	// repository with no corresponding entry in cfg.Mounts. Audit finding
	// (2026-09-09, V5-08A remediation): scopeguard.ValidateDiffs only ever
	// sees the diffs a caller actually passes it — a repository silently
	// missing its own Mount would never produce a diff to validate at all,
	// a false negative that could hide a real out-of-scope mutation in that
	// repository from every checkpoint this Sink ever captures.
	ErrMissingMount = errors.New("agentevents: EffectiveScope names a repository with no corresponding Mount")
)

// CheckpointStore is the narrow slice of the legacy ports.WorkflowPersistence
// interface (internal/app/ports/persistence.go) this Sink needs. Checkpoint
// persistence has not been migrated to the modern ports.Tx-composable
// accessor pattern agent_events uses (unlike AgentEventsRepository, added
// alongside this package, Checkpoint's own storage predates it and is out
// of this task's scope to migrate) — a caller passes whatever already
// implements StoreCheckpoint (in production, the same *sqlite.Store that
// backs its own ports.UnitOfWork).
type CheckpointStore interface {
	StoreCheckpoint(ctx context.Context, checkpoint runtime.Checkpoint) (runtime.Checkpoint, error)
}

// Mount is one repository this Sink watches for diff capture at each
// checkpoint: a real, already-resolved WorkspaceHandle this Sink may call
// ports.WorkspaceProvider against. Resolving a real Handle for a NodeRun's
// own EffectiveScope repositories is explicitly not this package's concern
// (V5-08's own buildExecutionEnvelope left it unresolved; V5-08B or later
// is where a production caller supplies one) — this package's own tests
// provision one directly against a real temporary Git repository.
type Mount struct {
	RepositoryID project.RepositoryID
	Handle       ports.WorkspaceHandle
}

// mountBaseline pins the Revision a Mount is diffed against at every
// checkpoint — always the revision as of Sink construction (ADR-005's own
// "Alpha luôn start fresh"), so each Checkpoint.Revisions entry means "what
// changed since this Attempt began," never "since the previous checkpoint."
type mountBaseline struct {
	Mount
	baseRevision workspace.Revision
}

// Config is everything NewSink needs. Every field is required unless its
// own doc comment says otherwise.
type Config struct {
	RunID             string
	NodeRunID         string
	AttemptID         string
	ContextSnapshotID string
	// EffectiveScope is the authority captureCheckpoint validates every
	// Mount's diff against (scopeguard.ValidateDiffs) — the same
	// []work.RepositoryScope a NodeRun's own admission envelope already
	// carries (internal/app/runtime/admission.go's buildExecutionEnvelope).
	EffectiveScope []work.RepositoryScope
	// Mounts may be empty — a compute-only AGENT node with no repository
	// access captures an empty RevisionSet at every checkpoint.
	Mounts      []Mount
	Workspaces  ports.WorkspaceProvider
	Registry    *eventschema.Registry
	Matcher     redact.Matcher
	UOW         ports.UnitOfWork
	Checkpoints CheckpointStore
	IDs         idsource.Source
	Clock       clock.Clock
	// JobLease/WriteLeases fence every write this Sink makes — V5-08B audit
	// finding (2026-09-09, deferred from V5-08A): a worker that already
	// lost its own JobLease or a mount's own WriteLease must never keep
	// persisting events/checkpoints as if it were still authoritative,
	// exactly the same GC-INV-17/18 guarantee FinalizeExecutionAttempt
	// already enforces for the terminal transition. Required — NewSink
	// fails closed if either is missing.
	JobLease    ports.JobLease
	WriteLeases []ports.WriteLeaseGrant
}

// Sink is V5-08A's real ports.AgentEventSink: normalize+validate through
// Registry, redact via Matcher, batch into AgentEventsRepository (bounded
// by MaxBatchEvents), and — whenever the provider proposes one
// (ports.AgentEventCheckpointProposed) — flush the current batch, capture a
// scope-validated workspace diff per Mount, and store an immutable
// Checkpoint. Not safe to reuse across more than one Attempt: construct a
// new Sink per Start/Resume call.
type Sink struct {
	runID             string
	nodeRunID         string
	attemptID         string
	contextSnapshotID string
	effectiveScope    []work.RepositoryScope
	mounts            []mountBaseline
	workspaces        ports.WorkspaceProvider
	registry          *eventschema.Registry
	matcher           redact.Matcher
	uow               ports.UnitOfWork
	checkpoints       CheckpointStore
	ids               idsource.Source
	clk               clock.Clock
	jobLease          ports.JobLease
	writeLeases       []ports.WriteLeaseGrant

	mu            sync.Mutex
	lastSequence  uint64
	buffer        []ports.AgentEventRecord
	checkpointSeq uint64
}

// NewSink captures each Mount's own current Revision as its diff baseline
// (real I/O — a git status/rev-parse call per Mount) before returning, so a
// later checkpoint's diff is always relative to "as of this Attempt's own
// start," never to whatever the working tree happened to be the instant
// the first checkpoint fires.
func NewSink(ctx context.Context, cfg Config) (*Sink, error) {
	if cfg.RunID == "" || cfg.NodeRunID == "" || cfg.AttemptID == "" || cfg.ContextSnapshotID == "" {
		return nil, errors.New("agentevents: RunID, NodeRunID, AttemptID and ContextSnapshotID are required")
	}
	if cfg.Workspaces == nil || cfg.Registry == nil || cfg.UOW == nil || cfg.Checkpoints == nil || cfg.IDs == nil || cfg.Clock == nil {
		return nil, errors.New("agentevents: Workspaces, Registry, UOW, Checkpoints, IDs and Clock are all required")
	}
	if cfg.JobLease.JobID == "" {
		return nil, errors.New("agentevents: JobLease is required — every write this Sink makes must be fenced against the job driving this Attempt")
	}

	mountedRepositories := make(map[project.RepositoryID]struct{}, len(cfg.Mounts))
	for _, mount := range cfg.Mounts {
		mountedRepositories[mount.RepositoryID] = struct{}{}
	}
	for _, scope := range cfg.EffectiveScope {
		if _, ok := mountedRepositories[scope.RepositoryID()]; !ok {
			return nil, fmt.Errorf("%w: repository %s", ErrMissingMount, scope.RepositoryID())
		}
	}

	baselines := make([]mountBaseline, 0, len(cfg.Mounts))
	for _, mount := range cfg.Mounts {
		revision, err := cfg.Workspaces.CaptureRevision(ctx, mount.Handle)
		if err != nil {
			return nil, fmt.Errorf("agentevents: capture baseline revision for repository %s: %w", mount.RepositoryID, err)
		}
		baselines = append(baselines, mountBaseline{Mount: mount, baseRevision: revision})
	}

	return &Sink{
		runID: cfg.RunID, nodeRunID: cfg.NodeRunID, attemptID: cfg.AttemptID, contextSnapshotID: cfg.ContextSnapshotID,
		effectiveScope: cfg.EffectiveScope, mounts: baselines, workspaces: cfg.Workspaces, registry: cfg.Registry,
		matcher: cfg.Matcher, uow: cfg.UOW, checkpoints: cfg.Checkpoints, ids: cfg.IDs, clk: cfg.Clock,
		jobLease: cfg.JobLease, writeLeases: append([]ports.WriteLeaseGrant(nil), cfg.WriteLeases...),
	}, nil
}

// validateFencingLocked re-checks JobLease and every WriteLeaseGrant this
// Sink was constructed with are still authoritative — GC-INV-17/18 made
// real for Sink's own writes (audit finding, 2026-09-09, deferred from
// V5-08A): a worker that already lost its own lease must never keep
// persisting events/checkpoints as if nothing changed, the exact case
// FinalizeExecutionAttempt already fences for the terminal transition.
func (s *Sink) validateFencingLocked(ctx context.Context, tx ports.Tx) error {
	if err := tx.Jobs().ValidateActiveJob(ctx, s.jobLease, "ExecutionAttempt", s.attemptID); err != nil {
		return err
	}
	for _, grant := range s.writeLeases {
		if err := tx.Runtime().ValidateWriteLeaseFencing(ctx, s.jobLease, grant); err != nil {
			return err
		}
	}
	return nil
}

var _ ports.AgentEventSink = (*Sink)(nil)

// Accept implements ports.AgentEventSink. See this package's own doc
// comment for the full normalize/redact/batch/checkpoint contract.
func (s *Sink) Accept(ctx context.Context, event ports.AgentEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validateOrderingLocked(event); err != nil {
		return err
	}
	if !s.registry.IsRegistered(string(event.Kind), schemaVersionV1) {
		return fmt.Errorf("%w: %s v%d", eventschema.ErrNotRegistered, event.Kind, schemaVersionV1)
	}

	record, err := s.buildRecord(event)
	if err != nil {
		return err
	}
	s.buffer = append(s.buffer, record)
	s.lastSequence = event.Sequence

	if event.Kind == ports.AgentEventCheckpointProposed {
		if err := s.flushLocked(ctx); err != nil {
			return err
		}
		return s.captureCheckpointLocked(ctx, event)
	}
	if len(s.buffer) >= MaxBatchEvents {
		return s.flushLocked(ctx)
	}
	return nil
}

// Flush persists any events still buffered — the caller's own
// responsibility to call exactly once after Start/Resume returns
// (regardless of its own error), so a trailing event after the last
// checkpoint (e.g. claude.go's own DIAGNOSTIC-then-EXECUTION_FINISHED tail
// on a failed run) is never silently lost. Safe to call with an empty
// buffer.
func (s *Sink) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked(ctx)
}

func (s *Sink) validateOrderingLocked(event ports.AgentEvent) error {
	if string(event.AttemptID) != s.attemptID {
		return fmt.Errorf("%w: this Sink is attempt %s, event carries attempt %s", ErrWrongAttempt, s.attemptID, event.AttemptID)
	}
	if event.Sequence == 0 {
		return fmt.Errorf("agentevents: attempt %s: event sequence must be positive", s.attemptID)
	}
	switch {
	case event.Sequence <= s.lastSequence:
		return fmt.Errorf("%w: attempt %s sequence %d (last accepted %d)", ErrDuplicateEvent, s.attemptID, event.Sequence, s.lastSequence)
	case event.Sequence != s.lastSequence+1:
		return fmt.Errorf("%w: attempt %s expected sequence %d, got %d", ErrSequenceGap, s.attemptID, s.lastSequence+1, event.Sequence)
	}
	return nil
}

// buildRecord redacts and encodes event into a durable AgentEventRecord.
// Redaction runs on event's own JSON encoding (Payload marshaled, then
// unmarshaled into a generic any) rather than on the Payload struct value
// directly: redact.Matcher.Value walks every exported struct field via
// reflection, and Payload.ObservedAt is a time.Time — a struct whose
// meaningful state lives entirely in unexported fields Value() must skip,
// which would silently reduce it to "{}". Round-tripping through JSON first
// means Value() only ever sees JSON's own primitive shapes (string,
// float64, bool, nil, map, slice), never that pitfall.
func (s *Sink) buildRecord(event ports.AgentEvent) (ports.AgentEventRecord, error) {
	payload := Payload{
		ObservedAt: event.ObservedAt, Message: event.Message, Tool: event.Tool,
		Usage: event.Usage, Session: event.Session, Diagnostic: event.Diagnostic,
		ProviderMetadata: event.ProviderMetadata,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ports.AgentEventRecord{}, fmt.Errorf("agentevents: encode event %d payload: %w", event.Sequence, err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return ports.AgentEventRecord{}, fmt.Errorf("agentevents: decode event %d payload for redaction: %w", event.Sequence, err)
	}
	redacted, err := s.matcher.Value(generic)
	if err != nil {
		return ports.AgentEventRecord{}, fmt.Errorf("agentevents: redact event %d payload: %w", event.Sequence, err)
	}
	payloadJSON, err := json.Marshal(redacted)
	if err != nil {
		return ports.AgentEventRecord{}, fmt.Errorf("agentevents: re-encode redacted event %d payload: %w", event.Sequence, err)
	}
	if len(payloadJSON) > MaxEventPayloadBytes {
		return ports.AgentEventRecord{}, fmt.Errorf("%w: event %d is %d bytes, exceeds %d byte limit",
			ErrPayloadTooLarge, event.Sequence, len(payloadJSON), MaxEventPayloadBytes)
	}
	return ports.AgentEventRecord{
		ID: s.ids.NewID(), AttemptID: s.attemptID, Sequence: event.Sequence,
		Kind: string(event.Kind), SchemaVersion: schemaVersionV1,
		PayloadJSON: string(payloadJSON), CreatedAt: event.ObservedAt,
	}, nil
}

// flushLocked persists s.buffer and only THEN clears it — audit finding
// (2026-09-09, V5-08A remediation): the previous version cleared the buffer
// before the transaction ran, so a genuine commit failure (a real, transient
// storage error, not a business rejection) permanently lost every buffered
// event with no way for a caller to retry them. Since AppendBatch runs
// inside one WithSerializedWrite transaction, a failure here means NOTHING
// was persisted (an all-or-nothing rollback, never a partial write), so
// retaining the batch for a later Accept/Flush retry is always safe — never
// a duplicate-insert risk.
func (s *Sink) flushLocked(ctx context.Context) error {
	if len(s.buffer) == 0 {
		return nil
	}
	batch := s.buffer
	if err := s.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if err := s.validateFencingLocked(ctx, tx); err != nil {
			return err
		}
		return tx.AgentEvents().AppendBatch(ctx, batch)
	}); err != nil {
		return err
	}
	s.buffer = nil
	return nil
}

// captureCheckpointLocked runs entirely OUTSIDE any database transaction
// while it calls Workspaces.Diff (real process I/O, one `git` invocation
// per Mount via internal/adapters/gitworktree) — the same "real I/O never
// inside WithSerializedWrite/WithReadOnly" discipline
// RuntimeExecutionConfigProvider.Resolve (V4-04) and V5-08's own admission
// probe already established. flushLocked's own transaction has already
// committed and closed by the time this runs (Accept calls it first),
// and StoreCheckpoint below is its own separate, already-autocommit
// statement (checkpoint_store.go's StoreCheckpoint uses the raw *sql.DB,
// never a shared *sql.Tx) — so nothing here ever nests a second transaction
// inside another.
func (s *Sink) captureCheckpointLocked(ctx context.Context, triggeringEvent ports.AgentEvent) error {
	diffs := make([]ports.WorkspaceDiff, 0, len(s.mounts))
	revisions := make([]workspace.Revision, 0, len(s.mounts))
	for _, mount := range s.mounts {
		diff, err := s.workspaces.Diff(ctx, mount.Handle, mount.baseRevision)
		if err != nil {
			return fmt.Errorf("agentevents: capture diff for repository %s: %w", mount.RepositoryID, err)
		}
		diffs = append(diffs, diff)
		revisions = append(revisions, diff.CurrentRevision)
	}
	if err := scopeguard.ValidateDiffs(s.effectiveScope, diffs); err != nil {
		return fmt.Errorf("agentevents: checkpoint at event %d: %w", triggeringEvent.Sequence, err)
	}
	revisionSet, err := workspace.NewRevisionSet(revisions)
	if err != nil {
		return fmt.Errorf("agentevents: build checkpoint revision set: %w", err)
	}

	s.checkpointSeq++
	checkpoint, err := runtime.NewCheckpoint(
		runtime.CheckpointID(s.ids.NewID()), runtime.WorkflowRunID(s.runID), runtime.NodeRunID(s.nodeRunID),
		runtime.ExecutionAttemptID(s.attemptID), s.checkpointSeq, triggeringEvent.Sequence,
		runtime.ContextSnapshotID(s.contextSnapshotID), revisionSet,
		sharedStateHash(s.attemptID, triggeringEvent.Sequence, revisionSet), nil, s.clk.Now(),
	)
	if err != nil {
		return fmt.Errorf("agentevents: build checkpoint: %w", err)
	}
	// StoreCheckpoint (checkpoint_store.go) is legacy, autocommit-only — not
	// ports.Tx-composable — so this fencing re-check cannot be atomic with
	// the write itself the way flushLocked's own check is. A real, narrow
	// TOCTOU window remains between this read-only check and the write
	// immediately below; accepted here (rather than left completely
	// unchecked, the audit finding this closes) because closing it fully
	// would require promoting checkpoint storage onto ports.Tx, out of this
	// task's own scope.
	if err := s.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		return s.validateFencingLocked(ctx, tx)
	}); err != nil {
		return err
	}
	_, err = s.checkpoints.StoreCheckpoint(ctx, checkpoint)
	if err != nil {
		return fmt.Errorf("agentevents: store checkpoint: %w", err)
	}
	return nil
}

// sharedStateHash gives Checkpoint.SharedStateHash a deterministic,
// meaningful value from data this package already has in hand, mirroring
// adapterbuild.HashExecutableFile's own "sha256:"-prefixed convention. No
// existing reader interprets this field beyond round-tripping it
// byte-for-byte (checkpoint_store.go's own checkpointsEqual) — reaching
// into WorkflowRun.SharedState itself would require this package to read
// application state well outside its own narrow event/checkpoint/diff
// concern for a field nothing yet consumes semantically.
func sharedStateHash(attemptID string, canonicalEventSequence uint64, revisions workspace.RevisionSet) string {
	material := fmt.Sprintf("%s|%d|%s", attemptID, canonicalEventSequence, revisions.ContentHash())
	sum := sha256.Sum256([]byte(material))
	return "sha256:" + hex.EncodeToString(sum[:])
}
