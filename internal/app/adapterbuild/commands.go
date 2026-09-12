// Package adapterbuild is the application-command layer for V2-07A's
// immutable AdapterBuildVersion registry (docs/design/04-v2-definition-plane.md
// V2-07A, ADR-022): ProbeAdapterBuild, RegisterAdapterBuild, ListAdapterBuilds
// and GetAdapterBuild — the four application commands V2-07A's own scope
// line names. Neither command spawns a real provider CLI to verify its
// protocol/capability behavior (that is V5-06/07's job): Probe only
// hashes the configured executable's file content, the same fingerprint-
// only observation internal/app/doctor already makes, graduated here into
// a real, persisted, signed registration flow.
//
// V6-10I (docs/design/08-v6-api-projections.md, ADR-022, ADR-025,
// GC-INV-35, GC-DS-11) hardens both commands onto the same
// CommandEnvelope/command-receipt idempotency contract every other public
// command in this codebase already uses (internal/app/catalog.CreateProject
// et al): a receipt/hash lookup happens BEFORE any real
// filesystem/process I/O, a probe replay returns the EXACT stored
// candidate even once its own expiry has passed (replay answers "what did
// this exact command produce", never "is that answer still fresh"), and a
// genuinely new idempotency key always probes/re-probes for real. This is
// a deliberately DIFFERENT axis from the pre-existing content-hash
// (fingerprint) dedup ports.AdapterBuildRepository.InsertIfAbsent already
// gives Build.ID() — fingerprint dedup answers "have we ever registered
// this exact measured tuple before" (an operational fact about the
// registry), while command-receipt replay answers "have we ever run this
// exact command before" (a fact about a specific caller's specific
// request) — a distinct command (distinct Actor/IdempotencyKey) that
// happens to reference an already-registered fingerprint still gets its
// own receipt, and a duplicated command against a fresh fingerprint would
// still be rejected at the fingerprint layer's own immutability
// guarantee. Both mechanisms co-exist; neither substitutes for the other.
package adapterbuild

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// tokenTTL is how long a candidate token remains valid — ADR-022 asks
// for "expiry ngắn" (short expiry) without naming a duration; 5 minutes
// is long enough for an operator to read the printed candidate and
// confirm registration, short enough that a stale, unused token is never
// a standing risk. This is the CANDIDATE's own freshness window, entirely
// separate from command-receipt replay: a probe REPLAY always returns the
// original candidate verbatim regardless of tokenTTL, since replay is
// about command identity, not candidate freshness — see this package's
// own doc comment.
const tokenTTL = 5 * time.Minute

// ErrExecutableDrift is returned by RegisterAdapterBuild when the
// executable at the token's bound path no longer hashes to the content
// the token pinned — this is V2-07A's own "build khác exact pin bị
// reject" bar, enforced by re-measuring outside the transaction and
// comparing against what the operator actually confirmed (ADR-022's
// TOCTOU-closing re-probe step).
var ErrExecutableDrift = errors.New("adapterbuild: executable no longer matches the content hash the candidate token pinned")

// ErrCapabilityManifestDrift is returned by RegisterAdapterBuild when the
// freshly-supplied capability manifest does not hash to what the token
// bound — the same re-measurement principle applied to the declared
// manifest, not just the executable file.
var ErrCapabilityManifestDrift = errors.New("adapterbuild: capability manifest no longer matches what the candidate token pinned")

// requireInstallationScope rejects any cmd.Scope that is not
// ports.InstallationScope() with ports.ErrScopeMismatch — the same shared
// sentinel internal/app/catalog.CreateProject/ListProjects already
// established for this identical check (V6-03, ADR-025's own "Handler
// MUST validate scope khớp command type" line). ADR-025's own closed
// installation list names both ProbeAdapterBuild and RegisterAdapterBuild
// explicitly, so a project-scoped envelope for either is a caller bug,
// rejected here rather than silently accepted with an ignored ProjectID
// (this task's own "Không làm: ... ProjectID" scope line).
func requireInstallationScope(cmd ports.Command) error {
	if !cmd.Scope.IsInstallation() {
		return fmt.Errorf("adapterbuild: %s is installation-scoped (ADR-025); a project-scoped command envelope is rejected: %w", cmd.Type, ports.ErrScopeMismatch)
	}
	return nil
}

// ProbeRequest is what a caller supplies to ProbeAdapterBuild.
type ProbeRequest struct {
	ProviderKey        string
	ExecutablePath     string
	ProtocolVersion    string
	CapabilityManifest adapterbuild.CapabilityManifest
	OS                 string
	Toolchain          string
	ConfigIdentity     string
}

func (req ProbeRequest) validate() error {
	if strings.TrimSpace(req.ProviderKey) == "" {
		return errors.New("adapterbuild: providerKey is required")
	}
	if strings.TrimSpace(req.ExecutablePath) == "" {
		return errors.New("adapterbuild: executablePath is required")
	}
	if strings.TrimSpace(req.ProtocolVersion) == "" {
		return errors.New("adapterbuild: protocolVersion is required")
	}
	if err := adapterbuild.ValidateCapabilityManifest(req.CapabilityManifest); err != nil {
		return err
	}
	if strings.TrimSpace(req.OS) == "" || strings.TrimSpace(req.Toolchain) == "" || strings.TrimSpace(req.ConfigIdentity) == "" {
		return errors.New("adapterbuild: os, toolchain and configIdentity are all required")
	}
	return nil
}

// ProbeAdapterBuild measures the configured executable's content hash
// and the declared capability manifest's hash, binds the whole tuple
// into a server-signed CandidateToken, and returns it — without ever
// mutating the adapter build registry itself (go-core-spec.md's own
// command table: "không mutate registry"). The one write it may perform
// is lazily bootstrapping the per-installation signing key on first use,
// which is infrastructure the registry depends on, not a registry row.
//
// V6-10I: before ever touching the real executable (a probe spawns/reads
// the configured file — expensive, side-effecting I/O), this looks up
// cmd's own command-receipt. A prior receipt with the same RequestHash is
// a replay: it returns the EXACT candidate token originally produced —
// same nonce, same ExpiresAt, even once that ExpiresAt has since passed —
// without touching the filesystem again. A prior receipt with a
// different RequestHash is ports.ErrReceiptConflict. No prior receipt at
// all means a genuinely new idempotency key, which always probes for
// real.
func ProbeAdapterBuild(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req ProbeRequest) (adapterbuild.CandidateToken, error) {
	if err := requireInstallationScope(cmd); err != nil {
		return adapterbuild.CandidateToken{}, err
	}
	if err := req.validate(); err != nil {
		return adapterbuild.CandidateToken{}, err
	}

	replayed, found, err := loadProbeReplay(ctx, uow, cmd)
	if err != nil {
		return adapterbuild.CandidateToken{}, err
	}
	if found {
		return replayed, nil
	}

	contentHash, err := HashExecutableFile(req.ExecutablePath)
	if err != nil {
		return adapterbuild.CandidateToken{}, fmt.Errorf("adapterbuild: measure executable: %w", err)
	}
	_, manifestHash, err := adapterbuild.HashCapabilityManifest(req.CapabilityManifest)
	if err != nil {
		return adapterbuild.CandidateToken{}, err
	}

	tuple := adapterbuild.CandidateTuple{
		ProviderKey:            req.ProviderKey,
		ExecutablePath:         req.ExecutablePath,
		ExecutableContentHash:  contentHash,
		ProtocolVersion:        req.ProtocolVersion,
		CapabilityManifestHash: manifestHash,
		OS:                     req.OS,
		Toolchain:              req.Toolchain,
		ConfigIdentity:         req.ConfigIdentity,
	}

	nonce, err := randomNonce()
	if err != nil {
		return adapterbuild.CandidateToken{}, err
	}
	expiresAt := time.Now().UTC().Add(tokenTTL)

	var token adapterbuild.CandidateToken
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		key, err := tx.AdapterBuilds().LoadOrCreateSigningKey(ctx)
		if err != nil {
			return err
		}
		signed, err := adapterbuild.SignToken(tuple, nonce, expiresAt, key)
		if err != nil {
			return err
		}
		resultJSON, err := json.Marshal(signed)
		if err != nil {
			return fmt.Errorf("adapterbuild: marshal probe receipt result: %w", err)
		}
		if err := tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}
		// Re-load rather than trust `signed` directly: two callers racing
		// the exact same idempotency key each sign their OWN candidate
		// (different nonce/expiry) before either has recorded a receipt;
		// SQLite serializes the two WithSerializedWrite calls, so exactly
		// one Record actually inserts and the other's Record is a silent
		// no-op replay (receiptsRepository.Record's own ON CONFLICT DO
		// NOTHING). Re-loading here guarantees BOTH callers hand back
		// whichever token actually got persisted first — never a losing
		// writer's own phantom, never-stored token.
		persisted, ok, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("adapterbuild: probe receipt not found immediately after recording")
		}
		return json.Unmarshal([]byte(persisted.ResultJSON), &token)
	})
	return token, err
}

// loadProbeReplay checks cmd's own command-receipt, entirely via a
// read-only transaction (a DB read, never filesystem/process I/O) —
// ProbeAdapterBuild's own "receipt/hash lookup before external I/O" step.
func loadProbeReplay(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command) (adapterbuild.CandidateToken, bool, error) {
	var receipt ports.Receipt
	var found bool
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		r, ok, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		receipt, found = r, ok
		return err
	})
	if err != nil || !found {
		return adapterbuild.CandidateToken{}, false, err
	}
	if receipt.RequestHash != cmd.RequestHash {
		return adapterbuild.CandidateToken{}, false, ports.ErrReceiptConflict
	}
	var token adapterbuild.CandidateToken
	if err := json.Unmarshal([]byte(receipt.ResultJSON), &token); err != nil {
		return adapterbuild.CandidateToken{}, false, fmt.Errorf("adapterbuild: decode stored probe receipt: %w", err)
	}
	return token, true, nil
}

// RegisterRequest is what a caller supplies to RegisterAdapterBuild. The
// caller supplies CapabilityManifest again (not just the token) so
// Register can re-derive its hash and compare against what the token
// bound, the same TOCTOU-closing re-measurement ADR-022 requires of the
// executable file. RegisteredBy is deliberately NOT a field here (V6-10I):
// it is always derived from cmd.Actor — see RegisterAdapterBuild's own
// doc comment.
type RegisterRequest struct {
	Token              adapterbuild.CandidateToken
	CapabilityManifest adapterbuild.CapabilityManifest
}

// RegisterResult is what RegisterAdapterBuild returns.
type RegisterResult struct {
	Build          adapterbuild.Build
	AlreadyExisted bool
}

// registerReceiptPayload is RegisterResult's own storage/wire shape for
// the command-receipt idempotency ledger. adapterbuild.Build has no
// exported fields at all (its own doc comment: immutability is
// structural, accessor-only) — json.Marshal(RegisterResult{...}) directly
// would silently encode the Build half as "{}" — so this DTO round-trips
// through Build's own accessors and adapterbuild.NewBuild instead, the
// same technique cmd/aw/adapter.go's own adapterBuildView already uses
// for CLI JSON output.
type registerReceiptPayload struct {
	Tuple              adapterbuild.CandidateTuple     `json:"tuple"`
	CapabilityManifest adapterbuild.CapabilityManifest `json:"capabilityManifest"`
	RegisteredBy       string                          `json:"registeredBy"`
	RegisteredAt       time.Time                        `json:"registeredAt"`
	AlreadyExisted     bool                             `json:"alreadyExisted"`
}

func newRegisterReceiptPayload(result RegisterResult) registerReceiptPayload {
	return registerReceiptPayload{
		Tuple: result.Build.Tuple(), CapabilityManifest: result.Build.CapabilityManifest(),
		RegisteredBy: result.Build.RegisteredBy(), RegisteredAt: result.Build.RegisteredAt(),
		AlreadyExisted: result.AlreadyExisted,
	}
}

func (p registerReceiptPayload) toResult() (RegisterResult, error) {
	build, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple: p.Tuple, CapabilityManifest: p.CapabilityManifest,
		RegisteredBy: p.RegisteredBy, RegisteredAt: p.RegisteredAt,
	})
	if err != nil {
		return RegisterResult{}, err
	}
	return RegisterResult{Build: build, AlreadyExisted: p.AlreadyExisted}, nil
}

// loadRegisterReplay is RegisterAdapterBuild's own "receipt/hash lookup
// before external I/O" step — checked before VerifyToken/re-probe/re-hash
// ever run, so a genuine replay (same IdempotencyKey+RequestHash) never
// re-verifies or re-touches the filesystem at all; this is what makes a
// crash-after-commit-before-ack retry safe to simply call again.
func loadRegisterReplay(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command) (RegisterResult, bool, error) {
	var receipt ports.Receipt
	var found bool
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		r, ok, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		receipt, found = r, ok
		return err
	})
	if err != nil || !found {
		return RegisterResult{}, false, err
	}
	if receipt.RequestHash != cmd.RequestHash {
		return RegisterResult{}, false, ports.ErrReceiptConflict
	}
	var payload registerReceiptPayload
	if err := json.Unmarshal([]byte(receipt.ResultJSON), &payload); err != nil {
		return RegisterResult{}, false, fmt.Errorf("adapterbuild: decode stored register receipt: %w", err)
	}
	result, err := payload.toResult()
	if err != nil {
		return RegisterResult{}, false, err
	}
	return result, true, nil
}

// RegisterAdapterBuild verifies req.Token (signature and expiry), then —
// entirely outside any database transaction, per ADR-022's own explicit
// ordering ("re-probe/re-hash executable ngoài database transaction,
// ngay trước commit") — re-measures the executable file and the supplied
// capability manifest, rejecting with ErrExecutableDrift/
// ErrCapabilityManifestDrift if either no longer matches what the token
// pinned. Only once both match does it open a transaction, and even then
// persists only the values it just measured server-side, never anything
// the token or the caller merely claimed.
//
// V6-10I: cmd.Actor supplies RegisteredBy — never a separate
// caller-supplied field (RegisterRequest carries none) — the same
// already-vetted-by-the-caller trust boundary ports.Command.Actor sits on
// everywhere else in this codebase. Before any of the above (including
// the token verification and re-probe), a command-receipt lookup runs
// first: a replay (same IdempotencyKey+RequestHash) returns the exact
// original RegisterResult without re-verifying or re-touching the
// filesystem at all. Only a genuinely new key re-probes outside the
// transaction and then, atomically inside one WithSerializedWrite call,
// inserts the Build row if-absent, appends AdapterBuildRegistered (only
// for a genuinely new row — a content-duplicate registration under a
// DIFFERENT command never re-emits the event for an aggregate that
// already has one), and records the receipt.
func RegisterAdapterBuild(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req RegisterRequest) (RegisterResult, error) {
	if err := requireInstallationScope(cmd); err != nil {
		return RegisterResult{}, err
	}
	if strings.TrimSpace(cmd.Actor) == "" {
		return RegisterResult{}, errors.New("adapterbuild: cmd.Actor is required (RegisterAdapterBuild derives RegisteredBy from it)")
	}

	replayed, found, err := loadRegisterReplay(ctx, uow, cmd)
	if err != nil {
		return RegisterResult{}, err
	}
	if found {
		return replayed, nil
	}

	var key []byte
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().LoadSigningKey(ctx)
		key = loaded
		return err
	}); err != nil {
		return RegisterResult{}, err
	}
	if err := adapterbuild.VerifyToken(req.Token, key, time.Now().UTC()); err != nil {
		return RegisterResult{}, err
	}

	freshExecutableHash, err := HashExecutableFile(req.Token.Tuple.ExecutablePath)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("adapterbuild: re-measure executable: %w", err)
	}
	if freshExecutableHash != req.Token.Tuple.ExecutableContentHash {
		return RegisterResult{}, ErrExecutableDrift
	}
	_, freshManifestHash, err := adapterbuild.HashCapabilityManifest(req.CapabilityManifest)
	if err != nil {
		return RegisterResult{}, err
	}
	if freshManifestHash != req.Token.Tuple.CapabilityManifestHash {
		return RegisterResult{}, ErrCapabilityManifestDrift
	}

	build, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple:              req.Token.Tuple,
		CapabilityManifest: req.CapabilityManifest,
		RegisteredBy:       cmd.Actor,
		RegisteredAt:       time.Now().UTC(),
	})
	if err != nil {
		return RegisterResult{}, err
	}

	var result RegisterResult
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		persisted, alreadyExisted, err := tx.AdapterBuilds().InsertIfAbsent(ctx, build)
		if err != nil {
			return err
		}

		if !alreadyExisted {
			tuple := persisted.Tuple()
			eventPayload, err := json.Marshal(adapterBuildRegisteredEventPayload{
				BuildID: persisted.ID(), ProviderKey: tuple.ProviderKey,
				ExecutablePath: tuple.ExecutablePath, ExecutableContentHash: tuple.ExecutableContentHash,
				ProtocolVersion: tuple.ProtocolVersion, OS: tuple.OS,
				Toolchain: tuple.Toolchain, ConfigIdentity: tuple.ConfigIdentity,
				RegisteredBy: persisted.RegisteredBy(), RegisteredAt: persisted.RegisteredAt(),
			})
			if err != nil {
				return fmt.Errorf("adapterbuild: marshal AdapterBuildRegistered payload: %w", err)
			}
			// AggregateID is the new Build's own content-addressed ID;
			// Sequence=1 is safe because a Build row is only ever inserted
			// once (InsertIfAbsent's own immutability guarantee) — the
			// alreadyExisted guard above is what keeps this branch from
			// ever running a second time for the same aggregate.
			if err := tx.Events().Append(ctx, ports.DomainEvent{
				ID: cmd.ID + "-registered", ProjectID: "",
				AggregateType: "AdapterBuildVersion", AggregateID: persisted.ID(), Sequence: 1,
				EventType: AdapterBuildRegisteredEventType, SchemaVersion: AdapterBuildRegisteredSchemaVersion,
				PayloadJSON: string(eventPayload), CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
			}); err != nil {
				return err
			}
		}

		computed := RegisterResult{Build: persisted, AlreadyExisted: alreadyExisted}
		resultJSON, err := json.Marshal(newRegisterReceiptPayload(computed))
		if err != nil {
			return fmt.Errorf("adapterbuild: marshal register receipt result: %w", err)
		}
		if err := tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		// Re-load rather than trust `computed` directly — the same
		// "a losing concurrent writer must hand back whichever result
		// actually got persisted first" discipline ProbeAdapterBuild's own
		// write closure follows above.
		persistedReceipt, ok, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("adapterbuild: register receipt not found immediately after recording")
		}
		var payload registerReceiptPayload
		if err := json.Unmarshal([]byte(persistedReceipt.ResultJSON), &payload); err != nil {
			return err
		}
		result, err = payload.toResult()
		return err
	})
	return result, err
}

// ListAdapterBuilds returns every registered build.
func ListAdapterBuilds(ctx context.Context, uow ports.UnitOfWork) ([]adapterbuild.Build, error) {
	var builds []adapterbuild.Build
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().List(ctx)
		builds = loaded
		return err
	})
	return builds, err
}

// GetAdapterBuild returns the build with the given ID, or
// ports.ErrAdapterBuildNotFound.
func GetAdapterBuild(ctx context.Context, uow ports.UnitOfWork, id string) (adapterbuild.Build, error) {
	var build adapterbuild.Build
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().Get(ctx, id)
		build = loaded
		return err
	})
	return build, err
}

// HashExecutableFile returns the sha256 content hash of the file at path,
// prefixed "sha256:" — exported so VerifyNoDrift (drift.go) can reuse the
// exact same measurement ProbeAdapterBuild/RegisterAdapterBuild already use
// for registration, rather than a second, potentially-diverging
// implementation.
func HashExecutableFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func randomNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("adapterbuild: generate nonce: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
