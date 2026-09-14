// This file is V6-04's own architecture proof for its own "Không làm" line
// (docs/design/08-v6-api-projections.md V6-04: "không generic
// status/family/workspace setter; không dùng projected detail để
// authorize"), mirroring TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt's
// own "parse real source, walk the AST, fail on a forbidden shape" idiom
// (command_envelope_test.go) rather than merely asserting it in a doc
// comment.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// forbiddenRequestStatusFields is every JSON field name a generic
// status/state setter would plausibly use — this task's own request bodies
// (internal/delivery/httpapi/workitem's own createRootWorkItemBody/
// createChildWorkItemBody/requestScopeExpansionBody/rejectScopeExpansionBody/
// emptyBody/scopeGrantBody) must never declare one of these, because none of
// this package's six mutating routes is a generic setter — each one always
// names a single, narrow, already-existing application command
// (CreateRootWorkItem, CreateChildWorkItem, RequestScopeExpansion,
// ApproveScopeExpansion, RejectScopeExpansion, WithdrawScopeExpansion), none
// of which takes a caller-supplied target status.
var forbiddenRequestStatusFields = map[string]bool{
	"status": true, "targetstatus": true, "state": true, "targetstate": true,
	"family": true, "familystatus": true, "workspace": true, "workspacestate": true,
}

// TestWorkItemPackageRequestBodiesNeverAcceptAStatusField walks every
// non-test .go file in internal/delivery/httpapi/workitem, finds every
// exported-or-not struct type whose own name ends in "Body" (this task's own
// consistent naming convention for a request wire DTO — see dto.go/
// workitem_commands.go/scope_expansion_commands.go), and fails if any such
// struct declares a field whose own `json:"..."` tag name is one of
// forbiddenRequestStatusFields. A read-only RESPONSE DTO
// (internal/app/work/queries.go's own WorkItemDetail.Status,
// TaskFamilyDetail.Status, ScopeExpansionRequestDetail.Status,
// WorkItemReadiness.Status — all legitimately report CURRENT state) lives in
// a different package entirely and is untouched by this scan, which is
// deliberately scoped to the HTTP request-body types a caller can actually
// populate.
func TestWorkItemPackageRequestBodiesNeverAcceptAStatusField(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "workitem")

	fset := token.NewFileSet()
	checked := 0
	foundBodyType := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		checked++
		ast.Inspect(file, func(n ast.Node) bool {
			typeSpec, ok := n.(*ast.TypeSpec)
			if !ok || !strings.HasSuffix(typeSpec.Name.Name, "Body") {
				return true
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok || structType.Fields == nil {
				return true
			}
			foundBodyType = true
			for _, field := range structType.Fields.List {
				if field.Tag == nil {
					continue
				}
				tagValue, unquoteErr := strconv.Unquote(field.Tag.Value)
				if unquoteErr != nil {
					continue
				}
				jsonName := jsonTagName(tagValue)
				if jsonName == "" {
					continue
				}
				if forbiddenRequestStatusFields[strings.ToLower(jsonName)] {
					t.Errorf("%s:%d: request body type %q declares a %q JSON field — no route in this package may accept a caller-supplied status/family/workspace target; every mutation must instead name one specific, already-existing application command (V6-04's own \"Không làm: không generic status/family/workspace setter\")",
						path, fset.Position(field.Pos()).Line, typeSpec.Name.Name, jsonName)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("no internal/delivery/httpapi/workitem files were checked — this test needs updating alongside the implementation")
	}
	if !foundBodyType {
		t.Fatal("no \"...Body\" request DTO type was found — this test's own naming-convention assumption needs updating alongside the implementation")
	}
}

// jsonTagName extracts the field name half of a struct tag's own `json:"..."`
// value (ignoring `,omitempty` and similar options, and reporting "" for
// `json:"-"` or a tag with no json key at all) — a minimal, local parser
// rather than importing reflect.StructTag for one call site.
func jsonTagName(tag string) string {
	const key = `json:"`
	idx := strings.Index(tag, key)
	if idx == -1 {
		return ""
	}
	rest := tag[idx+len(key):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return ""
	}
	name := strings.Split(rest[:end], ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

// forbiddenProjectionImportSubstrings names every substring V6-04's own
// "không dùng projected detail để authorize" line forbids this package from
// ever importing — V6-10 (the future projected Kanban/detail task this line
// contrasts against) does not exist yet in this codebase, so this check is
// deliberately forward-looking: it fails the moment anyone (this task or a
// careless future edit) imports a package whose own path suggests a
// projection/read-model/kanban concern into an authorization decision this
// package makes, rather than relying on that never happening by omission
// alone.
var forbiddenProjectionImportSubstrings = []string{"projection", "kanban"}

// TestWorkItemPackageNeverImportsProjection walks every non-test .go file in
// internal/delivery/httpapi/workitem and fails if any import path contains
// one of forbiddenProjectionImportSubstrings — every authorization decision
// this package makes reloads the real row via internal/app/work/queries.go
// (GetWorkItem/GetTaskFamily/GetScopeExpansionRequest), never a projected
// read model.
func TestWorkItemPackageNeverImportsProjection(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "workitem")

	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		checked++
		for _, imp := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				continue
			}
			lower := strings.ToLower(importPath)
			for _, forbidden := range forbiddenProjectionImportSubstrings {
				if strings.Contains(lower, forbidden) {
					t.Errorf("%s: internal/delivery/httpapi/workitem imports %q — this package must never authorize from a projection/read-model (V6-04's own \"Không làm: không dùng projected detail để authorize\"); every authorization decision must reload the real row via internal/app/work/queries.go instead",
						path, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("no internal/delivery/httpapi/workitem files were checked — this test needs updating alongside the implementation")
	}
}
