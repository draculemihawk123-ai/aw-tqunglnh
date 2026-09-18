package apicontract

import (
	"regexp"
	"strings"
)

// UXRow is one parsed data row from docs/design/11-v6-00-ux-artifact.md's
// own Actions/Queries tables (§2-§14, one per screen) or its Cross-cutting
// concerns table (§15) — every table sharing the same three columns this
// package's parser locks onto: "Proposed operationId", "Public
// application command/query" and "Owner Task ID" (§1 of that doc names
// these exact column headers). ParseUXDoc is NOT a general Markdown table
// parser — it recognizes only this one, consistent table shape, and
// silently ignores any other table in the file (e.g. §0's "13 screen đến
// từ đâu" summary table, whose columns are Screen/Owner UI Task/Owner
// backend Task(s), never "Proposed operationId").
type UXRow struct {
	// Section is the nearest preceding "## ..." heading text, kept for
	// readable findings/error messages (e.g. "2. Screen 1 — Doctor
	// (first-run / ongoing installation health)").
	Section string
	// OperationIDs is every backtick-quoted identifier found in this row's
	// own "Proposed operationId" cell — almost always exactly one, except
	// a couple of §15 cross-cutting rows that name a request/status pair
	// ("`requestProjectionRebuild` / `getProjectionRebuildStatus`"). A row
	// whose cell names no identifier at all (e.g.
	// "*(không phải action riêng)*" or "*(không áp dụng)*") is not
	// returned by ParseUXDoc at all — it proposes nothing to cross-check.
	OperationIDs []string
	// OwnerTaskID is the raw "Owner Task ID" cell text, verbatim (may
	// itself name more than one Task ID, e.g. "V6-10E / V6-10F" or
	// "V6-10I (hardening) → V6-10J (route)").
	OwnerTaskID string
	// ChuaCoGap is true when this row's own "Public application
	// command/query" cell contains the literal "[CHƯA CÓ" marker (V6-00's
	// own §1: an unimplemented-at-doc-authoring-time cell) — as opposed to
	// "[ĐÃ CÓ...]" or no bracket marker at all.
	ChuaCoGap bool
}

// operationIDCellPattern matches every backtick-quoted identifier in a
// "Proposed operationId" cell — deliberately narrow (camelCase
// identifiers only, this doc's own convention for every proposed
// operationId) rather than "anything inside backticks", so a stray
// backtick-quoted path fragment or type name elsewhere could never be
// mistaken for a proposed operationId.
var operationIDCellPattern = regexp.MustCompile("`([a-zA-Z][a-zA-Z0-9]*)`")

// chuaCoMarkerPattern matches the "[CHƯA CÓ" gap marker (V6-00's own §1:
// "chưa tồn tại (task backend chưa chạy)... đánh dấu [CHƯA CÓ]") — matched
// as a prefix so both the bare "[CHƯA CÓ]" and the extended
// "[CHƯA CÓ — ghi chú thêm]" form both count.
var chuaCoMarkerPattern = regexp.MustCompile(`\[CHƯA CÓ`)

// headingPattern matches a Markdown "## " (level-2) heading — the level
// this doc uses for each of its 13 screen sections (§2-§14) and its
// cross-cutting section (§15); §1/§0/§16/§17 use the same level but carry
// no Actions/Queries-shaped table, so they simply never produce a UXRow.
var headingPattern = regexp.MustCompile(`^##\s+(.+?)\s*$`)

// ParseUXDoc scans markdown (the raw contents of
// docs/design/11-v6-00-ux-artifact.md) and returns every UXRow whose
// "Proposed operationId" cell names at least one real identifier. See this
// file's own doc comment for the exact, narrow table shape recognized —
// a data row missing the required "Proposed operationId"/"Owner Task
// ID"/"Public application command/query" headers on its own table (any
// table other than an Actions/Queries or Cross-cutting concerns one) is
// silently skipped, never an error: this parser only ever pulls rows OUT
// of the one shape it understands, it does not validate every table in
// the file conforms to it.
func ParseUXDoc(markdown string) []UXRow {
	var rows []UXRow
	var section string
	var header []string
	inTable := false

	for _, line := range strings.Split(markdown, "\n") {
		if m := headingPattern.FindStringSubmatch(line); m != nil {
			section = m[1]
			inTable = false
			header = nil
			continue
		}

		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			inTable = false
			header = nil
			continue
		}

		cells := splitTableRow(trimmed)
		if isSeparatorRow(cells) {
			continue
		}

		if !inTable {
			header = cells
			inTable = true
			continue
		}

		if row, ok := parseUXDataRow(cells, header, section); ok {
			rows = append(rows, row)
		}
	}
	return rows
}

// parseUXDataRow extracts a UXRow from one already-split data-row cells
// slice, using header to locate the three columns this package cares
// about by NAME (never a hardcoded position — the Actions/Queries tables
// and the Cross-cutting concerns table put these same three columns at
// different positions). Returns ok=false when header does not carry all
// three required columns (a table this parser does not understand) or
// when the row's own "Proposed operationId" cell names no real
// identifier.
func parseUXDataRow(cells, header []string, section string) (UXRow, bool) {
	opIdx := columnIndex(header, "Proposed operationId")
	ownerIdx := columnIndex(header, "Owner Task ID")
	publicIdx := columnIndex(header, "Public application command/query")
	if opIdx < 0 || ownerIdx < 0 || publicIdx < 0 {
		return UXRow{}, false
	}
	if opIdx >= len(cells) || ownerIdx >= len(cells) || publicIdx >= len(cells) {
		return UXRow{}, false
	}

	matches := operationIDCellPattern.FindAllStringSubmatch(cells[opIdx], -1)
	if len(matches) == 0 {
		return UXRow{}, false
	}
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m[1])
	}

	return UXRow{
		Section:      section,
		OperationIDs: ids,
		OwnerTaskID:  strings.TrimSpace(cells[ownerIdx]),
		ChuaCoGap:    chuaCoMarkerPattern.MatchString(cells[publicIdx]),
	}, true
}

// columnIndex returns the index of the header cell that equals name
// exactly (after trimming), or -1 if none does.
func columnIndex(header []string, name string) int {
	for i, h := range header {
		if strings.TrimSpace(h) == name {
			return i
		}
	}
	return -1
}

// escapedPipePlaceholder stands in for a Markdown-escaped literal pipe
// (`\|`) while splitTableRow's own delimiter split runs, so a cell whose
// own TEXT needs a literal "|" (e.g. this doc's own
// "`aw artifact get --output <path\|->`" cell, §12 Screen 11 row 4) is
// never mistaken for a column boundary. Chosen as NUL-delimited control
// characters specifically because they can never legitimately appear in
// this Markdown document's own text.
const escapedPipePlaceholder = "\x00ESCAPED_PIPE\x00"

// splitTableRow splits one already-trimmed "| a | b | c |"-shaped line
// into its cells, trimming each cell's own surrounding whitespace and
// restoring any Markdown-escaped literal pipe (`\|`) back to a plain "|"
// inside the cell text it belongs to (see escapedPipePlaceholder's own
// doc comment — without this step, a cell like
// "`aw artifact get --output <path\|->`" would be incorrectly split into
// two cells, shifting every column after it by one for that row). The
// leading/trailing "|" produce no empty leading/trailing element because
// they are stripped before splitting.
func splitTableRow(line string) []string {
	line = strings.ReplaceAll(line, `\|`, escapedPipePlaceholder)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		parts[i] = strings.ReplaceAll(parts[i], escapedPipePlaceholder, "|")
	}
	return parts
}

// isSeparatorRow reports whether cells is a Markdown table header
// separator row (every cell made up only of "-"/":" characters, e.g.
// "---" or ":--:") — such a row carries no data and must never be
// mistaken for either a header or a data row.
func isSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			return false
		}
		for _, r := range c {
			if r != '-' && r != ':' {
				return false
			}
		}
	}
	return true
}
