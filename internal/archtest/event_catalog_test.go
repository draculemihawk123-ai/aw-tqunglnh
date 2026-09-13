// This file is V6-00A's own Part 5 (docs/design/08-v6-api-projections.md
// V6-00A Verify: "emitted-key inventory bằng registered-key inventory;
// unknown version fail-closed"): an automated replacement for the manual,
// repo-wide audits that found and closed 24 real domain-event catalog gaps
// across PRs #23-#26. It proves, as a real go test, that every
// (EventType, SchemaVersion) pair this codebase actually persists into
// domain_events has a registered eventschema.Decoder — so a future PR that
// adds a new event without registering it fails CI immediately, instead of
// silently shipping an undecodable historical row.
package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/artifactsweep"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
)

// eventKeySite pairs an emitted (EventType, SchemaVersion) key with the
// file:line the scanner found it at, so a failure names a real call site
// instead of just an abstract key.
type eventKeySite struct {
	eventschema.EventKey
	site string
}

// TestEmittedDomainEventInventoryMatchesRegisteredInventory is V6-00A's
// own CI inventory guard. It builds the "registered" side by actually
// calling every real production RegisterEventSchemas into one combined
// eventschema.Registry (not by re-parsing those files — a real functional
// call can never drift from what registration actually does, however a
// given package implements it, e.g. internal/app/agentevents' own
// loop-over-a-slice shape), and the "emitted" side by parsing every real
// non-test .go source file for the three distinct shapes this codebase
// uses to append a domain_events row (see the three scanX helpers below).
// A key emitted but not registered fails the test: an event this codebase
// can produce today but cannot decode is exactly the "unknown version"
// this task's own Verify line says must fail closed, not ship silently.
func TestEmittedDomainEventInventoryMatchesRegisteredInventory(t *testing.T) {
	moduleRoot := findModuleRoot(t)

	registry := eventschema.NewRegistry()
	sqlite.RegisterEventSchemas(registry)
	adapterbuild.RegisterEventSchemas(registry)
	catalog.RegisterEventSchemas(registry)
	definitions.RegisterEventSchemas(registry)
	message.RegisterEventSchemas(registry)
	work.RegisterEventSchemas(registry)
	releasesetcommit.RegisterEventSchemas(registry)
	workspacerelease.RegisterEventSchemas(registry)
	workspacereconcile.RegisterEventSchemas(registry)
	runtime.RegisterEventSchemas(registry)
	artifactsweep.RegisterEventSchemas(registry)
	safesettings.RegisterEventSchemas(registry)
	// internal/app/agentevents.RegisterEventSchemas is deliberately NOT
	// included here: it registers ports.AgentEventKind decoders for the
	// separate agent_events journal (V5-08A's own sink/checkpoint
	// contract), never a (EventType, SchemaVersion) pair written to
	// domain_events. That kind set is already closed and self-consistent
	// (agentevents.RegisterEventSchemas' own doc comment: "registers every
	// ports.AgentEventKind's Decoder into registry") and was never in scope
	// for this task's own audit, which was about domain_events specifically
	// — mixing a second journal's keys into this comparison would compare
	// two unrelated inventories against each other.

	registered := map[eventschema.EventKey]bool{}
	for _, key := range registry.Keys() {
		registered[key] = true
	}

	emitted := scanEmittedDomainEventKeys(t, moduleRoot)
	if len(emitted) == 0 {
		t.Fatal("scanner found zero emitted domain events — this test needs updating alongside the implementation")
	}

	var missing []string
	emittedSet := map[eventschema.EventKey]bool{}
	for _, site := range emitted {
		emittedSet[site.EventKey] = true
		if !registered[site.EventKey] {
			missing = append(missing, fmt.Sprintf("%s v%d has no registered decoder (emitted at %s)", site.EventType, site.SchemaVersion, site.site))
		}
	}
	sort.Strings(missing)
	for _, msg := range missing {
		t.Error(msg)
	}
	t.Logf("domain-event catalog: %d registered key(s), %d emitted key(s), %d missing decoder(s)", len(registered), len(emittedSet), len(missing))

	// The reverse direction (registered but not found emitted by this scan)
	// is intentionally NOT a failure: docs/design/08-v6-api-projections.md
	// V6-00A's own "Không làm: ... sửa raw history" plus ADR-008/ADR-015 (a
	// stored raw event is immutable forever) mean a decoder for a retired
	// emission site must stay registered so old journal rows keep decoding
	// even after the code path that produced them is gone. Logged, not
	// failed, so a reviewer can sanity-check it is a retired site rather
	// than a scanner gap.
	var unemitted []string
	for key := range registered {
		if !emittedSet[key] {
			unemitted = append(unemitted, fmt.Sprintf("%s v%d", key.EventType, key.SchemaVersion))
		}
	}
	sort.Strings(unemitted)
	for _, msg := range unemitted {
		t.Logf("registered but not found emitted by this scan (expected for a retired emission site): %s", msg)
	}
}

// scanEmittedDomainEventKeys covers the three distinct shapes this
// codebase actually uses to append a domain_events row, per this task's
// own exhaustive, previously-manual audit (see
// internal/adapters/sqlite/event_schema.go's own doc comment for the
// architectural background on why two of these bypass
// ports.EventsRepository entirely):
//
//  1. ports.DomainEvent{EventType: ..., SchemaVersion: ...} composite
//     literals passed to tx.Events().Append — every app-layer command
//     handler's own path, anywhere under internal/app.
//  2. repositoryWorkspaceEvent{eventType: ...} composite literals in
//     internal/adapters/sqlite/workspace_lifecycle.go — a raw INSERT INTO
//     domain_events wrapper with a fixed schema version of 1.
//  3. Raw 'EVENT_TYPE', N literal pairs embedded directly in INSERT INTO
//     domain_events SQL text (attempt_store.go, node_dispatch.go,
//     workflow_store.go) — event_schema.go's own doc comment records the
//     deliberate choice to leave these as SQL literals rather than
//     parameterize them.
func scanEmittedDomainEventKeys(t *testing.T, moduleRoot string) []eventKeySite {
	t.Helper()
	var sites []eventKeySite
	sites = append(sites, scanPortsDomainEventLiterals(t, filepath.Join(moduleRoot, "internal", "app"))...)
	sqliteDir := filepath.Join(moduleRoot, "internal", "adapters", "sqlite")
	sites = append(sites, scanRepositoryWorkspaceEventLiterals(t, sqliteDir)...)
	sites = append(sites, scanRawSQLDomainEventLiterals(t, sqliteDir)...)
	return sites
}

// scanPortsDomainEventLiterals is emission Shape 1 (see
// scanEmittedDomainEventKeys' own doc comment above): every
// ports.DomainEvent{} composite literal under root, resolving EventType/
// SchemaVersion field values against the constructing file's own package-
// level constants (every real call site in this codebase names a shared
// XxxEventType/XxxSchemaVersion pair rather than inlining a literal).
func scanPortsDomainEventLiterals(t *testing.T, root string) []eventKeySite {
	t.Helper()
	var sites []eventKeySite
	for _, dir := range collectGoDirs(t, root) {
		consts := collectPackageConstants(t, dir)
		forEachNonTestFile(t, dir, func(path string, fset *token.FileSet, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok || pkgIdent.Name != "ports" || sel.Sel.Name != "DomainEvent" {
					return true
				}
				key, ok := resolveEventKeyFromCompositeLit(t, path, fset, lit, consts, "EventType", "SchemaVersion")
				if !ok {
					return true
				}
				sites = append(sites, eventKeySite{EventKey: key, site: fmt.Sprintf("%s:%d", path, fset.Position(lit.Pos()).Line)})
				return true
			})
		})
	}
	return sites
}

// scanRepositoryWorkspaceEventLiterals is emission Shape 2. SchemaVersion
// is not one of repositoryWorkspaceEvent's own fields —
// appendRepositoryWorkspaceEvent's shared SQL text hardcodes it as the
// literal 1 for every call site (verified by inspection: `VALUES (?, ?,
// 'RepositoryWorkspace', ?, ?, ?, ?, 1, ?, ?, ?)`) — so this scanner
// hardcodes the same fact rather than re-deriving it from SQL text.
func scanRepositoryWorkspaceEventLiterals(t *testing.T, dir string) []eventKeySite {
	t.Helper()
	const hardcodedSchemaVersion = 1
	consts := collectPackageConstants(t, dir)
	var sites []eventKeySite
	forEachNonTestFile(t, dir, func(path string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ident, ok := lit.Type.(*ast.Ident)
			if !ok || ident.Name != "repositoryWorkspaceEvent" {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				keyIdent, ok := kv.Key.(*ast.Ident)
				if !ok || keyIdent.Name != "eventType" {
					continue
				}
				eventType, ok := resolveStringExpr(t, path, fset, kv.Value, consts)
				if !ok {
					continue
				}
				sites = append(sites, eventKeySite{
					EventKey: eventschema.EventKey{EventType: eventType, SchemaVersion: hardcodedSchemaVersion},
					site:     fmt.Sprintf("%s:%d", path, fset.Position(lit.Pos()).Line),
				})
			}
			return true
		})
	})
	return sites
}

// domainEventSQLLiteralPattern matches an 'EVENT_TYPE', N pair as it
// appears in this codebase's own raw INSERT INTO domain_events SQL text —
// an all-uppercase/underscore quoted literal immediately followed by a
// comma and an integer literal (the event_type, schema_version column
// pair). Deliberately strict (no lowercase allowed) so it never matches an
// unrelated adjacent literal like 'NodeRun' or 'RepositoryWorkspace' (an
// aggregate_type value in the same INSERT statements, always mixed-case in
// this codebase).
var domainEventSQLLiteralPattern = regexp.MustCompile(`'([A-Z][A-Z0-9_]*)',\s*(\d+)`)

// scanRawSQLDomainEventLiterals is emission Shape 3. It scans every string
// literal that mentions "INSERT INTO domain_events" — scoping by content
// rather than by filename so a future raw INSERT added anywhere in this
// package is picked up automatically — rather than parsing SQL for real,
// which this codebase's own event_schema.go doc comment already treats as
// deliberately out of scope for these particular call sites.
func scanRawSQLDomainEventLiterals(t *testing.T, dir string) []eventKeySite {
	t.Helper()
	var sites []eventKeySite
	forEachNonTestFile(t, dir, func(path string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if !strings.Contains(lit.Value, "INSERT INTO domain_events") {
				return true
			}
			for _, match := range domainEventSQLLiteralPattern.FindAllStringSubmatch(lit.Value, -1) {
				schemaVersion, err := strconv.Atoi(match[2])
				if err != nil {
					t.Fatalf("%s:%d: parse schema version from SQL literal %q: %v", path, fset.Position(lit.Pos()).Line, match[0], err)
				}
				sites = append(sites, eventKeySite{
					EventKey: eventschema.EventKey{EventType: match[1], SchemaVersion: schemaVersion},
					site:     fmt.Sprintf("%s:%d", path, fset.Position(lit.Pos()).Line),
				})
			}
			return true
		})
	})
	return sites
}

// resolveEventKeyFromCompositeLit extracts the (EventType, SchemaVersion)
// key from a composite literal's typeField/versionField keyed fields,
// resolving a bare identifier against consts. An expression this cannot
// statically resolve fails the test loudly (via resolveStringExpr/
// resolveIntExpr's own t.Error) rather than being silently skipped: a call
// site the scanner cannot prove a key for defeats this guard's entire
// fail-closed purpose.
func resolveEventKeyFromCompositeLit(t *testing.T, path string, fset *token.FileSet, lit *ast.CompositeLit, consts map[string]any, typeField, versionField string) (eventschema.EventKey, bool) {
	t.Helper()
	var eventType string
	var schemaVersion int
	var haveType, haveVersion bool
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		keyIdent, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch keyIdent.Name {
		case typeField:
			eventType, haveType = resolveStringExpr(t, path, fset, kv.Value, consts)
		case versionField:
			schemaVersion, haveVersion = resolveIntExpr(t, path, fset, kv.Value, consts)
		}
	}
	if !haveType || !haveVersion {
		return eventschema.EventKey{}, false
	}
	return eventschema.EventKey{EventType: eventType, SchemaVersion: schemaVersion}, true
}

func resolveStringExpr(t *testing.T, path string, fset *token.FileSet, expr ast.Expr, consts map[string]any) (string, bool) {
	t.Helper()
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)
		if err != nil {
			t.Fatalf("%s:%d: unquote string literal: %v", path, fset.Position(e.Pos()).Line, err)
		}
		return value, true
	case *ast.Ident:
		value, ok := consts[e.Name]
		if !ok {
			t.Errorf("%s:%d: references %s, which is not a resolvable package-level string constant — this scanner cannot prove the emitted key, defeating its own fail-closed purpose; use a plain string constant like every other event in this codebase", path, fset.Position(e.Pos()).Line, e.Name)
			return "", false
		}
		str, ok := value.(string)
		if !ok {
			t.Errorf("%s:%d: constant %s is not a string", path, fset.Position(e.Pos()).Line, e.Name)
			return "", false
		}
		return str, true
	default:
		t.Errorf("%s:%d: value is not a string literal or identifier this scanner can resolve", path, fset.Position(expr.Pos()).Line)
		return "", false
	}
}

func resolveIntExpr(t *testing.T, path string, fset *token.FileSet, expr ast.Expr, consts map[string]any) (int, bool) {
	t.Helper()
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.INT {
			return 0, false
		}
		value, err := strconv.Atoi(e.Value)
		if err != nil {
			t.Fatalf("%s:%d: parse int literal: %v", path, fset.Position(e.Pos()).Line, err)
		}
		return value, true
	case *ast.Ident:
		value, ok := consts[e.Name]
		if !ok {
			t.Errorf("%s:%d: references %s, which is not a resolvable package-level int constant", path, fset.Position(e.Pos()).Line, e.Name)
			return 0, false
		}
		i, ok := value.(int)
		if !ok {
			t.Errorf("%s:%d: constant %s is not an int", path, fset.Position(e.Pos()).Line, e.Name)
			return 0, false
		}
		return i, true
	default:
		t.Errorf("%s:%d: value is not an int literal or identifier this scanner can resolve", path, fset.Position(expr.Pos()).Line)
		return 0, false
	}
}

// collectPackageConstants returns every string/int literal constant
// declared at package scope across dir's own non-test .go files, keyed by
// name — resolving an identifier used as a field value back to the literal
// it actually names, exactly like every event_schema.go in this codebase
// declares its own XxxEventType/XxxSchemaVersion pair.
func collectPackageConstants(t *testing.T, dir string) map[string]any {
	t.Helper()
	consts := map[string]any{}
	forEachNonTestFile(t, dir, func(path string, fset *token.FileSet, file *ast.File) {
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.CONST {
				continue
			}
			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range valueSpec.Names {
					if i >= len(valueSpec.Values) {
						continue
					}
					lit, ok := valueSpec.Values[i].(*ast.BasicLit)
					if !ok {
						continue
					}
					switch lit.Kind {
					case token.STRING:
						value, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatalf("%s: unquote const %s: %v", path, name.Name, err)
						}
						consts[name.Name] = value
					case token.INT:
						value, err := strconv.Atoi(lit.Value)
						if err != nil {
							t.Fatalf("%s: parse int const %s: %v", path, name.Name, err)
						}
						consts[name.Name] = value
					}
				}
			}
		}
	})
	return consts
}

// collectGoDirs returns every directory under root containing at least one
// .go file — root itself included when it directly holds .go files — so a
// caller can process one Go package (directory) at a time, which constant
// resolution needs (package scope, not global).
func collectGoDirs(t *testing.T, root string) []string {
	t.Helper()
	seen := map[string]bool{}
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		dir := filepath.Dir(path)
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return dirs
}

// forEachNonTestFile parses every non-test .go file directly inside dir
// (no recursion) and calls fn with its parsed AST.
func forEachNonTestFile(t *testing.T, dir string, fn func(path string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		fn(path, fset, file)
	}
}
