package parity

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/apicontract"
)

// Class names one kind of parity violation. AllClasses lists every one; the
// fixture suite proves each is detected by planting it.
type Class string

const (
	// ClassMissingCLI: an HTTP operation (or a UX row's resolved HTTP
	// operation) no CLI descriptor mirrors — ADR-028's "mọi public
	// query/mutation mà V7 UI dùng MUST có đúng một lệnh aw".
	ClassMissingCLI Class = "MISSING_CLI"
	// ClassMissingHTTP: a UX proposal that resolves to no registered route,
	// a non-local CLI descriptor naming an unregistered operationId, or a
	// public registry operation exposed through no route.
	ClassMissingHTTP Class = "MISSING_HTTP"
	// ClassMissingApp: an operation reaching a delivery adapter without a
	// public application operation behind it (no registry entry, or a
	// registry entry that is not an application operation at all).
	ClassMissingApp Class = "MISSING_APP"
	// ClassDuplicate: two parties claim one authority — two CLI descriptors
	// for one operationId or one (path, scope), two registry entries for one
	// name or operationId, or two UX rows that give one proposed operationId
	// different owners.
	ClassDuplicate Class = "DUPLICATE"
	// ClassScopeMismatch: descriptor, route and registry disagree on
	// INSTALLATION versus PROJECT scope (ADR-025's closed list).
	ClassScopeMismatch Class = "SCOPE_MISMATCH"
	// ClassKindMismatch: a query served by a mutating route (or the
	// reverse), across UX kind, HTTP method and registry kind.
	ClassKindMismatch Class = "KIND_MISMATCH"
	// ClassAppMismatch: the registry entry a descriptor names exists but does
	// not bind the descriptor's HTTP operationId.
	ClassAppMismatch Class = "APP_MISMATCH"
	// ClassInternalExposed: a scheduler/worker-only operation reachable from
	// the CLI or HTTP (ADR-028's forbidden AdvanceRun and siblings).
	ClassInternalExposed Class = "INTERNAL_EXPOSED"
	// ClassRemoteGitExposed: a CLI path, application operation, route or
	// operationId naming a remote Git action (ADR-014: no push/fetch/PR/merge/
	// rebase/force-push in Alpha).
	ClassRemoteGitExposed Class = "REMOTE_GIT_EXPOSED"
	// ClassCLILocalNotAllowed: a CLI_LOCAL descriptor outside the closed set
	// serve/worker/help/version/evidence verify.
	ClassCLILocalNotAllowed Class = "CLI_LOCAL_NOT_ALLOWED"
	// ClassConfirmationMismatch: a descriptor's HighImpact differs from the
	// registry's (which cites the UX inventory).
	ClassConfirmationMismatch Class = "CONFIRMATION_MISMATCH"
	// ClassUXLeafMismatch: the `aw` invocation shape the UX inventory
	// reserved is not the registered CLI path and no reviewed rename covers
	// the difference.
	ClassUXLeafMismatch Class = "UX_LEAF_MISMATCH"
	// ClassRouteMissing: a CLI descriptor whose path the composed router
	// cannot dispatch — registered but unreachable from os.Args.
	ClassRouteMissing Class = "ROUTE_MISSING"
)

// AllClasses is every violation class Check can report.
func AllClasses() []Class {
	return []Class{
		ClassMissingCLI, ClassMissingHTTP, ClassMissingApp, ClassDuplicate, ClassScopeMismatch,
		ClassKindMismatch, ClassAppMismatch, ClassInternalExposed, ClassRemoteGitExposed,
		ClassCLILocalNotAllowed, ClassConfirmationMismatch, ClassUXLeafMismatch, ClassRouteMissing,
	}
}

// Finding is one violation. Subject identifies what is wrong in a stable,
// diffable form ("http:getEvidence", "cli:definition list@INSTALLATION",
// "ux:listDefinitions", "app:ListDefinitions"); Key is Class plus Subject.
type Finding struct {
	Class   Class
	Subject string
	Detail  string
}

// Key is the finding's identity, what a LedgerEntry pins.
func (f Finding) Key() string { return string(f.Class) + " " + f.Subject }

// Inputs is everything Check compares.
type Inputs struct {
	UX       []apicontract.UXRow
	HTTP     apicontract.Contract
	Registry []PublicOperation
	CLI      []cli.Descriptor
	// Routes lists the command paths ("run cancel") the composed router can
	// dispatch. nil disables the ROUTE_MISSING check (a caller without a
	// router, e.g. a fixture that only exercises the registries).
	Routes []string
}

// Rules is the closed, reviewed vocabulary Check applies. DefaultRules is
// the production set; the fixture suite passes altered copies.
type Rules struct {
	// CLILocalAllowed is the CLOSED set of command paths that may carry the
	// CLI_LOCAL sentinel (docs/design/08-v6-api-projections.md V6-15O:
	// "CLI_LOCAL closed set is serve/worker/help/version/evidence verify").
	CLILocalAllowed map[string]bool
	// BrowserBootstrap lists HTTP operations exempt from having a CLI
	// mirror: ADR-028's "Ngoại lệ duy nhất là bootstrap/static asset của
	// browser".
	BrowserBootstrap map[string]bool
	// UXProposalRenames maps a UX "Proposed operationId" the parser can find
	// no route for to the real operationIds that cover it, for the rows V6-12
	// classified "[ĐÃ CÓ]" (so its own rename map never evaluates them).
	UXProposalRenames map[string][]string
	// UXLeafRenames maps a UX-reserved `aw` shape (space-joined path) to the
	// registered CLI path that honors it under a reviewed wording change
	// (V6-00 §1: "V6-15B…V6-15O có quyền điều chỉnh chữ, miễn giữ đúng
	// invocation shape").
	UXLeafRenames map[string]string
	// RemoteGitTokens are the lower-case tokens that, appearing in a CLI
	// path, operation name, route path or operationId, mark a remote Git
	// action.
	RemoteGitTokens map[string]bool
	// InternalTokens are the lower-case tokens that mark a scheduler/worker
	// verb in a CLI path or operation name, on top of the registry's own
	// INTERNAL entries.
	InternalTokens map[string]bool
}

// DefaultRules returns the reviewed production rule set.
func DefaultRules() Rules {
	return Rules{
		CLILocalAllowed: map[string]bool{
			"serve": true, "worker": true, "help": true, "version": true, "evidence verify": true,
		},
		BrowserBootstrap: map[string]bool{"bootstrap": true},
		UXProposalRenames: map[string][]string{
			// The four Screen 2 rows V6-00 marked [ĐÃ CÓ] — catalog.go
			// registered each under its own "<resource><Verb>" name.
			"createProject":        {"projectsCreate"},
			"registerRepository":   {"projectRepositoriesRegister"},
			"retryRepositoryProbe": {"repositoriesRetryProbe"},
			"assignComponentPack":  {"componentPackAssignmentsAssign"},
			// Screen 1 row 2 (adapter list) and §15 health rows: V6-12's
			// rename map only covers [CHƯA CÓ] rows; these final names were
			// frozen by the leaf tasks.
			"getHealthLive":  {"healthLive"},
			"getHealthReady": {"healthReady"},
			// Screen 2 row 7 proposed a separate probe-history query; V6-03A
			// merged probe history into the onboarding view (baocaov6checklist.md
			// V6-03A "Quyết định": GET /repositories/{id}/onboarding carries
			// status, error and the whole probe-attempt history), so the
			// concern is served by repositoriesOnboarding.
			"listRepositoryProbeHistory": {"repositoriesOnboarding"},
		},
		UXLeafRenames: map[string]string{
			// V6-15J shipped attachment upload as a message subcommand: the
			// operation appends to the WorkItem conversation (same project
			// scope, same positional workItemId).
			"attachment upload": "message upload-attachment",
			// UX §5 row 11: "hai nút/leaf riêng cho cùng một command với
			// Outcome khác nhau" — V6-15I ships one leaf because the outcome
			// vocabulary is open and node-declared (`--outcome`).
			"approval approve": "approval resolve",
			"approval reject":  "approval resolve",
			// V6-03A merged repository detail and probe history into the
			// onboarding view (baocaov6checklist.md V6-03A "Quyết định").
			"repository probe-history": "repository onboarding",
			// docs/design/08-v6-api-projections.md V6-15E's own Thực hiện
			// line names the commands "version show/diff" (a top-level
			// `version` resource, ADR-028's `aw <resource> <action>`), where
			// the UX inventory had drafted `aw definition version ...`.
			"definition version show": "version show",
			"definition version diff": "version diff",
		},
		RemoteGitTokens: tokenSet("push", "fetch", "pull", "merge", "rebase", "pr", "remote", "clone", "upstream", "origin"),
		InternalTokens:  tokenSet("advance", "execute", "claim", "reap", "heartbeat", "lease", "sweep", "reconcileinterrupted"),
	}
}

func tokenSet(tokens ...string) map[string]bool {
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		set[t] = true
	}
	return set
}

// Check compares the four parties in both directions and returns every
// violation, sorted, with no ledger applied.
func Check(in Inputs, rules Rules) []Finding {
	c := &checker{in: in, rules: rules, seen: map[string]Finding{}}
	c.index()
	c.checkCLI()
	c.checkHTTP()
	c.checkRegistry()
	c.checkUX()
	return c.result()
}

type checker struct {
	in    Inputs
	rules Rules
	seen  map[string]Finding

	httpOps      map[string]apicontract.Operation
	registryByOp map[string]PublicOperation // by Name
	bindings     map[string][]PublicOperation
	cliByHTTP    map[string][]cli.Descriptor
	routes       map[string]bool
}

func (c *checker) add(class Class, subject, format string, args ...any) {
	f := Finding{Class: class, Subject: subject, Detail: fmt.Sprintf(format, args...)}
	if _, dup := c.seen[f.Key()]; !dup {
		c.seen[f.Key()] = f
	}
}

func (c *checker) result() []Finding {
	out := make([]Finding, 0, len(c.seen))
	for _, f := range c.seen {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

func pathOf(d cli.Descriptor) string { return strings.Join(d.Path, " ") }

func cliSubject(d cli.Descriptor) string { return "cli:" + pathOf(d) + "@" + string(d.Scope) }

func (c *checker) index() {
	c.httpOps = map[string]apicontract.Operation{}
	for _, op := range c.in.HTTP.Operations {
		c.httpOps[op.OperationID] = op
	}
	c.registryByOp = map[string]PublicOperation{}
	c.bindings = map[string][]PublicOperation{}
	for _, op := range c.in.Registry {
		if _, dup := c.registryByOp[op.Name]; dup {
			c.add(ClassDuplicate, "app:"+op.Name, "two registry entries share the name %q", op.Name)
		}
		c.registryByOp[op.Name] = op
		for _, b := range op.HTTP {
			c.bindings[b.OperationID] = append(c.bindings[b.OperationID], op)
		}
	}
	c.cliByHTTP = map[string][]cli.Descriptor{}
	seenKey := map[string]bool{}
	for _, d := range c.in.CLI {
		key := pathOf(d) + "|" + string(d.Scope)
		if seenKey[key] {
			c.add(ClassDuplicate, cliSubject(d), "two CLI descriptors share path %q at scope %s", pathOf(d), d.Scope)
		}
		seenKey[key] = true
		if d.HTTPOperationID != cli.CLILocalOperation {
			c.cliByHTTP[d.HTTPOperationID] = append(c.cliByHTTP[d.HTTPOperationID], d)
		}
	}
	if c.in.Routes != nil {
		c.routes = map[string]bool{}
		for _, r := range c.in.Routes {
			c.routes[r] = true
		}
	}
}

// ---- CLI -> (HTTP, application) ------------------------------------------

func (c *checker) checkCLI() {
	for _, d := range c.in.CLI {
		subject := cliSubject(d)

		if c.routes != nil && !c.routes[pathOf(d)] {
			c.add(ClassRouteMissing, subject, "the composed router cannot dispatch %q", pathOf(d))
		}
		c.checkTokens(subject, append(append([]string{}, d.Path...), d.AppOperation))

		app, hasApp := c.registryByOp[d.AppOperation]
		if hasApp && app.Exposure == ExposureInternal {
			c.add(ClassInternalExposed, subject, "%s is a scheduler/worker-only operation (%s)", d.AppOperation, app.Symbol)
		}

		if d.HTTPOperationID == cli.CLILocalOperation {
			if !c.rules.CLILocalAllowed[pathOf(d)] {
				c.add(ClassCLILocalNotAllowed, subject, "CLI_LOCAL is a closed set (serve, worker, help, version, evidence verify); %q is not in it", pathOf(d))
			}
			if !hasApp {
				c.add(ClassMissingApp, subject, "no registry entry for AppOperation %q", d.AppOperation)
			}
			continue
		}

		if !hasApp {
			c.add(ClassMissingApp, subject, "no public operation named %q in the registry", d.AppOperation)
		}
		op, ok := c.httpOps[d.HTTPOperationID]
		if !ok {
			c.add(ClassMissingHTTP, subject, "HTTP operationId %q is not a registered route", d.HTTPOperationID)
		}
		if ok && string(d.Scope) != op.ScopeKind {
			c.add(ClassScopeMismatch, subject, "descriptor scope %s but route %s is %s", d.Scope, op.OperationID, op.ScopeKind)
		}
		if hasApp && app.Exposure != ExposureInternal {
			binding, bound := bindingFor(app, d.HTTPOperationID)
			switch {
			case !bound:
				c.add(ClassAppMismatch, subject, "registry entry %q does not bind operationId %q", app.Name, d.HTTPOperationID)
			case binding.Scope != d.Scope:
				c.add(ClassScopeMismatch, subject, "descriptor scope %s but registry binds %s as %s", d.Scope, d.HTTPOperationID, binding.Scope)
			}
			if ok && !kindsAgree(app.Kind, op.Method) {
				c.add(ClassKindMismatch, subject, "registry kind %s but route %s is %s %s", app.Kind, op.OperationID, op.Method, op.Path)
			}
			if d.HighImpact != app.HighImpact {
				c.add(ClassConfirmationMismatch, subject, "descriptor HighImpact=%v but registry (UX inventory) says %v for %s", d.HighImpact, app.HighImpact, app.Name)
			}
		}
	}
	// One route claimed by two CLI descriptors is one authority with two
	// front doors.
	for id, ds := range c.cliByHTTP {
		if len(ds) > 1 {
			names := make([]string, len(ds))
			for i, d := range ds {
				names[i] = cliSubject(d)
			}
			sort.Strings(names)
			c.add(ClassDuplicate, "http:"+id, "%d CLI descriptors mirror one operationId: %s", len(ds), strings.Join(names, ", "))
		}
	}
	// Two scoped descriptors sharing a path must agree on confirmation: the
	// operator types one command.
	byPath := map[string]map[bool]bool{}
	for _, d := range c.in.CLI {
		if byPath[pathOf(d)] == nil {
			byPath[pathOf(d)] = map[bool]bool{}
		}
		byPath[pathOf(d)][d.HighImpact] = true
	}
	for path, verdicts := range byPath {
		if len(verdicts) > 1 {
			c.add(ClassConfirmationMismatch, "cli:"+path, "descriptors for %q disagree on HighImpact", path)
		}
	}
}

func bindingFor(op PublicOperation, operationID string) (HTTPBinding, bool) {
	for _, b := range op.HTTP {
		if b.OperationID == operationID {
			return b, true
		}
	}
	return HTTPBinding{}, false
}

func kindsAgree(kind OpKind, method string) bool {
	if kind == KindQuery {
		return method == "GET"
	}
	return method != "GET"
}

// ---- HTTP -> (CLI, application) ------------------------------------------

func (c *checker) checkHTTP() {
	for _, op := range c.in.HTTP.Operations {
		subject := "http:" + op.OperationID
		if c.rules.BrowserBootstrap[op.OperationID] {
			continue
		}
		c.checkTokens(subject, []string{op.OperationID, op.Path})

		if len(c.cliByHTTP[op.OperationID]) == 0 {
			c.add(ClassMissingCLI, subject, "no CLI descriptor mirrors %s %s", op.Method, op.Path)
		}

		owners := c.bindings[op.OperationID]
		switch {
		case len(owners) == 0:
			c.add(ClassMissingApp, subject, "no public operation in the registry is served as %q", op.OperationID)
		case len(owners) > 1:
			names := make([]string, len(owners))
			for i, o := range owners {
				names[i] = o.Name
			}
			sort.Strings(names)
			c.add(ClassDuplicate, subject, "several registry entries claim this route: %s", strings.Join(names, ", "))
		default:
			owner := owners[0]
			switch owner.Exposure {
			case ExposureInternal:
				c.add(ClassInternalExposed, subject, "route serves %s, a scheduler/worker-only operation", owner.Name)
			case ExposureProjectionRead:
				c.add(ClassMissingApp, subject, "the route reads the projection port directly; no public application operation backs it")
			}
			if b, ok := bindingFor(owner, op.OperationID); ok && string(b.Scope) != op.ScopeKind {
				c.add(ClassScopeMismatch, subject, "route scope %s but registry binds it as %s", op.ScopeKind, b.Scope)
			}
			if !kindsAgree(owner.Kind, op.Method) {
				c.add(ClassKindMismatch, subject, "registry kind %s but the route is %s", owner.Kind, op.Method)
			}
		}
	}
}

// ---- registry -> (HTTP) ---------------------------------------------------

func (c *checker) checkRegistry() {
	for _, op := range c.in.Registry {
		subject := "app:" + op.Name
		switch op.Exposure {
		case ExposureInternal:
			// An INTERNAL entry exists precisely to name what must stay
			// unexposed, so its own name carries the worker vocabulary; the
			// violation is any delivery binding.
			if len(op.HTTP) > 0 {
				c.add(ClassInternalExposed, subject, "an internal operation carries HTTP bindings")
			}
			continue
		}
		c.checkTokens(subject, []string{op.Name})
		switch op.Exposure {
		case ExposurePublic:
			if len(op.HTTP) == 0 {
				c.add(ClassMissingHTTP, subject, "public operation %s is served by no HTTP route", op.Name)
			}
		}
		for _, b := range op.HTTP {
			if _, ok := c.httpOps[b.OperationID]; !ok {
				c.add(ClassMissingHTTP, subject, "registry binds %q, which is not a registered route", b.OperationID)
			}
		}
	}
}

// ---- UX -> (HTTP, CLI) ----------------------------------------------------

func (c *checker) checkUX() {
	owners := map[string]map[string]bool{}
	for _, row := range c.in.UX {
		for _, proposed := range row.OperationIDs {
			subject := "ux:" + proposed
			if owners[proposed] == nil {
				owners[proposed] = map[string]bool{}
			}
			owners[proposed][row.OwnerTaskID] = true

			resolved := c.resolve(proposed)
			if len(resolved) == 0 {
				c.add(ClassMissingHTTP, subject, "the UX inventory proposes %q (section %q) but no route covers it", proposed, row.Section)
				continue
			}
			for _, id := range resolved {
				op := c.httpOps[id]
				switch row.Kind {
				case "query":
					if op.Method != "GET" {
						c.add(ClassKindMismatch, subject, "UX says query but %s is %s", id, op.Method)
					}
				case "command":
					if op.Method == "GET" {
						c.add(ClassKindMismatch, subject, "UX says command but %s is GET", id)
					}
				}
				descriptors := c.cliByHTTP[id]
				if len(descriptors) == 0 {
					c.add(ClassMissingCLI, "http:"+id, "no CLI descriptor mirrors %s %s (UX row %q)", op.Method, op.Path, proposed)
					continue
				}
				if len(row.AwLeaves) > 0 && !c.leafHonored(row.AwLeaves, descriptors) {
					c.add(ClassUXLeafMismatch, subject, "UX reserved %s but the CLI registers %s", joinLeaves(row.AwLeaves), joinPaths(descriptors))
				}
			}
		}
	}
	for proposed, set := range owners {
		if len(set) > 1 {
			names := make([]string, 0, len(set))
			for owner := range set {
				names = append(names, owner)
			}
			sort.Strings(names)
			c.add(ClassDuplicate, "ux:"+proposed, "the UX inventory gives %q different owners: %s", proposed, strings.Join(names, " | "))
		}
	}
}

// resolve maps a proposed operationId to registered operationIds: V6-12's own
// resolution first, then this package's reviewed renames.
func (c *checker) resolve(proposed string) []string {
	if got := apicontract.ResolveProposal(proposed, c.in.HTTP); len(got) > 0 {
		return got
	}
	if renamed, ok := c.rules.UXProposalRenames[proposed]; ok {
		var out []string
		for _, id := range renamed {
			if _, registered := c.httpOps[id]; registered {
				out = append(out, id)
			}
		}
		return out
	}
	return nil
}

func (c *checker) leafHonored(reserved [][]string, descriptors []cli.Descriptor) bool {
	for _, want := range reserved {
		wantPath := strings.Join(want, " ")
		if renamed, ok := c.rules.UXLeafRenames[wantPath]; ok {
			wantPath = renamed
		}
		for _, d := range descriptors {
			if pathOf(d) == wantPath {
				return true
			}
			// A reserved `aw <resource> <action> <more>` (e.g. `release-set
			// local-commit status`) whose leading words are the registered
			// command is NOT honored: the trailing subcommand is a distinct
			// invocation shape.
		}
	}
	return false
}

func joinLeaves(leaves [][]string) string {
	parts := make([]string, len(leaves))
	for i, l := range leaves {
		parts[i] = "`aw " + strings.Join(l, " ") + "`"
	}
	return strings.Join(parts, " / ")
}

func joinPaths(ds []cli.Descriptor) string {
	seen := map[string]bool{}
	var parts []string
	for _, d := range ds {
		p := "`aw " + pathOf(d) + "`"
		if !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, " / ")
}

// ---- token rules ----------------------------------------------------------

// checkTokens flags remote-Git and worker-verb vocabulary in the given
// identifiers. Route paths are tokenized with {param} segments removed.
func (c *checker) checkTokens(subject string, identifiers []string) {
	for _, id := range identifiers {
		for _, token := range tokenize(id) {
			if c.rules.RemoteGitTokens[token] {
				c.add(ClassRemoteGitExposed, subject, "%q contains the remote-Git token %q (ADR-014: no remote Git mutation in Alpha)", id, token)
			}
			if c.rules.InternalTokens[token] {
				c.add(ClassInternalExposed, subject, "%q contains the scheduler/worker token %q (ADR-028: internal commands are never exposed)", id, token)
			}
		}
	}
}

// tokenize splits an identifier into lower-case words on non-alphanumerics
// and camelCase boundaries: "requestReleaseSetLocalCommit" ->
// [request release set local commit]; "/projects/{id}/pull-request" ->
// [projects pull request] ({param} segments dropped).
func tokenize(s string) []string {
	var tokens []string
	var word []rune
	flush := func() {
		if len(word) > 0 {
			tokens = append(tokens, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	inBrace := false
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '{':
			flush()
			inBrace = true
		case r == '}':
			inBrace = false
		case inBrace:
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r):
			if len(word) > 0 && (unicode.IsLower(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1]))) {
				flush()
			}
			word = append(word, r)
		default:
			word = append(word, r)
		}
	}
	flush()
	return tokens
}

// ---- ledger ---------------------------------------------------------------

// LedgerEntry pins one finding a reviewer has accepted as KNOWN parity debt,
// with the task that owns closing it and why it cannot be closed here.
type LedgerEntry struct {
	Class   Class
	Subject string
	Owner   string
	Reason  string
}

// Key matches Finding.Key.
func (e LedgerEntry) Key() string { return string(e.Class) + " " + e.Subject }

// Report is the outcome of evaluating findings against a ledger.
type Report struct {
	// Debt is the total number of parity findings — the number V6-15P's gate
	// requires to be zero.
	Debt int
	// Acknowledged are findings a ledger entry pins.
	Acknowledged []Finding
	// New are findings no ledger entry covers: always a gate failure.
	New []Finding
	// Stale are ledger entries no finding matches any more: a resolved debt
	// must have its entry deleted, so the ledger can only shrink.
	Stale []LedgerEntry
}

// Evaluate splits findings against ledger.
func Evaluate(findings []Finding, ledger []LedgerEntry) Report {
	pinned := map[string]LedgerEntry{}
	for _, e := range ledger {
		pinned[e.Key()] = e
	}
	matched := map[string]bool{}
	report := Report{Debt: len(findings)}
	for _, f := range findings {
		if _, ok := pinned[f.Key()]; ok {
			matched[f.Key()] = true
			report.Acknowledged = append(report.Acknowledged, f)
			continue
		}
		report.New = append(report.New, f)
	}
	for _, e := range ledger {
		if !matched[e.Key()] {
			report.Stale = append(report.Stale, e)
		}
	}
	return report
}
