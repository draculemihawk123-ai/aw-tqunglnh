package cli

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ScopeKind classifies a Descriptor's own ports.CommandScope shape for
// inventory purposes — ADR-028's own eventual four-column parity
// inventory (UI action/query <-> HTTP operationId <-> aw command <->
// public application command/query, composed by V6-15O, not this task)
// reads this field. It is deliberately its own small enum here rather
// than a real ports.CommandScope value: a leaf's real scope (which
// project, specifically) is a per-invocation runtime value built from
// flags when the command actually runs, never registration-time
// metadata.
type ScopeKind string

const (
	ScopeInstallation ScopeKind = "INSTALLATION"
	ScopeProject      ScopeKind = "PROJECT"
)

// CLILocalOperation is the typed sentinel a Descriptor's own
// HTTPOperationID field carries for one of the small number of CLI-only
// leaves ADR-028 explicitly allows (docs/architecture/02-architecture-decisions.md:
// "Ngoại lệ duy nhất là bootstrap/static asset của browser; SSE được biểu
// diễn bằng `aw events watch`" — plus the local-only set `{aw serve, aw
// worker, aw help, aw version, aw evidence verify}` ADR-028 names
// directly). Deliberately never an empty string, so "a leaf forgot to set
// this field" (caught by Descriptor.validate) and "this leaf deliberately
// has no HTTP equivalent" stay distinguishable from each other.
const CLILocalOperation = "CLI_LOCAL"

// Descriptor is one leaf's own registration record — V6-15B's own
// "Leaf descriptor records path, scope, app operation, HTTP operationId
// or typed CLI_LOCAL" line. It carries everything ADR-028's eventual
// four-column parity inventory (V6-15O) will read back out of this
// framework, without that later task needing to know anything about how
// any individual leaf is implemented.
type Descriptor struct {
	// Path is the invocation shape, e.g. []string{"definition", "create"}
	// for `aw definition create` — ADR-028's own "invocation shape (command
	// path + scope discriminator)" parity key.
	Path []string
	// Scope is this leaf's own scope discriminator. ADR-028's own example:
	// "nhánh definition global và project có thể cùng path CLI nhưng lần
	// lượt là `--scope global` và `--project-id <id>`, map tới hai
	// operationId khác nhau" — two Descriptors may legitimately share the
	// same Path only when they differ in Scope; see Registry's own
	// duplicate check, which keys on (Path, Scope) together, never Path
	// alone.
	Scope ScopeKind
	// AppOperation names the public application command/query this leaf
	// calls (e.g. "CreateDefinition") — the fourth column of ADR-028's
	// eventual inventory.
	AppOperation string
	// HTTPOperationID is the matching HTTP operationId this leaf mirrors
	// (V6-02A's own RouteDescriptor.OperationID convention), or the
	// CLILocalOperation sentinel for a leaf with no HTTP equivalent.
	HTTPOperationID string
}

// key is the Registry's own de-duplication key: (Path, Scope) together,
// never Path alone — see Scope's own doc comment for why two Descriptors
// may legitimately share a Path.
func (d Descriptor) key() string {
	return strings.Join(d.Path, " ") + "|" + string(d.Scope)
}

// validate checks the fields Registry.Register requires to be non-empty/
// well-formed before ever admitting a Descriptor — the "missing metadata"
// half of V6-15B's own "descriptor duplicate/missing metadata" verify
// bullet (the "duplicate" half is Registry.Register's own job).
func (d Descriptor) validate() error {
	if len(d.Path) == 0 {
		return fmt.Errorf("cli: descriptor path must not be empty")
	}
	for i, segment := range d.Path {
		if strings.TrimSpace(segment) == "" {
			return fmt.Errorf("cli: descriptor path[%d] must not be blank", i)
		}
	}
	switch d.Scope {
	case ScopeInstallation, ScopeProject:
	default:
		return fmt.Errorf("cli: descriptor %q has invalid scope %q (must be cli.ScopeInstallation or cli.ScopeProject)", strings.Join(d.Path, " "), d.Scope)
	}
	if strings.TrimSpace(d.AppOperation) == "" {
		return fmt.Errorf("cli: descriptor %q must set AppOperation", strings.Join(d.Path, " "))
	}
	if strings.TrimSpace(d.HTTPOperationID) == "" {
		return fmt.Errorf("cli: descriptor %q must set HTTPOperationID (a real HTTP operationId, or cli.CLILocalOperation for a CLI-only leaf)", strings.Join(d.Path, " "))
	}
	return nil
}

// Registry holds every registered Descriptor. Each leaf package registers
// its own Descriptor(s) into Default (via the package-level Register/
// MustRegister) from its own init(), so independent leaf tasks can add
// packages without ever editing a shared file that lists them all — the
// same "own package, own RegisterRoutes(routes, deps) call" discipline
// every HTTP endpoint package already follows since V6-03A, mirrored here
// for the CLI side instead of a `cmd/aw`-style shared map literal
// (cmd/aw/cli.go's own `subcommands` map is exactly the pattern this
// avoids). This is V6-15B's own "Hoàn thành khi: independent leaf tasks
// can add packages/descriptors without shared-file edits" line, made
// real.
//
// A test constructs its own throwaway *Registry via NewRegistry rather
// than touching Default, so registrations from one test never leak into
// another — Default itself is shared, global, process-lifetime state,
// exactly like cli.Default's own doc comment says.
type Registry struct {
	mu          sync.Mutex
	descriptors map[string]Descriptor
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{descriptors: make(map[string]Descriptor)}
}

// Register validates d (see Descriptor.validate) and adds it, or returns
// an error for invalid metadata or a (Path, Scope) pair already
// registered — never silently overwriting an existing entry.
func (r *Registry) Register(d Descriptor) error {
	if err := d.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.descriptors == nil {
		r.descriptors = make(map[string]Descriptor)
	}
	key := d.key()
	if existing, ok := r.descriptors[key]; ok {
		return fmt.Errorf("cli: descriptor %q at scope %q already registered (app operation %q) — duplicate leaf registration",
			strings.Join(d.Path, " "), d.Scope, existing.AppOperation)
	}
	r.descriptors[key] = d
	return nil
}

// MustRegister calls Register and panics on error — the ergonomic form a
// leaf package's own init() calls (mirroring the Go standard library's
// own regexp.MustCompile/database-driver-Register-panics-on-duplicate
// idiom), so ordinary leaf code never needs its own registration
// error-handling boilerplate just to register itself at startup.
func (r *Registry) MustRegister(d Descriptor) {
	if err := r.Register(d); err != nil {
		panic(err)
	}
}

// All returns every registered Descriptor, ordered deterministically by
// (Path, Scope) — never Go map iteration order — so a caller (eventually
// V6-15O's own inventory checker) gets a stable, diffable listing.
func (r *Registry) All() []Descriptor {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]Descriptor, 0, len(r.descriptors))
	for _, d := range r.descriptors {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key() < result[j].key() })
	return result
}

// Default is the shared, package-level Registry every leaf package's own
// init() registers into via the package-level Register/MustRegister
// below, once a real leaf exists (V6-15C+).
var Default = NewRegistry()

// Register adds d to Default. See Registry.Register.
func Register(d Descriptor) error { return Default.Register(d) }

// MustRegister adds d to Default, panicking on error. See
// Registry.MustRegister.
func MustRegister(d Descriptor) { Default.MustRegister(d) }

// All returns every Descriptor registered in Default. See Registry.All.
func All() []Descriptor { return Default.All() }
