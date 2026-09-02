package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/block"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/engineeringpack"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// runDefinition is the "definition" command group's dispatcher (V2-11,
// docs/design/04-v2-definition-plane.md): create|validate|publish|list|
// show|diff against the application-command layer V2-10 already built
// (internal/app/definitions), the same "CLI is a thin wrapper, never a
// second implementation of compiler/repository logic" discipline
// adapter.go (V2-07B) already established for the "adapter" command
// group. This file is what makes V2-11's own "Hoàn thành khi" bar true:
// an operator can author -> validate -> publish -> inspect -> diff any
// of the nine DefinitionKinds without ever touching SQLite by hand.
func runDefinition(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 {
		return usageError{errors.New("expected 'definition create|validate|publish|list|show|diff'")}
	}
	verb := arguments[0]
	rest := arguments[1:]
	switch verb {
	case "create":
		return runDefinitionCreate(rest, stdout)
	case "validate":
		return runDefinitionValidate(rest, stdout)
	case "publish":
		return runDefinitionPublish(rest, stdout)
	case "list":
		return runDefinitionList(rest, stdout)
	case "show":
		return runDefinitionShow(rest, stdout)
	case "diff":
		return runDefinitionDiff(rest, stdout)
	default:
		return usageError{fmt.Errorf("unknown definition subcommand %q (want create|validate|publish|list|show|diff)", verb)}
	}
}

// runDefinitionCreate creates a new Definition (DRAFT, generation 1) for
// any of the nine DefinitionKinds -- the one manual step
// PublishDefinitionVersion needs a real Definition row to exist against
// (see this file's own top doc comment and the PR description for why
// this is a separate, explicit subcommand rather than folded into
// 'publish' auto-creation).
func runDefinitionCreate(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("definition create", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	kindRaw := flags.String("kind", "", "definition kind (WORKFLOW, BLOCK, SKILL, LAYER, ENGINEERING_PACK, AGENT_PROFILE, COMMAND, GATE, POLICY)")
	definitionID := flags.String("definition-id", "", "definition id to create")
	name := flags.String("name", "", "human-readable name")
	projectID := flags.String("project-id", "", "project id to scope this definition to (omit for a global/installation-wide definition)")
	actor := flags.String("actor", "operator", "operator identity performing this create")
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for this create command (reuse the same value to safely retry)")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	kind, err := parseDefinitionKind(*kindRaw)
	if err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*definitionID) == "" {
		return usageError{errors.New("--definition-id is required")}
	}
	if strings.TrimSpace(*name) == "" {
		return usageError{errors.New("--name is required")}
	}
	if strings.TrimSpace(*idempotencyKey) == "" {
		return usageError{errors.New("--idempotency-key is required")}
	}

	defScope, cmdScope := resolveDefinitionScopes(*projectID)

	ctx := context.Background()
	store, uow, err := openDefinitionDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	cmd := newDefinitionCommand("CreateDefinition", *idempotencyKey, *actor, cmdScope,
		requestHash(string(kind), *definitionID, *projectID, *name))
	result, err := definitions.CreateDefinition(ctx, uow, cmd, definitions.CreateDefinitionRequest{
		DefinitionID: *definitionID, Kind: kind, Scope: defScope, Name: *name,
	})
	if err != nil {
		return mapReceiptConflict(err)
	}
	return writeStableJSON(stdout, result)
}

// runDefinitionValidate compiles (and, for Workflow, resolves every
// dependency pin against the real registry) a candidate Version without
// publishing it -- a read-only dry run, no command envelope, no receipt.
func runDefinitionValidate(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("definition validate", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	kindRaw := flags.String("kind", "", "definition kind (WORKFLOW, BLOCK, SKILL, LAYER, ENGINEERING_PACK, AGENT_PROFILE, COMMAND, GATE, POLICY)")
	definitionID := flags.String("definition-id", "", "definition id the draft would publish against")
	filePath := flags.String("file", "", "path to the authored document (JSON or YAML), or '-' for stdin")
	formatRaw := flags.String("format", "json", "document format: json or yaml (WORKFLOW documents are always json)")
	schemaVersion := flags.Int("schema-version", 1, "document schema version (ignored for WORKFLOW, whose schemaVersion is a field of the document itself)")
	actor := flags.String("actor", "operator", "operator identity recorded as publishedBy for this dry run")
	name := flags.String("name", "", "workflow name (WORKFLOW only; must match the --name given to 'definition create' for this definition id -- see this file's own doc comment on why)")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	kind, err := parseDefinitionKind(*kindRaw)
	if err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*definitionID) == "" {
		return usageError{errors.New("--definition-id is required")}
	}
	if strings.TrimSpace(*filePath) == "" {
		return usageError{errors.New("--file is required")}
	}
	docFormat, err := parseDocumentFormat(*formatRaw)
	if err != nil {
		return usageError{err}
	}
	raw, err := readDocumentBytes(*filePath)
	if err != nil {
		return usageError{fmt.Errorf("read document: %w", err)}
	}

	ctx := context.Background()
	store, uow, err := openDefinitionDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	req, err := buildValidateDraftRequest(kind, *definitionID, *name, raw, docFormat, *schemaVersion, *actor)
	if err != nil {
		return usageError{err}
	}

	fields, err := definitions.ValidateDraft(ctx, uow, req)
	if err != nil {
		return err
	}
	return writeStableJSON(stdout, newVersionFieldsView(fields))
}

// runDefinitionPublish compiles and publishes a real new Version -- the
// atomic "publish event + version + receipt" bar V2-10 already built,
// wired to a file on disk instead of a Go caller.
func runDefinitionPublish(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("definition publish", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	kindRaw := flags.String("kind", "", "definition kind (WORKFLOW, BLOCK, SKILL, LAYER, ENGINEERING_PACK, AGENT_PROFILE, COMMAND, GATE, POLICY)")
	definitionID := flags.String("definition-id", "", "definition id to publish against (must already exist -- see 'definition create')")
	filePath := flags.String("file", "", "path to the authored document (JSON or YAML), or '-' for stdin")
	formatRaw := flags.String("format", "json", "document format: json or yaml (WORKFLOW documents are always json)")
	schemaVersion := flags.Int("schema-version", 1, "document schema version (ignored for WORKFLOW, whose schemaVersion is a field of the document itself)")
	actor := flags.String("actor", "operator", "operator identity recorded as publishedBy")
	idempotencyKey := flags.String("idempotency-key", "", "idempotency key for this publish command (reuse the same value to safely retry)")
	projectID := flags.String("project-id", "", "project id this command is scoped to (must match the definition's own scope; omit for global)")
	name := flags.String("name", "", "workflow name (WORKFLOW only, required; must match the --name given to 'definition create' for this definition id -- see this file's own doc comment on why)")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	kind, err := parseDefinitionKind(*kindRaw)
	if err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*definitionID) == "" {
		return usageError{errors.New("--definition-id is required")}
	}
	if strings.TrimSpace(*filePath) == "" {
		return usageError{errors.New("--file is required")}
	}
	if strings.TrimSpace(*idempotencyKey) == "" {
		return usageError{errors.New("--idempotency-key is required")}
	}
	if kind == definition.KindWorkflow && strings.TrimSpace(*name) == "" {
		return usageError{errors.New("--name is required for --kind WORKFLOW (must match the --name given to 'definition create')")}
	}
	docFormat, err := parseDocumentFormat(*formatRaw)
	if err != nil {
		return usageError{err}
	}
	raw, err := readDocumentBytes(*filePath)
	if err != nil {
		return usageError{fmt.Errorf("read document: %w", err)}
	}

	ctx := context.Background()
	store, uow, err := openDefinitionDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	versionNumber := 1
	if kind == definition.KindWorkflow {
		// Unlike the eight shared kinds (whose VersionNumber is a
		// discarded placeholder -- internal/adapters/sqlite's own
		// publishSharedDefinitionVersionTx always reallocates
		// nextVersionNo from the database, ignoring the candidate's
		// value entirely), Workflow's own publishWorkflowVersionTx
		// genuinely compares candidate.VersionNumber() against the real
		// next version number and rejects a mismatch as
		// ports.ErrOptimisticConflict. So only for Workflow this CLI
		// must ask the registry what the next version actually is
		// before compiling the candidate.
		existing, err := definitions.ListVersions(ctx, uow, definition.KindWorkflow, *definitionID)
		if err != nil {
			return err
		}
		versionNumber = len(existing) + 1
	}

	_, cmdScope := resolveDefinitionScopes(*projectID)
	cmd := newDefinitionCommand("PublishDefinitionVersion", *idempotencyKey, *actor, cmdScope,
		requestHash(string(kind), *definitionID, strconv.Itoa(*schemaVersion), strconv.Itoa(int(docFormat)), string(raw)))

	req, err := buildPublishRequest(kind, *definitionID, *name, raw, docFormat, *schemaVersion, versionNumber, *actor)
	if err != nil {
		return usageError{err}
	}

	result, err := definitions.PublishDefinitionVersion(ctx, uow, cmd, req)
	if err != nil {
		return mapReceiptConflict(err)
	}
	return writeStableJSON(stdout, newVersionFieldsView(result))
}

// runDefinitionList prints every Version definitionID has published, as
// a stable JSON array (oldest first, definitions.ListVersions' own
// order).
func runDefinitionList(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("definition list", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	kindRaw := flags.String("kind", "", "definition kind (WORKFLOW, BLOCK, SKILL, LAYER, ENGINEERING_PACK, AGENT_PROFILE, COMMAND, GATE, POLICY)")
	definitionID := flags.String("definition-id", "", "definition id to list published versions for")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	kind, err := parseDefinitionKind(*kindRaw)
	if err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*definitionID) == "" {
		return usageError{errors.New("--definition-id is required")}
	}

	ctx := context.Background()
	store, uow, err := openDefinitionDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	versions, err := definitions.ListVersions(ctx, uow, kind, *definitionID)
	if err != nil {
		return err
	}
	views := make([]versionFieldsView, len(versions))
	for i, v := range versions {
		views[i] = newVersionFieldsView(v)
	}
	return writeStableJSON(stdout, views)
}

// runDefinitionShow prints one published Version as stable JSON, or a
// clean "not found" error -- never a raw ports.ErrDefinitionVersionNotFound
// dump -- for an unknown id, mirroring runAdapterShow's own convention.
func runDefinitionShow(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("definition show", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	versionID := flags.String("version-id", "", "published version id")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	if strings.TrimSpace(*versionID) == "" {
		return usageError{errors.New("--version-id is required")}
	}

	ctx := context.Background()
	store, uow, err := openDefinitionDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	fields, err := loadAnyVersion(ctx, uow, store, *versionID)
	if err != nil {
		return mapVersionNotFound(err, *versionID)
	}
	return writeStableJSON(stdout, newVersionFieldsView(fields))
}

// runDefinitionDiff loads two published Versions and prints a structured
// comparison: their identity/hash summaries plus a line-oriented diff of
// their pretty-printed CanonicalSource (see prettyJSONLines/diffLines'
// own doc comments for why re-indenting first, rather than diffing the
// single-line canonical JSON verbatim, is what makes this actually
// useful). There is no prescribed diff format anywhere in the design
// docs for this subcommand (confirmed absent the same way V2-09/V2-10's
// own list/load naming gap was) -- this shape was chosen because it is
// low-complexity, dependency-free (no diff library exists in go.mod) and
// answers the two questions an operator actually has: "did anything
// change" (Identical) and "what changed" (SourceDiff).
func runDefinitionDiff(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("definition diff", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	versionIDA := flags.String("version-id-a", "", "first version id")
	versionIDB := flags.String("version-id-b", "", "second version id")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	if strings.TrimSpace(*versionIDA) == "" || strings.TrimSpace(*versionIDB) == "" {
		return usageError{errors.New("--version-id-a and --version-id-b are required")}
	}

	ctx := context.Background()
	store, uow, err := openDefinitionDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	a, err := loadAnyVersion(ctx, uow, store, *versionIDA)
	if err != nil {
		return mapVersionNotFound(err, *versionIDA)
	}
	b, err := loadAnyVersion(ctx, uow, store, *versionIDB)
	if err != nil {
		return mapVersionNotFound(err, *versionIDB)
	}
	return writeStableJSON(stdout, newVersionDiffView(a, b))
}

// loadAnyVersion resolves versionID against either persistence layout a
// published Version might live in. definitions.LoadVersion (V2-10's own
// application-layer query) only ever resolves the eight shared kinds'
// table -- see ports.DefinitionsRepository.LoadVersion's own doc
// comment: "It only ever resolves a shared-kind Version ... Workflow
// keeps its own dedicated tables". Rather than leaving 'show'/'diff'
// unable to inspect a Workflow-kind version at all (there is no --kind
// flag on either subcommand to route by, and adding one would make an
// operator supply information they may not have handy -- a bare version
// id, unlike a DefinitionID+Kind pair, is not naturally paired with its
// own kind in an operator's head), this falls back to
// store.LoadWorkflowVersion -- an already-exported, already-tested method
// on the very *sqlite.Store this file already opens for --db, so this is
// still "calling a real Go API", never "editing SQLite by hand" (V2-11's
// own "Hoàn thành khi" bar) -- only when the shared-table lookup
// specifically reports ports.ErrDefinitionVersionNotFound. Any other
// error (e.g. a genuine I/O failure) propagates as-is, never masked by a
// second lookup attempt.
func loadAnyVersion(ctx context.Context, uow ports.UnitOfWork, store *sqlite.Store, versionID string) (definition.VersionFields, error) {
	fields, err := definitions.LoadVersion(ctx, uow, versionID)
	if err == nil {
		return fields, nil
	}
	if !errors.Is(err, ports.ErrDefinitionVersionNotFound) {
		return definition.VersionFields{}, err
	}
	workflowVersion, workflowErr := store.LoadWorkflowVersion(ctx, workflow.WorkflowVersionID(versionID))
	if workflowErr != nil {
		if errors.Is(workflowErr, ports.ErrPersistenceNotFound) {
			return definition.VersionFields{}, err // the original, shared-table not-found error
		}
		return definition.VersionFields{}, workflowErr
	}
	return workflowVersionFields(workflowVersion)
}

// workflowVersionFields converts a real, already-published
// workflow.WorkflowVersion into the kind-agnostic definition.VersionFields
// shape every other subcommand already prints. This duplicates
// internal/app/definitions' own unexported workflowVersionToVersionFields
// (commands.go) and internal/adapters/sqlite's own
// workflowVersionToDefinitionFields (definitions.go) rather than sharing
// code with either -- both are private to their own packages for the
// exact reasons commands.go's own doc comment on that function already
// gives (this package must never import the sqlite adapter's internals
// beyond its already-open *sqlite.Store handle, and reaching into a
// sibling package's unexported function is not an available option
// regardless); the same reasoning applies here a third time.
func workflowVersionFields(v workflow.WorkflowVersion) (definition.VersionFields, error) {
	var dependencies definition.DependencyManifest
	for _, pin := range v.Dependencies().Pins {
		dependencies.Pins = append(dependencies.Pins, definition.DependencyPin{
			Kind: definition.Kind(pin.Kind), DefinitionID: pin.Key, VersionID: pin.Version,
		})
	}
	schemaVersion, _ := strconv.Atoi(v.SchemaVersion())
	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: string(v.ID()), DefinitionID: string(v.DefinitionID()), Kind: definition.KindWorkflow,
		VersionNumber: v.VersionNumber(), SchemaVersion: schemaVersion,
		CanonicalSource: string(v.CanonicalContent()), SourceHash: v.ContentHash(),
		CompiledSnapshot: string(v.CanonicalContent()), CompiledHash: v.ContentHash(),
		Dependencies: dependencies, PublishedBy: v.PublishedBy(), PublishedAt: v.PublishedAt(),
	})
}

// openDefinitionDB opens a real sqlite-backed ports.UnitOfWork at
// dbPath -- the same minimal --db-flag-to-sqlite.Open wiring
// openAdapterBuildDB (adapter.go, V2-07B) already established, kept as
// its own small local helper rather than reached-into-and-reused so this
// file stays self-contained the same way adapter.go is.
func openDefinitionDB(ctx context.Context, dbPath string) (*sqlite.Store, ports.UnitOfWork, error) {
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	return store, sqlite.NewUnitOfWork(store), nil
}

// parseDefinitionKind validates raw against the nine closed
// DefinitionKind values, case-insensitively, so --kind block and --kind
// BLOCK both work.
func parseDefinitionKind(raw string) (definition.Kind, error) {
	kind := definition.Kind(strings.ToUpper(strings.TrimSpace(raw)))
	if !kind.Valid() {
		return "", fmt.Errorf("unknown --kind %q (want one of WORKFLOW, BLOCK, SKILL, LAYER, ENGINEERING_PACK, AGENT_PROFILE, COMMAND, GATE, POLICY)", raw)
	}
	return kind, nil
}

// parseDocumentFormat validates --format against the two authoring.Format
// values this codebase's DecodeStrict supports.
func parseDocumentFormat(raw string) (authoring.Format, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "json":
		return authoring.FormatJSON, nil
	case "yaml", "yml":
		return authoring.FormatYAML, nil
	default:
		return 0, fmt.Errorf("unknown --format %q (want json or yaml)", raw)
	}
}

// readDocumentBytes reads path, or stdin when path is "-" -- the same
// convention adapter.go's own readCandidateToken already established for
// --token.
func readDocumentBytes(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// resolveDefinitionScopes derives both scope representations this file
// needs from one --project-id flag value: definition.Scope (what
// CreateDefinition persists) and ports.CommandScope (what the command
// envelope's idempotency/receipt lookup keys on) always agree with each
// other, since a mismatched pair would let an operator's --idempotency-key
// collide across a project/global boundary that CreateDefinitionRequest
// itself does not see.
func resolveDefinitionScopes(projectID string) (definition.Scope, ports.CommandScope) {
	if strings.TrimSpace(projectID) == "" {
		return definition.GlobalScope(), ports.InstallationScope()
	}
	return definition.ProjectScope(project.ProjectID(projectID)), ports.ProjectScope(projectID)
}

// requestHash deterministically hashes every semantically meaningful
// part of a command's request payload into the RequestHash
// ports.Command carries (see internal/app/ports/command.go's own
// ErrReceiptConflict doc comment): two calls with the same
// --idempotency-key but different --file content (or different --kind,
// --definition-id, ...) must never silently replay one another's
// result -- they must conflict. No production call site in this
// codebase computed a real RequestHash before this file (every existing
// caller is test code that hardcodes a literal string, e.g.
// internal/adapters/sqlite/command_handler_example_test.go's "hash-a"),
// so this is a new, first real answer to "how", not a reuse of an
// existing convention -- it follows internal/domain/authoring.Canonicalize's
// own "sha256:<hex>" hash-string convention for consistency with every
// other content hash this codebase already prints.
func requestHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		// A NUL separator (never a legal byte in any of these string
		// parts -- kind names, ids, JSON/YAML text) prevents two
		// different (parts...) slices from ever hashing identically by
		// concatenation ambiguity (e.g. ["ab", "c"] vs ["a", "bc"]).
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// newDefinitionCommand builds the ports.Command envelope
// CreateDefinition/PublishDefinitionVersion require, following the same
// field-population convention command_handler_example_test.go's own
// newCreateProjectCommand established (ID/CorrelationID derived from the
// command type + idempotency key, so they are stable and reproducible
// across a retry, never a fresh random value that would defeat log
// correlation across the retried attempts).
func newDefinitionCommand(commandType, idempotencyKey, actor string, scope ports.CommandScope, hash string) ports.Command {
	id := commandType + "-" + idempotencyKey
	return ports.Command{
		ID: id, IdempotencyKey: idempotencyKey, Actor: actor,
		CorrelationID: id, Scope: scope, RequestedAt: time.Now().UTC(),
		Type: commandType, RequestHash: hash,
	}
}

// mapReceiptConflict maps ports.ErrReceiptConflict to a clean,
// operator-facing message: the same --idempotency-key was reused for a
// genuinely different request (a different --file, --kind, etc.), which
// is a usage mistake, not an internal failure.
func mapReceiptConflict(err error) error {
	if errors.Is(err, ports.ErrReceiptConflict) {
		return fmt.Errorf("--idempotency-key was already used for a different request; use a fresh --idempotency-key, or retry with the exact same request to replay: %w", err)
	}
	return err
}

// mapVersionNotFound maps ports.ErrDefinitionVersionNotFound to a clean
// message naming the id that failed to resolve, never a raw sentinel
// dump -- mirroring runAdapterShow's own ports.ErrAdapterBuildNotFound
// handling.
func mapVersionNotFound(err error, versionID string) error {
	if errors.Is(err, ports.ErrDefinitionVersionNotFound) {
		return fmt.Errorf("definition version %q not found", versionID)
	}
	return err
}

// candidateVersionID mints a fresh, globally unique version id via
// internal/app/idsource -- this codebase's own established "application
// code always creates the ID before the first write" convention
// (idsource's own package doc comment). This matters more than it might
// look: internal/adapters/sqlite's definition_versions.id (and
// workflow_versions.id) are both PRIMARY KEY columns storing the
// candidate's own VersionID verbatim -- publishSharedDefinitionVersionTx
// inserts req.VersionID directly with no server-side reallocation, and
// publishWorkflowVersionTx explicitly rejects a reused id
// (ports.ErrImmutableVersionConflict) for different content. A single
// fixed placeholder string reused across two different publish calls
// would therefore collide on the second one; only VersionNumber (for the
// eight shared kinds only, never Workflow) is genuinely a discarded,
// server-reallocated placeholder.
func candidateVersionID() string {
	return idsource.Random{}.NewID()
}

// syntheticDraftFields builds the definition.Fields every non-Workflow
// kind's own pure Compile call needs for its CanPublish(Status) check.
// This CLI has no "get definition" query to load a real Definition's
// current Status from (ports.DefinitionsRepository, as of V2-10, only
// exposes LoadVersion/CreateDefinition/PublishVersion/
// PublishWorkflowVersion/ListVersions -- no read-one-Definition method),
// and more fundamentally there is no ArchiveDefinition/ActivateDefinition
// application command anywhere in this codebase yet -- every Definition
// CreateDefinition can ever produce is permanently DRAFT, generation 1,
// today. StatusDraft is therefore not a guessed placeholder here; it is
// the only Status any real Definition row can currently have. A later
// task that adds a lifecycle-transition command should revisit this.
func syntheticDraftFields(kind definition.Kind) definition.Fields {
	return definition.Fields{Kind: kind, Status: definition.StatusDraft}
}

// buildValidateDraftRequest and buildPublishRequest share the same
// per-kind dispatch: Workflow decodes into workflow.WorkflowDocument and
// is carried as WorkflowDefinition/WorkflowRequest; the other eight
// kinds decode via that kind's own DecodeStrict-based DecodeDocument
// (invoked through CompileFrom) and are carried as a Compile closure.
// This mirrors internal/app/definitions.ValidateDraftRequest/
// PublishDefinitionVersionRequest's own Kind-selected either/or shape
// exactly.

func buildValidateDraftRequest(kind definition.Kind, definitionID, name string, raw []byte, format authoring.Format, schemaVersion int, actor string) (definitions.ValidateDraftRequest, error) {
	publishedAt := time.Now().UTC()
	if kind == definition.KindWorkflow {
		document, err := decodeWorkflowDocument(raw, format)
		if err != nil {
			return definitions.ValidateDraftRequest{}, err
		}
		if strings.TrimSpace(name) == "" {
			// validate never writes to workflow_definitions (only publish's
			// real write path does, via ensureWorkflowDefinition), so an
			// empty placeholder Name is harmless here -- workflow.Compile
			// itself never checks WorkflowDefinition.Name -- but a
			// non-empty, deterministic fallback keeps validate's own
			// candidate output legible even when --name was omitted.
			name = definitionID
		}
		return definitions.ValidateDraftRequest{
			Kind:               kind,
			WorkflowDefinition: workflow.WorkflowDefinition{ID: workflow.WorkflowDefinitionID(definitionID), Name: name, Status: workflow.DefinitionDraft, Version: 1},
			WorkflowRequest: workflow.PublishRequest{
				VersionID: workflow.WorkflowVersionID(candidateVersionID()), VersionNumber: 1,
				Document: document, PublishedBy: actor, PublishedAt: publishedAt,
			},
		}, nil
	}
	compile, err := compileClosureForKind(kind, compileInputs{
		DefinitionID: definitionID, RawDocument: raw, Format: format,
		VersionNumber: 1, SchemaVersion: schemaVersion, PublishedBy: actor, PublishedAt: publishedAt,
	})
	if err != nil {
		return definitions.ValidateDraftRequest{}, err
	}
	return definitions.ValidateDraftRequest{Kind: kind, Compile: compile}, nil
}

func buildPublishRequest(kind definition.Kind, definitionID, name string, raw []byte, format authoring.Format, schemaVersion, versionNumber int, actor string) (definitions.PublishDefinitionVersionRequest, error) {
	publishedAt := time.Now().UTC()
	if kind == definition.KindWorkflow {
		if format != authoring.FormatJSON {
			return definitions.PublishDefinitionVersionRequest{}, errors.New("--format yaml is not supported for --kind WORKFLOW; author workflow documents as JSON")
		}
		document, err := decodeWorkflowDocument(raw, format)
		if err != nil {
			return definitions.PublishDefinitionVersionRequest{}, err
		}
		// name must equal the persisted workflow_definitions row's own
		// Name (the one 'definition create' wrote) -- see this file's own
		// top doc comment and runDefinitionPublish's own --name flag
		// description for why: ports.DefinitionsRepository has no "read
		// one Definition" query this CLI could use to load the real
		// stored Name back, and internal/adapters/sqlite's own
		// ensureWorkflowDefinition rejects a publish whose supplied
		// WorkflowDefinition.Name disagrees with what is already stored
		// (a ports.ErrPersistenceAlreadyExists-wrapped "differs from its
		// persisted identity" error) rather than silently overwriting it.
		return definitions.PublishDefinitionVersionRequest{
			DefinitionID:       definitionID,
			Kind:               kind,
			WorkflowDefinition: workflow.WorkflowDefinition{ID: workflow.WorkflowDefinitionID(definitionID), Name: name, Status: workflow.DefinitionDraft, Version: 1},
			WorkflowRequest: workflow.PublishRequest{
				VersionID: workflow.WorkflowVersionID(candidateVersionID()), VersionNumber: uint64(versionNumber),
				Document: document, PublishedBy: actor, PublishedAt: publishedAt,
			},
		}, nil
	}
	compile, err := compileClosureForKind(kind, compileInputs{
		DefinitionID: definitionID, RawDocument: raw, Format: format,
		VersionNumber: versionNumber, SchemaVersion: schemaVersion, PublishedBy: actor, PublishedAt: publishedAt,
	})
	if err != nil {
		return definitions.PublishDefinitionVersionRequest{}, err
	}
	return definitions.PublishDefinitionVersionRequest{DefinitionID: definitionID, Kind: kind, Compile: compile}, nil
}

// decodeWorkflowDocument strictly decodes raw JSON into a
// workflow.WorkflowDocument. Workflow has no YAML decode path anywhere
// in this codebase (internal/domain/workflow only ever exposes
// CompileJSON, never a DecodeStrict-based YAML route the way the other
// eight kinds' own DecodeDocument functions do), so this CLI only
// accepts --format json for --kind WORKFLOW -- see the usage error
// buildPublishRequest/buildValidateDraftRequest's own callers return for
// yaml.
func decodeWorkflowDocument(raw []byte, format authoring.Format) (workflow.WorkflowDocument, error) {
	if format != authoring.FormatJSON {
		return workflow.WorkflowDocument{}, errors.New("--format yaml is not supported for --kind WORKFLOW; author workflow documents as JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document workflow.WorkflowDocument
	if err := decoder.Decode(&document); err != nil {
		return workflow.WorkflowDocument{}, fmt.Errorf("decode workflow document: %w", err)
	}
	return document, nil
}

// compileInputs is the per-kind-agnostic input compileClosureForKind
// needs to build one of the eight shared kinds' own Compile closure.
type compileInputs struct {
	DefinitionID  string
	RawDocument   []byte
	Format        authoring.Format
	VersionNumber int
	SchemaVersion int
	PublishedBy   string
	PublishedAt   time.Time
}

// compileClosureForKind builds the func() (definition.VersionFields,
// error) closure internal/app/definitions.ValidateDraftRequest.Compile/
// PublishDefinitionVersionRequest.Compile expects, for any of the eight
// shared (non-Workflow) DefinitionKinds. Every kind follows the exact
// same shape (<Kind>Definition{ID, Fields}, PublishRequest{VersionID,
// VersionNumber, SchemaVersion, Document, Dependencies, PublishedBy,
// PublishedAt}, CompileFrom(def, rawDocument, format, req)) -- this
// function is deliberately just nine near-identical cases, not a clever
// generic dispatch, since each kind's own concrete types
// (BlockDefinitionID vs CommandDefinitionID, ...) are exactly what keep
// a pin from one kind's document ever being silently accepted as another
// kind's. Dependencies is deliberately left as an empty
// definition.DependencyManifest{} for all eight: dependency *resolution*
// (verifying a document's own inline pins -- e.g. Block's ExecutorRef,
// AgentProfile's ContextPolicyRef -- actually exist and hash to
// something) is explicitly out of V2-11's own scope (only Workflow's
// node-level pins are resolved, by workflowcompiler.CompileAndResolve,
// V2-09's own job); the eight shared kinds' Compile functions are pure
// (no I/O) and never resolve or require their own Dependencies field to
// be populated to compile successfully.
func compileClosureForKind(kind definition.Kind, in compileInputs) (func() (definition.VersionFields, error), error) {
	fields := syntheticDraftFields(kind)
	switch kind {
	case definition.KindBlock:
		def := block.BlockDefinition{ID: block.BlockDefinitionID(in.DefinitionID), Fields: fields}
		req := block.PublishRequest{
			VersionID: block.BlockVersionID(candidateVersionID()), VersionNumber: uint64(in.VersionNumber),
			SchemaVersion: in.SchemaVersion, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return block.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindSkill:
		def := skill.SkillDefinition{ID: skill.SkillDefinitionID(in.DefinitionID), Fields: fields}
		req := skill.PublishRequest{
			VersionID: skill.SkillVersionID(candidateVersionID()), VersionNumber: uint64(in.VersionNumber),
			SchemaVersion: in.SchemaVersion, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return skill.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindLayer:
		def := layer.LayerDefinition{ID: layer.LayerDefinitionID(in.DefinitionID), Fields: fields}
		req := layer.PublishRequest{
			VersionID: layer.LayerVersionID(candidateVersionID()), VersionNumber: uint64(in.VersionNumber),
			SchemaVersion: in.SchemaVersion, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return layer.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindEngineeringPack:
		def := engineeringpack.EngineeringPackDefinition{ID: engineeringpack.EngineeringPackDefinitionID(in.DefinitionID), Fields: fields}
		req := engineeringpack.PublishRequest{
			VersionID: engineeringpack.EngineeringPackVersionID(candidateVersionID()), VersionNumber: uint64(in.VersionNumber),
			SchemaVersion: in.SchemaVersion, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return engineeringpack.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindAgentProfile:
		def := agentprofile.AgentProfileDefinition{ID: agentprofile.AgentProfileDefinitionID(in.DefinitionID), Fields: fields}
		req := agentprofile.PublishRequest{
			VersionID: agentprofile.AgentProfileVersionID(candidateVersionID()), VersionNumber: uint64(in.VersionNumber),
			SchemaVersion: in.SchemaVersion, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return agentprofile.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindCommand:
		def := command.CommandDefinition{ID: command.CommandDefinitionID(in.DefinitionID), Fields: fields}
		req := command.PublishRequest{
			VersionID: command.CommandVersionID(candidateVersionID()), VersionNumber: uint64(in.VersionNumber),
			SchemaVersion: in.SchemaVersion, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return command.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindGate:
		def := gate.GateDefinition{ID: gate.GateDefinitionID(in.DefinitionID), Fields: fields}
		req := gate.PublishRequest{
			VersionID: gate.GateVersionID(candidateVersionID()), VersionNumber: uint64(in.VersionNumber),
			SchemaVersion: in.SchemaVersion, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) { return gate.CompileFrom(def, in.RawDocument, in.Format, req) }, nil
	case definition.KindPolicy:
		def := policy.PolicyDefinition{ID: policy.PolicyDefinitionID(in.DefinitionID), Fields: fields}
		req := policy.PublishRequest{
			VersionID: policy.PolicyVersionID(candidateVersionID()), VersionNumber: uint64(in.VersionNumber),
			SchemaVersion: in.SchemaVersion, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return policy.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	default:
		return nil, fmt.Errorf("definition: kind %q has no non-workflow compile path", kind)
	}
}

// versionFieldsView is this CLI's own stable, exported-field JSON view
// of a definition.VersionFields -- VersionFields' own fields are all
// unexported (V2-01's immutability discipline; see
// internal/app/definitions/commands.go's own versionFieldsDTO, whose
// shape this mirrors), so encoding/json reflecting over it directly
// would silently print "{}".
type versionFieldsView struct {
	ID               string                        `json:"id"`
	DefinitionID     string                        `json:"definitionId"`
	Kind             definition.Kind               `json:"kind"`
	VersionNumber    uint64                        `json:"versionNumber"`
	SchemaVersion    int                           `json:"schemaVersion"`
	CanonicalSource  string                        `json:"canonicalSource"`
	SourceHash       string                        `json:"sourceHash"`
	CompiledSnapshot string                        `json:"compiledSnapshot"`
	CompiledHash     string                        `json:"compiledHash"`
	Dependencies     definition.DependencyManifest `json:"dependencies"`
	PublishedBy      string                        `json:"publishedBy"`
	PublishedAt      time.Time                     `json:"publishedAt"`
}

func newVersionFieldsView(v definition.VersionFields) versionFieldsView {
	return versionFieldsView{
		ID: v.ID(), DefinitionID: v.DefinitionID(), Kind: v.Kind(),
		VersionNumber: v.VersionNumber(), SchemaVersion: v.SchemaVersion(),
		CanonicalSource: v.CanonicalSource(), SourceHash: v.SourceHash(),
		CompiledSnapshot: v.CompiledSnapshot(), CompiledHash: v.CompiledHash(),
		Dependencies: v.Dependencies(), PublishedBy: v.PublishedBy(), PublishedAt: v.PublishedAt(),
	}
}

// versionSummaryView is 'diff's own compact per-side identity/hash
// summary -- the full CanonicalSource/CompiledSnapshot bodies are
// deliberately left out here (SourceDiff already carries the meaningful
// content comparison; repeating both full bodies again in A/B would
// just double the output size for no new information).
type versionSummaryView struct {
	ID            string          `json:"id"`
	DefinitionID  string          `json:"definitionId"`
	Kind          definition.Kind `json:"kind"`
	VersionNumber uint64          `json:"versionNumber"`
	SourceHash    string          `json:"sourceHash"`
	CompiledHash  string          `json:"compiledHash"`
	PublishedBy   string          `json:"publishedBy"`
	PublishedAt   time.Time       `json:"publishedAt"`
}

func newVersionSummaryView(v definition.VersionFields) versionSummaryView {
	return versionSummaryView{
		ID: v.ID(), DefinitionID: v.DefinitionID(), Kind: v.Kind(), VersionNumber: v.VersionNumber(),
		SourceHash: v.SourceHash(), CompiledHash: v.CompiledHash(),
		PublishedBy: v.PublishedBy(), PublishedAt: v.PublishedAt(),
	}
}

// diffLineView is one line of a unified line diff: Op is "equal", "add"
// (present in B, not A) or "remove" (present in A, not B).
type diffLineView struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

type versionDiffView struct {
	A versionSummaryView `json:"a"`
	B versionSummaryView `json:"b"`
	// Identical is true when a and b compiled to the exact same content
	// (CompiledHash equal) -- AK-ARCH-005B's own dedup key, so this is
	// the same notion of "no real difference" PublishDefinitionVersion
	// itself already uses to decide whether republishing is a no-op.
	Identical  bool           `json:"identical"`
	SourceDiff []diffLineView `json:"sourceDiff"`
}

func newVersionDiffView(a, b definition.VersionFields) versionDiffView {
	return versionDiffView{
		A: newVersionSummaryView(a), B: newVersionSummaryView(b),
		Identical:  a.CompiledHash() == b.CompiledHash(),
		SourceDiff: diffLines(prettyJSONLines(a.CanonicalSource()), prettyJSONLines(b.CanonicalSource())),
	}
}

// prettyJSONLines re-indents compact canonical JSON (authoring.Canonicalize
// marshals with json.Marshal, never MarshalIndent, so CanonicalSource is
// always exactly one line) into multiple lines before diffing -- a line
// diff of two single-line strings would only ever report "line 1
// changed: <whole blob> -> <whole other blob>", which tells an operator
// nothing about what specifically differs. Re-indenting first (on a
// value this package already knows is valid canonical JSON) turns the
// diff into a genuinely useful per-field comparison while still
// comparing exactly the canonical, semantically-stable content.
func prettyJSONLines(canonicalJSON string) []string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(canonicalJSON), "", "  "); err != nil {
		return strings.Split(canonicalJSON, "\n")
	}
	return strings.Split(buf.String(), "\n")
}

// diffLines is a standard LCS-based line diff (see e.g. "An O(ND)
// Difference Algorithm" for the well-known faster alternative; this
// straightforward O(len(a)*len(b)) dynamic-programming version is used
// instead since authored definition documents are small and this
// codebase has no diff library dependency to reach for -- see this
// file's own package doc comment on 'diff's design). It never panics on
// any input, including empty a/b.
func diffLines(a, b []string) []diffLineView {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	result := make([]diffLineView, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			result = append(result, diffLineView{Op: "equal", Text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			result = append(result, diffLineView{Op: "remove", Text: a[i]})
			i++
		default:
			result = append(result, diffLineView{Op: "add", Text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		result = append(result, diffLineView{Op: "remove", Text: a[i]})
	}
	for ; j < m; j++ {
		result = append(result, diffLineView{Op: "add", Text: b[j]})
	}
	return result
}
