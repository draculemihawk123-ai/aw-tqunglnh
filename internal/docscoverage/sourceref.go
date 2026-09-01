package docscoverage

import (
	"regexp"
	"strings"
)

// tokenKind names which of the seven SourceRef prefixes
// (00-roadmap.md §3) a token matched, or tokenInvalid when none did.
type tokenKind string

const (
	tokenADR      tokenKind = "ADR"
	tokenAKArch   tokenKind = "AK-ARCH"
	tokenHE       tokenKind = "HE"
	tokenGCInv    tokenKind = "GC-INV"
	tokenGCAcc    tokenKind = "GC-ACC"
	tokenGCDs     tokenKind = "GC-DS"
	tokenRoadmap  tokenKind = "ROADMAP"
	tokenInvalid  tokenKind = ""
)

var sourceRefPatterns = []struct {
	kind    tokenKind
	pattern *regexp.Regexp
}{
	{tokenADR, regexp.MustCompile(`^ADR-\d{3}$`)},
	{tokenAKArch, regexp.MustCompile(`^AK-ARCH-\d{3}[A-Z]?$`)},
	{tokenHE, regexp.MustCompile(`^HE-\d{2}-[MS]\d{2}$`)},
	{tokenGCInv, regexp.MustCompile(`^GC-INV-\d{2}$`)},
	{tokenGCAcc, regexp.MustCompile(`^GC-ACC-\d{2}$`)},
	{tokenGCDs, regexp.MustCompile(`^GC-DS-\d{2}$`)},
	{tokenRoadmap, regexp.MustCompile(`^ROADMAP-§[0-9]+[A-Za-z]?$`)},
}

// classifyToken matches one already-trimmed token against every grammar
// pattern. A token must match at most one pattern by construction (the
// prefixes are disjoint); if none match, it returns tokenInvalid.
func classifyToken(token string) tokenKind {
	for _, p := range sourceRefPatterns {
		if p.pattern.MatchString(token) {
			return p.kind
		}
	}
	return tokenInvalid
}

// tokenizeNguon splits a raw Nguồn field value into individual SourceRef
// tokens. It never accepts "at least one valid prefix" as sufficient —
// every comma-separated piece becomes its own token that must independently
// pass classifyToken, per 00-roadmap.md §3's explicit warning that a
// substring check "đã từng cho lọt lỗi thật".
func tokenizeNguon(raw string) []string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	tokens := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		t = strings.Trim(t, "`")
		if t == "" {
			continue
		}
		tokens = append(tokens, t)
	}
	return tokens
}
