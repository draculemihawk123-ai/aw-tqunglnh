// Package authoring is the strict YAML/JSON decoder and canonicalizer
// every DefinitionKind's authoring input goes through
// (docs/design/04-v2-definition-plane.md V2-03, AK-ARCH-001, AK-ARCH-020):
// the same semantic authoring content must produce the exact same
// canonical JSON — and therefore the exact same SourceHash — whether it
// was written as YAML or JSON, whichever order its keys happened to be
// written in, and on Windows or Linux. This package never mixes concerns
// with internal/domain/definition: it only ever turns raw authored bytes
// into a validated, canonical form; what happens to that form afterward
// (compiling, publishing) is each kind's own job.
package authoring

import (
	"fmt"
	"strings"
)

// Diagnostic is one decode/validation problem. Line/Column are 1-based
// source positions (0 when not applicable, e.g. a problem that spans the
// whole document rather than one token). Path is a dot-separated field
// path from the document root (e.g. "nodes[2].outcomes") pinpointing
// which value the problem is about, independent of Line/Column — useful
// even for formats or problems where a precise source position isn't
// available. What/Why/Fix follow the same convention
// internal/app/config.Validate already established: WHAT is wrong, WHY
// it matters, FIX is the concrete next step — never one bare sentence
// that makes an author guess at what to do next.
type Diagnostic struct {
	Line   int
	Column int
	Path   string
	What   string
	Why    string
	Fix    string
}

func (d Diagnostic) Error() string {
	var location strings.Builder
	if d.Line > 0 {
		fmt.Fprintf(&location, "%d:%d ", d.Line, d.Column)
	}
	if d.Path != "" {
		fmt.Fprintf(&location, "%s: ", d.Path)
	}
	return fmt.Sprintf("%sWHAT %s; WHY %s; FIX %s", location.String(), d.What, d.Why, d.Fix)
}

// Diagnostics is every problem found in one decode/canonicalize attempt.
// Like config.Validate, it collects every problem in one pass rather than
// stopping at the first — an author fixing a file from scratch needs the
// whole picture, not one round trip per mistake.
type Diagnostics []Diagnostic

func (ds Diagnostics) Error() string {
	lines := make([]string, len(ds))
	for i, d := range ds {
		lines[i] = d.Error()
	}
	return strings.Join(lines, " | ")
}

// HasProblems reports whether ds contains at least one Diagnostic.
func (ds Diagnostics) HasProblems() bool { return len(ds) > 0 }

// AsError returns ds as an error, or nil if ds is empty — the usual
// "return diagnostics.AsError()" pattern at the end of a validation pass.
func (ds Diagnostics) AsError() error {
	if len(ds) == 0 {
		return nil
	}
	return ds
}
