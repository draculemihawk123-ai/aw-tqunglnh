package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// runAdapter is the "adapter" command group's dispatcher
// (docs/design/04-v2-definition-plane.md V2-07B, ADR-022): probe|register|
// list|show against the immutable AdapterBuildVersion registry V2-07A
// already built at the application layer (internal/app/adapterbuild). It
// is deliberately its own top-level command, not nested under
// "definition" -- AdapterBuildVersion is explicitly not a DefinitionKind
// (ADR-022) -- and this file is the first place in cmd/agentkit that
// opens a real SQLite connection, since serve/worker/doctor/definition
// are all still stubs.
func runAdapter(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 {
		return usageError{errors.New("expected 'adapter probe|register|list|show'")}
	}
	verb := arguments[0]
	rest := arguments[1:]
	switch verb {
	case "probe":
		return runAdapterProbe(rest, stdout)
	case "register":
		return runAdapterRegister(rest, stdout)
	case "list":
		return runAdapterList(rest, stdout)
	case "show":
		return runAdapterShow(rest, stdout)
	default:
		return usageError{fmt.Errorf("unknown adapter subcommand %q (want probe|register|list|show)", verb)}
	}
}

// runAdapterProbe runs the configured executable's real, already-built
// AgentExecutor (claude.Adapter/codex.Adapter) to measure a genuinely
// system-observed capability manifest and protocol version, hashes the
// executable file, and asks ProbeAdapterBuild for a server-signed
// candidate token -- printing it to stdout without ever mutating the
// registry. The operator is expected to redirect this to a file (or pipe
// it) and hand it to 'adapter register' once they have reviewed it.
func runAdapterProbe(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("adapter probe", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	providerKey := flags.String("provider", "", "provider key (claude|codex)")
	executablePath := flags.String("executable", "", "path to the provider CLI executable to probe")
	configIdentity := flags.String("config-identity", "default", "operator-assigned identity for this executable's configuration (permission mode, env profile, ...)")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	if strings.TrimSpace(*providerKey) == "" {
		return usageError{errors.New("--provider is required")}
	}
	if strings.TrimSpace(*executablePath) == "" {
		return usageError{errors.New("--executable is required")}
	}

	executor, err := newAgentExecutor(*providerKey, *executablePath)
	if err != nil {
		return usageError{err}
	}

	ctx := context.Background()
	caps, err := executor.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("measure %s capabilities: %w", *providerKey, err)
	}

	store, uow, err := openAdapterBuildDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	token, err := appadapterbuild.ProbeAdapterBuild(ctx, uow, appadapterbuild.ProbeRequest{
		ProviderKey:        string(caps.Provider),
		ExecutablePath:     *executablePath,
		ProtocolVersion:    caps.ProtocolVersion,
		CapabilityManifest: capabilityManifestFromAgentCapabilities(caps),
		OS:                 runtime.GOOS,
		Toolchain:          runtime.Version(),
		ConfigIdentity:     *configIdentity,
	})
	if err != nil {
		return err
	}
	return writeStableJSON(stdout, token)
}

// runAdapterRegister reads a candidate token (from a file, or stdin when
// --token is "-"), re-derives the same real, system-measured capability
// manifest the same way probe did -- from the token's own bound
// provider/executable, never from an operator-typed flag -- and asks
// RegisterAdapterBuild to verify and persist it.
func runAdapterRegister(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("adapter register", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	tokenPath := flags.String("token", "", "path to a candidate token JSON file printed by 'adapter probe', or '-' to read from stdin")
	registeredBy := flags.String("registered-by", "", "operator identity confirming this registration")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	if strings.TrimSpace(*tokenPath) == "" {
		return usageError{errors.New("--token is required")}
	}
	if strings.TrimSpace(*registeredBy) == "" {
		return usageError{errors.New("--registered-by is required")}
	}

	token, err := readCandidateToken(*tokenPath)
	if err != nil {
		return usageError{err}
	}

	// Re-derive the manifest from the token's own bound provider/executable,
	// through the same real AgentExecutor.Capabilities(ctx) probe used --
	// this file defines no capability-shaped flag anywhere for a client to
	// feed a different value in through.
	executor, err := newAgentExecutor(token.Tuple.ProviderKey, token.Tuple.ExecutablePath)
	if err != nil {
		return err
	}
	ctx := context.Background()
	caps, err := executor.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("re-measure %s capabilities: %w", token.Tuple.ProviderKey, err)
	}

	store, uow, err := openAdapterBuildDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	result, err := appadapterbuild.RegisterAdapterBuild(ctx, uow, appadapterbuild.RegisterRequest{
		Token:              token,
		CapabilityManifest: capabilityManifestFromAgentCapabilities(caps),
		RegisteredBy:       *registeredBy,
	})
	if err != nil {
		return err
	}
	return writeStableJSON(stdout, newAdapterBuildRegisterResultView(result))
}

// runAdapterList prints every registered build as a stable JSON array.
func runAdapterList(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("adapter list", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}

	ctx := context.Background()
	store, uow, err := openAdapterBuildDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	builds, err := appadapterbuild.ListAdapterBuilds(ctx, uow)
	if err != nil {
		return err
	}
	views := make([]adapterBuildView, len(builds))
	for i, build := range builds {
		views[i] = newAdapterBuildView(build)
	}
	return writeStableJSON(stdout, views)
}

// runAdapterShow prints one registered build as stable JSON, or a clean
// "not found" error -- never a raw ports.ErrAdapterBuildNotFound dump --
// for an unknown id.
func runAdapterShow(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("adapter show", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	id := flags.String("id", "", "adapter build version id")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	if strings.TrimSpace(*id) == "" {
		return usageError{errors.New("--id is required")}
	}

	ctx := context.Background()
	store, uow, err := openAdapterBuildDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	build, err := appadapterbuild.GetAdapterBuild(ctx, uow, *id)
	if err != nil {
		if errors.Is(err, ports.ErrAdapterBuildNotFound) {
			return fmt.Errorf("adapter build %q not found", *id)
		}
		return err
	}
	return writeStableJSON(stdout, newAdapterBuildView(build))
}

// newAgentExecutor constructs the real, already-built AgentExecutor
// (V0/V1) for providerKey pointed at executablePath, using the exact same
// construction pattern claude.New/codex.New already establish. This is
// the one and only place probe/register ever obtain an AgentExecutor
// from, and by extension the one and only place either command ever
// obtains a CapabilityManifest from (see
// capabilityManifestFromAgentCapabilities) -- never from an
// operator-typed flag.
func newAgentExecutor(providerKey, executablePath string) (ports.AgentExecutor, error) {
	switch ports.ProviderKey(providerKey) {
	case ports.ProviderClaude:
		return claude.New(process.NewSupervisor(), claude.Config{Executable: executablePath})
	case ports.ProviderCodex:
		return codex.New(process.NewSupervisor(), codex.Config{Executable: executablePath})
	default:
		return nil, fmt.Errorf("adapter: unknown provider %q (want %q or %q)", providerKey, ports.ProviderClaude, ports.ProviderCodex)
	}
}

// capabilityManifestFromAgentCapabilities converts a real AgentExecutor's
// own measured ports.AgentCapabilities into the domain CapabilityManifest
// ProbeAdapterBuild/RegisterAdapterBuild persist. This is deliberately
// the ONLY path this file ever constructs a CapabilityManifest through:
// there is no --supports-start/--supports-resume/--supports-cancel (or
// similar) flag anywhere in runAdapterProbe/runAdapterRegister for an
// operator (or a scripted caller) to type a value into by hand, which is
// what makes "Capability manifest chỉ lấy từ giá trị hệ thống tự đo,
// không nhận từ input của client" (docs/design/04-v2-definition-plane.md
// V2-07B) literally true at this boundary.
//
// As of V5-06/V5-07, both claude.Adapter.Capabilities(ctx) and
// codex.Adapter.Capabilities(ctx) genuinely spawn the configured
// executable's "--version" and report what they observe
// (internal/adapters/providers/{claude,codex}, sharing
// internal/adapters/providers/internal/versionprobe) — V2-07A's own scope
// note had deferred this for both providers. This CLI's responsibility --
// never inventing a client-input path around whichever measurement
// mechanism the port is backed by -- is satisfied regardless of which
// provider is configured.
func capabilityManifestFromAgentCapabilities(caps ports.AgentCapabilities) domainadapterbuild.CapabilityManifest {
	kinds := make([]string, len(caps.CanonicalEventKinds))
	for i, kind := range caps.CanonicalEventKinds {
		kinds[i] = string(kind)
	}
	return domainadapterbuild.CapabilityManifest{
		SupportsStart:       caps.SupportsStart,
		SupportsResume:      caps.SupportsResume,
		SupportsCancel:      caps.SupportsCancel,
		CanonicalEventKinds: kinds,
	}
}

// openAdapterBuildDB opens a real sqlite-backed ports.UnitOfWork at
// dbPath. This is the minimal, direct --db-flag-to-sqlite.Open wiring
// V2-07B's own task brief asks for -- no general composition-root/DI
// framework, since serve/worker/doctor don't need one yet either.
func openAdapterBuildDB(ctx context.Context, dbPath string) (*sqlite.Store, ports.UnitOfWork, error) {
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	return store, sqlite.NewUnitOfWork(store), nil
}

// readCandidateToken reads and JSON-decodes a domainadapterbuild.CandidateToken
// from path, or from stdin when path is "-".
func readCandidateToken(path string) (domainadapterbuild.CandidateToken, error) {
	var reader io.Reader
	if path == "-" {
		reader = os.Stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return domainadapterbuild.CandidateToken{}, fmt.Errorf("open candidate token: %w", err)
		}
		defer file.Close()
		reader = file
	}
	var token domainadapterbuild.CandidateToken
	if err := json.NewDecoder(reader).Decode(&token); err != nil {
		return domainadapterbuild.CandidateToken{}, fmt.Errorf("decode candidate token: %w", err)
	}
	return token, nil
}

// writeStableJSON encodes value as indented JSON with a trailing newline.
// Every value this file ever passes here (CandidateToken, adapterBuildView,
// adapterBuildRegisterResultView, []adapterBuildView) has a fixed struct
// shape with json tags, so field order/name/nesting is fixed by the Go
// type declaration, not by map iteration order -- this is what "JSON ổn
// định" (stable JSON output) means here.
func writeStableJSON(stdout io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON output: %w", err)
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	return err
}

// adapterBuildView is this CLI's own stable, exported-field JSON view of
// a domainadapterbuild.Build. Build's own fields are unexported by design
// (its own doc comment: immutability is structural, accessor-only, not
// merely an access-control convention) -- encoding/json reflecting over
// it directly would silently print "{}" -- so list/show populate this DTO
// through Build's accessor methods instead.
type adapterBuildView struct {
	ID                     string                 `json:"id"`
	ProviderKey            string                 `json:"providerKey"`
	ExecutablePath         string                 `json:"executablePath"`
	ExecutableContentHash  string                 `json:"executableContentHash"`
	ProtocolVersion        string                 `json:"protocolVersion"`
	CapabilityManifestHash string                 `json:"capabilityManifestHash"`
	OS                     string                 `json:"os"`
	Toolchain              string                 `json:"toolchain"`
	ConfigIdentity         string                 `json:"configIdentity"`
	CapabilityManifest     capabilityManifestView `json:"capabilityManifest"`
	RegisteredBy           string                 `json:"registeredBy"`
	RegisteredAt           time.Time              `json:"registeredAt"`
}

type capabilityManifestView struct {
	SupportsStart       bool     `json:"supportsStart"`
	SupportsResume      bool     `json:"supportsResume"`
	SupportsCancel      bool     `json:"supportsCancel"`
	CanonicalEventKinds []string `json:"canonicalEventKinds,omitempty"`
}

func newAdapterBuildView(build domainadapterbuild.Build) adapterBuildView {
	tuple := build.Tuple()
	manifest := build.CapabilityManifest()
	return adapterBuildView{
		ID:                     build.ID(),
		ProviderKey:            tuple.ProviderKey,
		ExecutablePath:         tuple.ExecutablePath,
		ExecutableContentHash:  tuple.ExecutableContentHash,
		ProtocolVersion:        tuple.ProtocolVersion,
		CapabilityManifestHash: tuple.CapabilityManifestHash,
		OS:                     tuple.OS,
		Toolchain:              tuple.Toolchain,
		ConfigIdentity:         tuple.ConfigIdentity,
		CapabilityManifest: capabilityManifestView{
			SupportsStart:       manifest.SupportsStart,
			SupportsResume:      manifest.SupportsResume,
			SupportsCancel:      manifest.SupportsCancel,
			CanonicalEventKinds: manifest.CanonicalEventKinds,
		},
		RegisteredBy: build.RegisteredBy(),
		RegisteredAt: build.RegisteredAt(),
	}
}

type adapterBuildRegisterResultView struct {
	Build          adapterBuildView `json:"build"`
	AlreadyExisted bool             `json:"alreadyExisted"`
}

func newAdapterBuildRegisterResultView(result appadapterbuild.RegisterResult) adapterBuildRegisterResultView {
	return adapterBuildRegisterResultView{
		Build:          newAdapterBuildView(result.Build),
		AlreadyExisted: result.AlreadyExisted,
	}
}
