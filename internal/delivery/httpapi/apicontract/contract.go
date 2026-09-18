// Package apicontract is V6-12's own machine-readable API contract
// generator and gate suite (docs/design/08-v6-api-projections.md V6-12:
// "compose/freeze route fragments and prove the runtime router equals the
// checked schema both ways"). It reads a fully-composed
// httpapi.RouteRegistry (built the one, real way —
// internal/delivery/httpcompose.ComposeRoutes, this task's own extraction
// of cmd/aw/serve.go's route-composition block) and produces:
//
//   - Contract (this file): a deterministic, JSON-serializable description
//     of every registered operation — operationId, method, path, scope and
//     a shallow reflected description of its request/response schema.
//   - DetectBreakingChanges (breaking.go): the compatibility/breaking-diff
//     gate comparing two Contract snapshots.
//   - ParseUXDoc/CheckUXGaps (uxdoc.go/uxgap.go): the four-way parity
//     inventory SEED's HTTP-side dependency/reference checker, cross-
//     referencing docs/design/11-v6-00-ux-artifact.md's own proposed-
//     operationId inventory against this same Contract.
//
// # Why a custom JSON contract, not full OpenAPI 3.x
//
// The design doc's own language ("OpenAPI/equivalent artifact") explicitly
// allows either. A faithful OpenAPI 3.x document needs a real Go-struct-to-
// JSON-Schema reflector: $ref/components/schemas, correct handling of
// time.Time/maps/slices/embedded fields/pointers, and a convention for the
// ~13 already-merged routes (see below) that only ever registered an
// opaque struct{}{} placeholder. No such reflector exists anywhere in this
// codebase today, and building one that is actually FAITHFUL (not just
// shaped like OpenAPI while quietly misdescribing half the real DTOs) is a
// much bigger task than V6-12's own scope ("no retrofitting metadata a
// leaf task should have already supplied") budgets for. A hand-rolled
// OpenAPI document that LOOKS authoritative but is subtly wrong in its
// schema section would be worse than an honest, narrower artifact — a
// future consumer would trust the $ref graph and be wrong. This package
// instead emits a smaller, honest, custom JSON shape: exact
// method/path/operationId/scope (the part every one of V6-12's own Verify
// bullets actually needs to be 100% correct), plus a best-effort shallow
// field list for each request/response schema value a leaf actually
// supplied (type name, JSON tag, Go type — one level deep, no attempt at
// full recursive JSON Schema), with an explicit `opaque: true` flag for
// the routes that only ever registered `struct{}{}` — never inventing
// detail that was never there. A later task can still walk this artifact
// to hand-generate a real OpenAPI document once every leaf's schema is
// real; V6-12 itself is honest about not being that document.
//
// # The known opaque-schema gap
//
// Three already-merged leaf packages registered every one of their routes
// with `RequestSchema: struct{}{}, ResponseSchema: struct{}{}` — fully
// opaque placeholders, never a real DTO:
// internal/delivery/httpapi/catalog (11 routes — every one of V6-03A's
// own project/repository/component endpoints), plus one route each in
// internal/delivery/httpapi/evidence and
// internal/delivery/httpapi/workspaceinspection. V6-12's own "Không làm"
// line ("no retrofitting metadata a leaf task should have already
// supplied") means this package does not — and must not — invent a fake
// schema for these 13 routes to make the artifact look more complete than
// it is. describeSchema below detects the literal, unnamed `struct{}{}`
// value (Kind==Struct, zero fields, empty Name — distinct from a
// deliberately-named empty body type like workitem.emptyBody{}, which
// legitimately means "this route has no request body" and is NOT flagged
// opaque) and marks it opaque instead of crashing or fabricating fields.
// See baocaov6checklist.md's own V6-12 section for the exact route list
// this produces.
package apicontract

import (
	"reflect"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// ContractVersion is this artifact format's own schema version — bumped
// only if Contract/Operation/SchemaRef/Field's own JSON shape changes in a
// way a consumer parsing the artifact itself would need to know about
// (never bumped for an ordinary route addition/removal, which
// DetectBreakingChanges already reports on its own terms).
const ContractVersion = "1"

// Contract is the whole machine-readable artifact: every operation this
// process serves, in a fixed, OperationID-sorted order (never registration
// order, which would reorder itself — and thrash the golden diff — the
// moment ComposeRoutes' own call sequence is reordered for unrelated
// reasons).
type Contract struct {
	Version    string      `json:"version"`
	Operations []Operation `json:"operations"`
}

// Operation is one registered HTTP route's contract-relevant shape — every
// field RouteDescriptor itself carries except Handler (an unexported
// runtime value with no business being in a serialized artifact).
type Operation struct {
	OperationID    string    `json:"operationId"`
	Method         string    `json:"method"`
	Path           string    `json:"path"`
	ScopeKind      string    `json:"scopeKind"`
	RequestSchema  SchemaRef `json:"requestSchema"`
	ResponseSchema SchemaRef `json:"responseSchema"`
}

// SchemaRef is a shallow, reflected description of one RequestSchema/
// ResponseSchema value — see this package's own doc comment for why this
// is deliberately not a full recursive JSON Schema.
type SchemaRef struct {
	// TypeName is the Go type's own String() form, e.g.
	// "workitem.createRootWorkItemBody" or "struct {}".
	TypeName string `json:"typeName"`
	// Opaque is true only for the literal, unnamed `struct{}{}` placeholder
	// (see the package doc comment's "known opaque-schema gap" section) —
	// never set for a legitimately-named empty-body type.
	Opaque bool `json:"opaque,omitempty"`
	// Fields is a one-level-deep exported-field listing for a struct (or
	// pointer-to-struct) schema value; omitted entirely for an opaque
	// placeholder or a non-struct schema value (none exist today, but a
	// future leaf choosing e.g. a bare slice/map type should not crash
	// this generator).
	Fields []Field `json:"fields,omitempty"`
}

// Field is one exported struct field of a SchemaRef, in declaration order.
type Field struct {
	Name    string `json:"name"`
	JSONTag string `json:"jsonTag,omitempty"`
	Type    string `json:"type"`
}

// Build walks descriptors (a fully-composed httpapi.RouteRegistry's own
// Descriptors() — production callers pass the SAME registry
// internal/delivery/httpcompose.ComposeRoutes just populated, this
// package's own tests pass the identical thing built against real,
// temporary infrastructure) and produces a deterministic Contract.
//
// Build performs no validation of its own (uniqueness of Method+Path and
// of OperationID is ALREADY enforced by httpapi.RouteRegistry.Register at
// composition time — Build only ever sees an already-valid registry, and
// a duplicate would have panicked long before Build ever ran).
func Build(descriptors []httpapi.RouteDescriptor) Contract {
	ops := make([]Operation, 0, len(descriptors))
	for _, d := range descriptors {
		ops = append(ops, Operation{
			OperationID:    d.OperationID,
			Method:         d.Method,
			Path:           d.Path,
			ScopeKind:      string(d.ScopeKind),
			RequestSchema:  describeSchema(d.RequestSchema),
			ResponseSchema: describeSchema(d.ResponseSchema),
		})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].OperationID < ops[j].OperationID })
	return Contract{Version: ContractVersion, Operations: ops}
}

// describeSchema reflects on v (a RouteDescriptor.RequestSchema/
// ResponseSchema value, always a non-nil struct or pointer-to-struct
// value in this codebase today) and produces a shallow SchemaRef. It never
// panics on an unexpected shape — a bare non-struct schema value (none
// exist today) degrades to a TypeName-only SchemaRef with no Fields,
// rather than this generator crashing on a future leaf's unanticipated
// choice.
func describeSchema(v any) SchemaRef {
	if v == nil {
		return SchemaRef{TypeName: "<nil>", Opaque: true}
	}
	t := reflect.TypeOf(v)
	ref := SchemaRef{TypeName: t.String()}

	structType := t
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}
	if structType.Kind() != reflect.Struct {
		return ref
	}
	// The literal, unnamed `struct{}{}` placeholder: zero fields AND an
	// empty type Name() — an anonymous type. A deliberately-named empty
	// body type (e.g. workitem.emptyBody{}) also has zero fields but a
	// non-empty Name(), and is NOT opaque: it is a real, intentional "no
	// request body" declaration, not a missing schema.
	if structType.NumField() == 0 && structType.Name() == "" {
		ref.Opaque = true
		return ref
	}
	for i := 0; i < structType.NumField(); i++ {
		f := structType.Field(i)
		if f.PkgPath != "" {
			continue // unexported field — never part of the wire schema
		}
		ref.Fields = append(ref.Fields, Field{
			Name:    f.Name,
			JSONTag: f.Tag.Get("json"),
			Type:    f.Type.String(),
		})
	}
	return ref
}
