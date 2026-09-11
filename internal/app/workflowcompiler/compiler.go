// Package workflowcompiler is V2-09's "Graph/dependency compiler"
// (docs/design/04-v2-definition-plane.md V2-09, ADR-012, ADR-013,
// AK-ARCH-003, AK-ARCH-005B, HE-14-M06): the application-layer
// orchestration that resolves every node-level dependency.DependencyPin
// and AdapterBuildVersion reference a WorkflowDocument names, against
// the real, published registry state, and only then hands the resolved
// dependency manifest to internal/domain/workflow.Compile — the pure
// domain function that predates this package and cannot itself perform
// the real-database resolution this task requires (internal/domain
// packages never do I/O, per this repo's own domain/adapter boundary).
//
// This package's own scope stops at producing a compiled, resolved
// WorkflowVersion in memory. It never persists anything, never checks
// idempotency, never emits a domain event or a command receipt — that is
// explicitly V2-10's "Validate/publish application commands" job
// ("CLI/API sau này dùng một contract, không gọi compiler/repository
// trực tiếp"): V2-10 is the clean application-command wrapper this
// compiler's own output feeds into, not something this package builds
// itself.
package workflowcompiler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// integrationMultiRepositoryWriteCapability is ADR-013's own named
// capability string ("Nhiều repository chỉ hợp lệ khi BlockVersion khai
// capability INTEGRATION_MULTI_REPOSITORY_WRITE, policy grant, compiler
// xác nhận scope...") — the one this package's scope/capability check
// looks for among resolved PERMISSION-category policies'
// GrantedCapabilities.
const integrationMultiRepositoryWriteCapability = "INTEGRATION_MULTI_REPOSITORY_WRITE"

// adapterBuildVersionManifestKind is the workflow.DependencyPin.Kind
// value this package uses for a resolved AdapterBuildVersion pin.
// AdapterBuildVersion is deliberately not a definition.Kind (ADR-022),
// so this is a plain string, not one of definition.Kind's closed
// values — workflow.DependencyPin.Kind is itself already a plain,
// open string (see workflow.go's own DependencyPin, distinct from
// definition.DependencyPin), which is exactly why it — not
// definition.DependencyPin — is the shape capable of carrying this.
const adapterBuildVersionManifestKind = "ADAPTER_BUILD_VERSION"

// ResolutionError collects every dependency-resolution and
// scope/capability problem found compiling one WorkflowDocument — the
// application-layer counterpart of workflow.ValidationError, for
// problems only resolvable against real registry state, never
// discoverable from the document's own structure alone.
type ResolutionError struct {
	Problems []string
}

func (e *ResolutionError) Error() string {
	return "workflow dependency resolution failed: " + strings.Join(e.Problems, "; ")
}

// CompileAndResolve validates request.Document structurally (failing
// fast before any database round trip), resolves every node-level
// definition.DependencyPin and AdapterBuildVersion reference against the
// real registry — all within one read-only snapshot, so "exact registry
// snapshot" (ADR-012) means what it says: every pin resolved here sees
// the same point-in-time registry state — checks ADR-013's
// exactly-one-repository-WRITE-by-default rule, and only then calls the
// pure workflow.Compile with the fully resolved dependency manifest.
func CompileAndResolve(ctx context.Context, uow ports.UnitOfWork, def workflow.WorkflowDefinition, request workflow.PublishRequest) (workflow.WorkflowVersion, error) {
	if err := workflow.ValidateDocument(request.Document); err != nil {
		return workflow.WorkflowVersion{}, err
	}
	if err := checkAgentRolesExplicit(request.Document); err != nil {
		return workflow.WorkflowVersion{}, err
	}

	refs := collectReferences(request.Document)

	var resolved resolvedReferences
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var resolveErr error
		resolved, resolveErr = resolveReferences(ctx, tx, refs)
		return resolveErr
	})
	if err != nil {
		return workflow.WorkflowVersion{}, err
	}

	if err := checkScopeAndCapability(resolved); err != nil {
		return workflow.WorkflowVersion{}, err
	}
	if err := checkCompletionPolicyRefCategory(request.Document.CompletionPolicyRef, resolved); err != nil {
		return workflow.WorkflowVersion{}, err
	}

	manifest, err := buildDependencyManifest(resolved)
	if err != nil {
		return workflow.WorkflowVersion{}, err
	}

	request.Dependencies = manifest
	return workflow.Compile(def, request)
}

// AgentRoleValidationError collects every AGENT node missing an explicit,
// valid workflow.AgentRole at publish time — checkAgentRolesExplicit's
// own error shape, distinct from ResolutionError (whose own doc comment
// scopes it to problems only resolvable against real registry state):
// every problem here is discoverable from request.Document's own
// structure alone, with no database round trip needed.
type AgentRoleValidationError struct {
	Problems []string
}

func (e *AgentRoleValidationError) Error() string {
	return "workflow agent role validation failed: " + strings.Join(e.Problems, "; ")
}

// checkAgentRolesExplicit enforces V5-12's own publish-time bar: every
// AGENT node in a document being published through THIS function must
// declare an explicit, valid Role (MAKER or CHECKER) — never empty.
// workflow.ValidateDocument (called just above, and the same function
// internal/adapters/sqlite's own loadWorkflowVersion calls indirectly via
// workflow.Compile to re-verify an already-persisted row) deliberately
// stays permissive about an empty Role, precisely so that reload path
// keeps working for every WorkflowVersion published before this field
// existed. CompileAndResolve is the one and only real publish
// entrypoint (internal/app/definitions' own command handlers call this,
// never workflow.Compile directly) — enforcing the stricter "explicit
// Role required" bar here, and nowhere lower, is what lets an old row
// still rebuild while every new publish going forward must comply. Never
// defaults a missing Role to MAKER itself (unlike
// workflow.AgentNodeConfig.EffectiveRole(), which a reader uses well
// after publish) — a fresh publish either declares the Role it means, or
// is rejected.
func checkAgentRolesExplicit(document workflow.WorkflowDocument) error {
	var problems []string
	for _, node := range document.Nodes {
		if node.Agent == nil {
			continue
		}
		switch node.Agent.Role {
		case workflow.AgentRoleMaker, workflow.AgentRoleChecker:
		default:
			problems = append(problems, fmt.Sprintf(
				"node %q must declare an explicit agent.role (%q or %q)", node.Key, workflow.AgentRoleMaker, workflow.AgentRoleChecker,
			))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return &AgentRoleValidationError{Problems: problems}
	}
	return nil
}

// nodeReferences is every dependency.DependencyPin and AdapterBuildID a
// single node declares, tagged with which role each pin plays — needed
// downstream to know, e.g., that a resolved COMMAND pin's own document
// carries a CwdRepositoryTarget worth inspecting for the scope check,
// while a POLICY pin's document is worth inspecting for
// GrantedCapabilities.
type nodeReferences struct {
	executorKind definition.Kind
	executorPin  *definition.DependencyPin
	policyPins   []definition.DependencyPin
	adapterBuild *string
}

func collectReferences(document workflow.WorkflowDocument) []nodeReferences {
	refs := make([]nodeReferences, 0, len(document.Nodes))
	for _, node := range document.Nodes {
		switch {
		case node.Agent != nil:
			pin := node.Agent.ProfileRef
			refs = append(refs, nodeReferences{
				executorKind: definition.KindAgentProfile, executorPin: &pin,
				policyPins: node.Agent.PolicyRefs, adapterBuild: node.Agent.AdapterBuildID,
			})
		case node.Command != nil:
			pin := node.Command.CommandRef
			refs = append(refs, nodeReferences{
				executorKind: definition.KindCommand, executorPin: &pin,
				policyPins: node.Command.PolicyRefs,
			})
		case node.MachineGate != nil:
			pin := node.MachineGate.GateRef
			refs = append(refs, nodeReferences{
				executorKind: definition.KindGate, executorPin: &pin,
				policyPins: node.MachineGate.PolicyRefs,
			})
		}
	}
	// document.CompletionPolicyRef (V5-11, 2026-09-10) is a root-level
	// pin, not a per-node one — folded in as its own policyPins-only
	// entry purely to reuse resolveReferences' own existing
	// resolve-once-per-DefinitionID/conflict-detection logic unchanged.
	// checkCompletionPolicyRefCategory (below) is the ADDITIONAL,
	// completion-specific verification this generic pipeline does not
	// itself do (it only inspects PERMISSION-category documents).
	if document.CompletionPolicyRef != nil {
		refs = append(refs, nodeReferences{policyPins: []definition.DependencyPin{*document.CompletionPolicyRef}})
	}
	return refs
}

// checkCompletionPolicyRefCategory verifies that ref (if any) resolved to
// a real, published COMPLETION-category policy with CompletionRules
// populated — the one check resolveReferences' own generic KindPolicy
// handling does not perform (it only inspects PERMISSION-category
// documents, for GrantedCapabilities). ref is assumed already resolved
// into resolved.byDefinitionID by collectReferences/resolveReferences
// above; a nil ref is a no-op (a workflow with no CompletionPolicy
// pinned yet — legitimate, see WorkflowDocument.CompletionPolicyRef's own
// doc comment).
func checkCompletionPolicyRefCategory(ref *definition.DependencyPin, resolved resolvedReferences) error {
	if ref == nil {
		return nil
	}
	entry, ok := resolved.byDefinitionID[ref.DefinitionID]
	if !ok {
		// Unreachable in practice: resolveReferences would already have
		// returned a ResolutionError for an unresolvable pin before this
		// function is ever called. Fails closed rather than panicking on
		// a map miss if that invariant is ever somehow violated.
		return &ResolutionError{Problems: []string{fmt.Sprintf("completionPolicyRef %q/%q was never resolved", ref.DefinitionID, ref.VersionID)}}
	}
	var doc policy.PolicyDocument
	if err := json.Unmarshal([]byte(entry.Fields.CanonicalSource()), &doc); err != nil {
		return &ResolutionError{Problems: []string{fmt.Sprintf("decode completionPolicyRef %q document: %v", ref.DefinitionID, err)}}
	}
	if doc.Category != policy.CategoryCompletion || doc.Completion == nil {
		return &ResolutionError{Problems: []string{fmt.Sprintf(
			"completionPolicyRef %q/%q is not a COMPLETION-category policy with completion rules", ref.DefinitionID, ref.VersionID,
		)}}
	}
	return nil
}

// resolvedReferences is every distinct pin collectReferences found,
// resolved against real registry state within one snapshot.
type resolvedReferences struct {
	// byDefinitionID is every resolved definition.DependencyPin, keyed by
	// DefinitionID — the same key workflow's own DependencyManifest
	// dedupes on (see buildDependencyManifest), so a definitionID pinned
	// by more than one node resolves and appears exactly once.
	byDefinitionID map[string]resolvedDefinitionPin
	// adapterBuilds is every distinct resolved AdapterBuildVersion, keyed
	// by its own content-addressed Build.ID().
	adapterBuilds map[string]adapterbuild.Build
	// commandCwdTargets is every resolved COMMAND pin's own declared
	// CwdRepositoryTarget — the scope/capability check's own "which
	// repositories does this graph touch" signal (see
	// checkScopeAndCapability's doc comment for why this is the only
	// concrete signal reachable from a compiled graph today).
	commandCwdTargets map[string]bool
	// grantedCapabilities is every resolved PERMISSION-category Policy's
	// own GrantedCapabilities, deduplicated.
	grantedCapabilities map[string]bool
}

type resolvedDefinitionPin struct {
	Pin    definition.DependencyPin
	Fields definition.VersionFields
}

func resolveReferences(ctx context.Context, tx ports.Tx, refs []nodeReferences) (resolvedReferences, error) {
	result := resolvedReferences{
		byDefinitionID:      map[string]resolvedDefinitionPin{},
		adapterBuilds:       map[string]adapterbuild.Build{},
		commandCwdTargets:   map[string]bool{},
		grantedCapabilities: map[string]bool{},
	}
	var problems []string

	resolvePin := func(pin definition.DependencyPin) {
		if existing, seen := result.byDefinitionID[pin.DefinitionID]; seen {
			if existing.Pin.VersionID != pin.VersionID {
				problems = append(problems, fmt.Sprintf(
					"definition %q is pinned at two different versions (%q and %q) across this graph",
					pin.DefinitionID, existing.Pin.VersionID, pin.VersionID,
				))
			}
			return
		}
		fields, err := tx.Definitions().LoadVersion(ctx, pin.VersionID)
		if errors.Is(err, ports.ErrDefinitionVersionNotFound) {
			problems = append(problems, fmt.Sprintf("dependency pin %q/%q/%q does not resolve to any published version", pin.Kind, pin.DefinitionID, pin.VersionID))
			return
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("resolve dependency pin %q/%q/%q: %v", pin.Kind, pin.DefinitionID, pin.VersionID, err))
			return
		}
		if fields.Kind() != pin.Kind || fields.DefinitionID() != pin.DefinitionID {
			problems = append(problems, fmt.Sprintf(
				"dependency pin claims %s/%s but version %q actually belongs to %s/%s",
				pin.Kind, pin.DefinitionID, pin.VersionID, fields.Kind(), fields.DefinitionID(),
			))
			return
		}
		result.byDefinitionID[pin.DefinitionID] = resolvedDefinitionPin{Pin: pin, Fields: fields}

		switch pin.Kind {
		case definition.KindCommand:
			var doc command.CommandDocument
			if err := json.Unmarshal([]byte(fields.CanonicalSource()), &doc); err == nil && strings.TrimSpace(doc.CwdRepositoryTarget) != "" {
				result.commandCwdTargets[doc.CwdRepositoryTarget] = true
			}
		case definition.KindPolicy:
			var doc policy.PolicyDocument
			if err := json.Unmarshal([]byte(fields.CanonicalSource()), &doc); err == nil && doc.Category == policy.CategoryPermission && doc.Permission != nil {
				for _, capability := range doc.Permission.GrantedCapabilities {
					result.grantedCapabilities[capability] = true
				}
			}
		}
	}

	for _, ref := range refs {
		if ref.executorPin != nil {
			resolvePin(*ref.executorPin)
		}
		for _, policyPin := range ref.policyPins {
			resolvePin(policyPin)
		}
		if ref.adapterBuild != nil {
			id := *ref.adapterBuild
			if _, seen := result.adapterBuilds[id]; !seen {
				build, err := tx.AdapterBuilds().Get(ctx, id)
				if errors.Is(err, ports.ErrAdapterBuildNotFound) {
					problems = append(problems, fmt.Sprintf("adapter build %q does not resolve to any registered build", id))
				} else if err != nil {
					problems = append(problems, fmt.Sprintf("resolve adapter build %q: %v", id, err))
				} else {
					result.adapterBuilds[id] = build
				}
			}
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return resolvedReferences{}, &ResolutionError{Problems: problems}
	}
	return result, nil
}

// checkScopeAndCapability enforces ADR-013's default: exactly one
// repository may be written by default; more than one is only valid
// when some resolved PERMISSION-category policy in the graph grants
// INTEGRATION_MULTI_REPOSITORY_WRITE ("compiler xác nhận scope" is
// literally this check's own citation).
//
// Scoping note: the only concrete "which repository does this touch"
// signal reachable from a compiled Alpha graph today is a resolved
// COMMAND pin's own CwdRepositoryTarget — AGENT/MACHINE_GATE pins
// (AgentProfile/Gate) carry no repository-target field of their own,
// and ScopeSelector (internal/domain/block's own declared-access-level
// concept) is unreachable from any workflow node, since nodes pin
// AgentProfile/Command/Gate directly, never Block (V2-08's own design
// guidance). This check is therefore necessarily narrower than the full
// scope model ADR-013 describes in the abstract — it enforces exactly
// the rule against exactly the data this graph schema actually exposes,
// and does not invent a broader mechanism the schema cannot back. A
// later task giving Command/Gate/AgentProfile their own ScopeSelector
// (mirroring Block's) would let this check see every executable node's
// target, not just COMMAND's.
func checkScopeAndCapability(resolved resolvedReferences) error {
	if len(resolved.commandCwdTargets) <= 1 {
		return nil
	}
	if resolved.grantedCapabilities[integrationMultiRepositoryWriteCapability] {
		return nil
	}
	targets := make([]string, 0, len(resolved.commandCwdTargets))
	for target := range resolved.commandCwdTargets {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return &ResolutionError{Problems: []string{fmt.Sprintf(
		"workflow targets %d repositories (%s) but no resolved PERMISSION policy grants %s",
		len(targets), strings.Join(targets, ", "), integrationMultiRepositoryWriteCapability,
	)}}
}

// buildDependencyManifest translates resolved's resolved pins into
// workflow's own DependencyManifest shape — Key=DefinitionID,
// Version=VersionID, Hash=the resolved Version's own CompiledHash. This
// is where ADR-012's "Registry thay đổi làm dependency resolve khác
// phải tạo compiled snapshot/version mới" and V2-09's own "Hoàn thành
// khi" bar become real: the manifest's Hash values come from the
// registry's ACTUAL resolved content, not from the pin's own bare
// strings, so changing which VersionID a node pins changes which
// VersionFields resolves, which changes CompiledHash, which changes the
// final manifest, which changes the WorkflowVersion's own
// CompiledSnapshotHash (workflow.WorkflowVersion.ContentHash()).
func buildDependencyManifest(resolved resolvedReferences) (workflow.DependencyManifest, error) {
	pins := make([]workflow.DependencyPin, 0, len(resolved.byDefinitionID)+len(resolved.adapterBuilds))
	for _, entry := range resolved.byDefinitionID {
		pins = append(pins, workflow.DependencyPin{
			Kind:    string(entry.Pin.Kind),
			Key:     entry.Pin.DefinitionID,
			Version: entry.Pin.VersionID,
			Hash:    entry.Fields.CompiledHash(),
		})
	}
	for id := range resolved.adapterBuilds {
		pins = append(pins, workflow.DependencyPin{
			Kind:    adapterBuildVersionManifestKind,
			Key:     id,
			Version: id,
			Hash:    id,
		})
	}
	sort.Slice(pins, func(i, j int) bool {
		if pins[i].Kind != pins[j].Kind {
			return pins[i].Kind < pins[j].Kind
		}
		return pins[i].Key < pins[j].Key
	})
	return workflow.DependencyManifest{Pins: pins}, nil
}
