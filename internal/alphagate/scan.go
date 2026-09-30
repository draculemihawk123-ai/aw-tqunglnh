package alphagate

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
	"strings"
)

// criterionIDPattern matches every phase-labeled criterion family: AK-ARCH,
// GC-INV/ACC/DS and HE-NN-Mxx (the mandatory tier; HE-*-S* siblings are
// citable but never phase-labeled, so a test citing one is not criterion
// evidence).
var criterionIDPattern = regexp.MustCompile(`\b(AK-ARCH-\d{3}[A-Z]?|GC-(?:INV|ACC|DS)-\d{2}|HE-\d{2}-M\d{2})\b`)

// metaPackages are the packages that READ the coverage map. Their tests name
// criterion IDs as fixtures and assertions about the docs, not as behaviour a
// criterion requires, so counting them as evidence would make a criterion look
// tested because the checker's own test mentions it.
var metaPackages = map[string]bool{
	"internal/alphagate":    true,
	"internal/docscoverage": true,
}

// Citation is one place a test source names a criterion. Test is the
// enclosing (or documented) top-level Go test function; it is empty when the
// citation sits outside any test (a package/file comment, a helper, a
// TypeScript test file), in which case the file as a whole is the evidence.
type Citation struct {
	Pkg  string `json:"pkg"`
	File string `json:"file"`
	Test string `json:"test,omitempty"`
}

// Scan is what the repository's own test sources say.
type Scan struct {
	// Tests is the set of top-level Go test functions, keyed "pkgDir|Name".
	Tests map[string]bool
	// Citations maps a criterion ID to every test source citing it.
	Citations map[string][]Citation
}

// HasTest reports whether ref names a test that exists. A slash-qualified
// subtest is checked by its top-level parent: a subtest's name is a runtime
// string, so only the parent is statically knowable — the final-gate run's
// own pass/fail outcome is what proves the subtest itself ran.
func (s Scan) HasTest(ref TestRef) bool {
	name := ref.Name
	if i := strings.IndexByte(name, '/'); i >= 0 {
		name = name[:i]
	}
	return s.Tests[ref.Pkg+"|"+name]
}

// ScanRepository walks the repository's test sources (Go tests under
// internal/ and cmd/, TypeScript tests under web/src and web/e2e). It only
// reads.
func ScanRepository(repoRoot string) (Scan, error) {
	scan := Scan{Tests: map[string]bool{}, Citations: map[string][]Citation{}}
	for _, root := range []string{"internal", "cmd", "web/src", "web/e2e"} {
		base := filepath.Join(repoRoot, filepath.FromSlash(root))
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				switch entry.Name() {
				case "node_modules", "dist", "testdata", ".git":
					return filepath.SkipDir
				}
				if metaPackages[rel] {
					return filepath.SkipDir
				}
				return nil
			}
			switch {
			case strings.HasSuffix(rel, "_test.go"):
				return scanGoTest(path, rel, &scan)
			case isTypeScriptTest(rel):
				return scanTypeScriptTest(path, rel, &scan)
			}
			return nil
		})
		if err != nil {
			return Scan{}, fmt.Errorf("scan %s: %w", root, err)
		}
	}
	for id := range scan.Citations {
		sort.Slice(scan.Citations[id], func(a, b int) bool {
			x, y := scan.Citations[id][a], scan.Citations[id][b]
			if x.File != y.File {
				return x.File < y.File
			}
			return x.Test < y.Test
		})
	}
	return scan, nil
}

func isTypeScriptTest(rel string) bool {
	return strings.HasSuffix(rel, ".test.ts") || strings.HasSuffix(rel, ".test.tsx") || strings.HasSuffix(rel, ".spec.ts")
}

func scanTypeScriptTest(path, rel string, scan *Scan) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, id := range uniqueIDs(string(content)) {
		scan.Citations[id] = append(scan.Citations[id], Citation{Pkg: filepath.ToSlash(filepath.Dir(rel)), File: rel})
	}
	return nil
}

func scanGoTest(path, rel string, scan *Scan) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", rel, err)
	}
	pkg := filepath.ToSlash(filepath.Dir(rel))
	source := string(content)

	// Byte ranges covered by top-level Test functions (doc comment through the
	// closing brace); everything outside them is file-level text.
	type span struct {
		name       string
		start, end int
	}
	var spans []span
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		scan.Tests[pkg+"|"+fn.Name.Name] = true
		start := fset.Position(fn.Pos()).Offset
		if fn.Doc != nil {
			start = fset.Position(fn.Doc.Pos()).Offset
		}
		spans = append(spans, span{fn.Name.Name, start, fset.Position(fn.End()).Offset})
	}

	covered := make([]bool, len(source))
	for _, s := range spans {
		for _, id := range uniqueIDs(source[s.start:s.end]) {
			scan.Citations[id] = appendCitation(scan.Citations[id], Citation{Pkg: pkg, File: rel, Test: s.name})
		}
		for i := s.start; i < s.end && i < len(covered); i++ {
			covered[i] = true
		}
	}
	var outside strings.Builder
	for i := 0; i < len(source); i++ {
		if covered[i] {
			outside.WriteByte(' ')
		} else {
			outside.WriteByte(source[i])
		}
	}
	for _, id := range uniqueIDs(outside.String()) {
		scan.Citations[id] = appendCitation(scan.Citations[id], Citation{Pkg: pkg, File: rel})
	}
	return nil
}

func appendCitation(list []Citation, c Citation) []Citation {
	for _, existing := range list {
		if existing == c {
			return list
		}
	}
	return append(list, c)
}

func uniqueIDs(text string) []string {
	seen := map[string]bool{}
	var ids []string
	for _, m := range criterionIDPattern.FindAllString(text, -1) {
		if !seen[m] {
			seen[m] = true
			ids = append(ids, m)
		}
	}
	return ids
}
