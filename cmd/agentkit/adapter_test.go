package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

func adapterTestDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "agentkit-adapter.db")
}

func writeAdapterExecutable(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-cli")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	return path
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, code exitCode) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = run(args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

func decodeToken(t *testing.T, raw string) domainadapterbuild.CandidateToken {
	t.Helper()
	var token domainadapterbuild.CandidateToken
	if err := json.Unmarshal([]byte(raw), &token); err != nil {
		t.Fatalf("decode candidate token: %v\nraw: %s", err, raw)
	}
	return token
}

func writeTokenFile(t *testing.T, token domainadapterbuild.CandidateToken) string {
	t.Helper()
	encoded, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	path := filepath.Join(t.TempDir(), "candidate-token.json")
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return path
}

// probeAndCaptureToken runs 'adapter probe' via the CLI dispatcher and
// returns the decoded CandidateToken it printed.
func probeAndCaptureToken(t *testing.T, dbPath, provider, executablePath string) domainadapterbuild.CandidateToken {
	t.Helper()
	stdout, stderr, code := runCLI(t, "adapter", "probe", "--db", dbPath, "--provider", provider, "--executable", executablePath)
	if code != exitSuccess {
		t.Fatalf("adapter probe: code=%d stderr=%q", code, stderr)
	}
	return decodeToken(t, stdout)
}

// TestAdapterProbe_DoesNotMutateRegistry is V2-07B's own "probe không
// mutate registry" bar: the registry list before and after a probe must
// be byte-identical, and must stay the empty array — probe's only
// possible write is lazily bootstrapping the signing key, never a
// registry row.
func TestAdapterProbe_DoesNotMutateRegistry(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")

	before, stderr, code := runCLI(t, "adapter", "list", "--db", dbPath)
	if code != exitSuccess {
		t.Fatalf("list before probe: code=%d stderr=%q", code, stderr)
	}

	token := probeAndCaptureToken(t, dbPath, "claude", executablePath)
	if token.Signature == "" {
		t.Fatal("probe should print a signed candidate token")
	}

	after, stderr, code := runCLI(t, "adapter", "list", "--db", dbPath)
	if code != exitSuccess {
		t.Fatalf("list after probe: code=%d stderr=%q", code, stderr)
	}
	if strings.TrimSpace(before) != strings.TrimSpace(after) {
		t.Fatalf("probe mutated the registry list:\nbefore: %s\nafter:  %s", before, after)
	}
	if strings.TrimSpace(after) != "[]" {
		t.Fatalf("adapter list = %q, want empty JSON array", after)
	}
}

// TestAdapterProbeThenRegister_Succeeds is the CLI's own end-to-end proof
// of ADR-022's required flow: probe prints a token, register (given that
// token) creates a real, immutable AdapterBuildVersion.
func TestAdapterProbeThenRegister_Succeeds(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")

	tokenPath := writeTokenFile(t, probeAndCaptureToken(t, dbPath, "claude", executablePath))

	stdout, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", tokenPath, "--registered-by", "operator-1")
	if code != exitSuccess {
		t.Fatalf("register: code=%d stderr=%q", code, stderr)
	}
	var result adapterBuildRegisterResultView
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode register output: %v\nraw: %s", err, stdout)
	}
	if result.AlreadyExisted {
		t.Fatal("first registration should not report alreadyExisted=true")
	}
	if result.Build.ID == "" {
		t.Fatal("registered build should have a non-empty id")
	}
	if result.Build.RegisteredBy != "operator-1" {
		t.Fatalf("registeredBy = %q, want operator-1", result.Build.RegisteredBy)
	}
	if result.Build.ProviderKey != "claude" {
		t.Fatalf("providerKey = %q, want claude", result.Build.ProviderKey)
	}
}

// TestAdapterRegister_DuplicateIsIdempotent re-exercises V2-07A's own
// already-proven idempotency guarantee through the CLI: probing and
// registering the identical, unchanged executable twice must report the
// second registration as alreadyExisted, with the same id, and must
// never create a second row.
func TestAdapterRegister_DuplicateIsIdempotent(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")

	firstTokenPath := writeTokenFile(t, probeAndCaptureToken(t, dbPath, "claude", executablePath))
	firstOut, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", firstTokenPath, "--registered-by", "operator-1")
	if code != exitSuccess {
		t.Fatalf("first register: code=%d stderr=%q", code, stderr)
	}
	var first adapterBuildRegisterResultView
	if err := json.Unmarshal([]byte(firstOut), &first); err != nil {
		t.Fatalf("decode first register output: %v", err)
	}

	secondTokenPath := writeTokenFile(t, probeAndCaptureToken(t, dbPath, "claude", executablePath))
	secondOut, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", secondTokenPath, "--registered-by", "operator-2")
	if code != exitSuccess {
		t.Fatalf("second register: code=%d stderr=%q", code, stderr)
	}
	var second adapterBuildRegisterResultView
	if err := json.Unmarshal([]byte(secondOut), &second); err != nil {
		t.Fatalf("decode second register output: %v", err)
	}
	if !second.AlreadyExisted {
		t.Fatal("second registration of the identical measured tuple should report alreadyExisted=true")
	}
	if second.Build.ID != first.Build.ID {
		t.Fatalf("second registration produced a different id: %s vs %s", second.Build.ID, first.Build.ID)
	}
	if second.Build.RegisteredBy != "operator-1" {
		t.Fatalf("RegisteredBy = %q, want the original operator-1 preserved (a duplicate register must never overwrite)", second.Build.RegisteredBy)
	}

	listOut, stderr, code := runCLI(t, "adapter", "list", "--db", dbPath)
	if code != exitSuccess {
		t.Fatalf("list: code=%d stderr=%q", code, stderr)
	}
	var builds []adapterBuildView
	if err := json.Unmarshal([]byte(listOut), &builds); err != nil {
		t.Fatalf("decode list output: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("len(builds) = %d, want exactly 1 (duplicate register must never create a second row)", len(builds))
	}
}

// TestAdapterRegister_DriftCreatesNewBuild re-exercises V2-07A's own
// "drift creates a new build, never overwrites" guarantee through the
// CLI: a genuinely different executable registered under the same
// provider key becomes a distinct, separate row.
func TestAdapterRegister_DriftCreatesNewBuild(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")

	firstTokenPath := writeTokenFile(t, probeAndCaptureToken(t, dbPath, "claude", executablePath))
	firstOut, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", firstTokenPath, "--registered-by", "operator-1")
	if code != exitSuccess {
		t.Fatalf("first register: code=%d stderr=%q", code, stderr)
	}
	var first adapterBuildRegisterResultView
	if err := json.Unmarshal([]byte(firstOut), &first); err != nil {
		t.Fatalf("decode first register output: %v", err)
	}

	if err := os.WriteFile(executablePath, []byte("binary-v2-genuinely-different"), 0o755); err != nil {
		t.Fatalf("overwrite executable: %v", err)
	}
	secondTokenPath := writeTokenFile(t, probeAndCaptureToken(t, dbPath, "claude", executablePath))
	secondOut, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", secondTokenPath, "--registered-by", "operator-2")
	if code != exitSuccess {
		t.Fatalf("second register: code=%d stderr=%q", code, stderr)
	}
	var second adapterBuildRegisterResultView
	if err := json.Unmarshal([]byte(secondOut), &second); err != nil {
		t.Fatalf("decode second register output: %v", err)
	}
	if second.Build.ID == first.Build.ID {
		t.Fatal("a drifted (genuinely different) executable must register as a distinct build, never overwrite the original")
	}

	listOut, stderr, code := runCLI(t, "adapter", "list", "--db", dbPath)
	if code != exitSuccess {
		t.Fatalf("list: code=%d stderr=%q", code, stderr)
	}
	var builds []adapterBuildView
	if err := json.Unmarshal([]byte(listOut), &builds); err != nil {
		t.Fatalf("decode list output: %v", err)
	}
	if len(builds) != 2 {
		t.Fatalf("len(builds) = %d, want exactly 2 (both the original and the drifted build)", len(builds))
	}
}

// TestAdapterRegister_RejectsExecutableSwappedBetweenProbeAndRegister is
// the literal TOCTOU-closing property ADR-022 exists for, proven through
// the CLI: if the executable is swapped after probe but before register
// (without a fresh probe), register with the now-stale token must be
// rejected as a clean, non-crashing error, and nothing may be persisted.
func TestAdapterRegister_RejectsExecutableSwappedBetweenProbeAndRegister(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-original")

	staleTokenPath := writeTokenFile(t, probeAndCaptureToken(t, dbPath, "claude", executablePath))

	// An operator (or an unrelated upgrade) swaps the binary after the
	// token was issued but before it is confirmed.
	if err := os.WriteFile(executablePath, []byte("binary-SWAPPED"), 0o755); err != nil {
		t.Fatalf("swap executable: %v", err)
	}

	stdout, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", staleTokenPath, "--registered-by", "operator-1")
	if code != exitFailure {
		t.Fatalf("register with a stale token after executable swap: code=%d, want exitFailure; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty on a rejected registration, got %q", stdout)
	}
	if !strings.Contains(stderr, "no longer matches") {
		t.Fatalf("stderr should clearly explain the executable drift, got %q", stderr)
	}

	listOut, listErr, listCode := runCLI(t, "adapter", "list", "--db", dbPath)
	if listCode != exitSuccess {
		t.Fatalf("list: code=%d stderr=%q", listCode, listErr)
	}
	if strings.TrimSpace(listOut) != "[]" {
		t.Fatalf("adapter list = %q, want empty JSON array — a rejected registration must never persist anything", listOut)
	}
}

// loadAdapterSigningKey opens dbPath directly and returns the
// per-installation candidate-token signing key a prior probe already
// bootstrapped — used only to construct a genuinely, correctly-signed
// (but deliberately expired) token for TestAdapterRegister_RejectsExpiredToken,
// so that test exercises real expiry rejection rather than a signature
// mismatch, distinctly from the forged-signature test below.
func loadAdapterSigningKey(t *testing.T, dbPath string) []byte {
	t.Helper()
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db to read signing key: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)
	var key []byte
	err = uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().LoadSigningKey(context.Background())
		key = loaded
		return err
	})
	if err != nil {
		t.Fatalf("load signing key: %v", err)
	}
	return key
}

// TestAdapterRegister_RejectsExpiredToken proves an otherwise
// genuinely, correctly-signed token is rejected once its expiry has
// passed — distinct from TestAdapterRegister_RejectsForgedSignature
// below, which covers a tampered signature instead.
func TestAdapterRegister_RejectsExpiredToken(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")

	token := probeAndCaptureToken(t, dbPath, "claude", executablePath)
	key := loadAdapterSigningKey(t, dbPath)

	expired, err := domainadapterbuild.SignToken(token.Tuple, "expired-nonce", time.Now().UTC().Add(-1*time.Minute), key)
	if err != nil {
		t.Fatalf("sign an already-expired token: %v", err)
	}
	tokenPath := writeTokenFile(t, expired)

	stdout, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", tokenPath, "--registered-by", "operator-1")
	if code != exitFailure {
		t.Fatalf("register with an expired token: code=%d, want exitFailure; stdout=%q stderr=%q", code, stdout, stderr)
	}

	listOut, listErr, listCode := runCLI(t, "adapter", "list", "--db", dbPath)
	if listCode != exitSuccess {
		t.Fatalf("list: code=%d stderr=%q", listCode, listErr)
	}
	if strings.TrimSpace(listOut) != "[]" {
		t.Fatalf("adapter list = %q, want empty JSON array — a rejected registration must never persist anything", listOut)
	}
}

// TestAdapterRegister_RejectsForgedSignature proves a token whose
// signature does not match its (otherwise valid, unexpired) contents is
// rejected — a tampered or outright forged token.
func TestAdapterRegister_RejectsForgedSignature(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")

	token := probeAndCaptureToken(t, dbPath, "claude", executablePath)
	token.Signature = "forged" + token.Signature
	tokenPath := writeTokenFile(t, token)

	stdout, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", tokenPath, "--registered-by", "operator-1")
	if code != exitFailure {
		t.Fatalf("register with a forged signature: code=%d, want exitFailure; stdout=%q stderr=%q", code, stdout, stderr)
	}

	listOut, listErr, listCode := runCLI(t, "adapter", "list", "--db", dbPath)
	if listCode != exitSuccess {
		t.Fatalf("list: code=%d stderr=%q", listCode, listErr)
	}
	if strings.TrimSpace(listOut) != "[]" {
		t.Fatalf("adapter list = %q, want empty JSON array — a rejected registration must never persist anything", listOut)
	}
}

// TestAdapterCLI_TokenSurvivesSeparateProbeAndRegisterInvocations mirrors
// internal/adapters/sqlite/adapterbuild_test.go's own
// TestAdapterBuild_SigningKeySurvivesRestart, but through actual separate
// top-level run() invocations rather than manually closing/reopening a
// *sqlite.Store: every runAdapterProbe/runAdapterRegister call opens its
// own *sqlite.Store and closes it (via defer) before returning, so two
// separate run() calls against the same --db path are already a faithful
// stand-in for "probe and register are two separate CLI process
// invocations" — the signing key must be read back from disk, not merely
// survive in memory across the calls.
func TestAdapterCLI_TokenSurvivesSeparateProbeAndRegisterInvocations(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")

	tokenPath := writeTokenFile(t, probeAndCaptureToken(t, dbPath, "claude", executablePath))

	stdout, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", tokenPath, "--registered-by", "operator-1")
	if code != exitSuccess {
		t.Fatalf("register (as a separate CLI invocation) with a token signed by an earlier invocation: code=%d stderr=%q", code, stderr)
	}
	var result adapterBuildRegisterResultView
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode register output: %v", err)
	}
	if result.Build.ID == "" {
		t.Fatal("expected a successfully registered build")
	}
}

// TestAdapterProbe_CapabilityManifestIsSystemMeasured_NotClientSuppliable
// is V2-07B's own core design requirement made concrete: this CLI defines
// no --supports-start/--supports-resume/--supports-cancel (or similar)
// flag anywhere, so there is no channel for a client to type capability
// data in by hand at all; and the manifest probe actually prints is
// exactly what the real claude.Adapter's own Capabilities(ctx) method —
// this repo's one existing "system measurement" mechanism — reports,
// independent of anything else on the command line.
func TestAdapterProbe_CapabilityManifestIsSystemMeasured_NotClientSuppliable(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")

	// There is no flag to carry a client-supplied capability value —
	// attempting one must fail as an ordinary unknown-flag usage error,
	// not silently accept and (mis)use it.
	_, _, code := runCLI(t, "adapter", "probe", "--db", dbPath, "--provider", "claude", "--executable", executablePath, "--supports-start", "false")
	if code != exitUsage {
		t.Fatalf("probe with an unrecognized --supports-start flag: code=%d, want exitUsage (no client-input channel for capability data should exist)", code)
	}

	token := probeAndCaptureToken(t, dbPath, "claude", executablePath)

	executor, err := claude.New(process.NewSupervisor(), claude.Config{Executable: executablePath})
	if err != nil {
		t.Fatalf("construct the real claude adapter: %v", err)
	}
	wantCaps, err := executor.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	wantManifest := capabilityManifestFromAgentCapabilities(wantCaps)
	_, wantHash, err := domainadapterbuild.HashCapabilityManifest(wantManifest)
	if err != nil {
		t.Fatalf("HashCapabilityManifest: %v", err)
	}
	if token.Tuple.CapabilityManifestHash != wantHash {
		t.Fatalf("probe's capabilityManifestHash = %s, want %s (the real claude.Adapter's own measured Capabilities())", token.Tuple.CapabilityManifestHash, wantHash)
	}
	if token.Tuple.ProtocolVersion != wantCaps.ProtocolVersion {
		t.Fatalf("probe's protocolVersion = %s, want %s", token.Tuple.ProtocolVersion, wantCaps.ProtocolVersion)
	}
}

// TestAdapterShow_NotFoundIsCleanError proves an unknown id maps to a
// clear, operator-facing message rather than a raw
// ports.ErrAdapterBuildNotFound dump.
func TestAdapterShow_NotFoundIsCleanError(t *testing.T) {
	dbPath := adapterTestDB(t)
	stdout, stderr, code := runCLI(t, "adapter", "show", "--db", dbPath, "--id", "does-not-exist")
	if code != exitFailure {
		t.Fatalf("show unknown id: code=%d, want exitFailure", code)
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty, got %q", stdout)
	}
	if !strings.Contains(stderr, `"does-not-exist" not found`) {
		t.Fatalf("stderr should give a clean not-found message naming the id, got %q", stderr)
	}
}

// TestAdapterShow_ReturnsRegisteredBuild proves show's stable JSON output
// round-trips a real registered build's own identity and fields.
func TestAdapterShow_ReturnsRegisteredBuild(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")
	tokenPath := writeTokenFile(t, probeAndCaptureToken(t, dbPath, "claude", executablePath))

	registerOut, stderr, code := runCLI(t, "adapter", "register", "--db", dbPath, "--token", tokenPath, "--registered-by", "operator-1")
	if code != exitSuccess {
		t.Fatalf("register: code=%d stderr=%q", code, stderr)
	}
	var registered adapterBuildRegisterResultView
	if err := json.Unmarshal([]byte(registerOut), &registered); err != nil {
		t.Fatalf("decode register output: %v", err)
	}

	showOut, stderr, code := runCLI(t, "adapter", "show", "--db", dbPath, "--id", registered.Build.ID)
	if code != exitSuccess {
		t.Fatalf("show: code=%d stderr=%q", code, stderr)
	}
	var shown adapterBuildView
	if err := json.Unmarshal([]byte(showOut), &shown); err != nil {
		t.Fatalf("decode show output: %v", err)
	}
	if shown.ID != registered.Build.ID {
		t.Fatalf("show id = %s, want %s", shown.ID, registered.Build.ID)
	}
	if shown.ExecutableContentHash != registered.Build.ExecutableContentHash {
		t.Fatal("show's executableContentHash does not match the registered build")
	}
}

// TestAdapter_MissingRequiredFlags proves every required flag across all
// four subcommands is enforced as a usage error, not a runtime crash.
func TestAdapter_MissingRequiredFlags(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")

	cases := [][]string{
		{"adapter"},
		{"adapter", "bogus-verb"},
		{"adapter", "probe"},
		{"adapter", "probe", "--db", dbPath},
		{"adapter", "probe", "--db", dbPath, "--provider", "claude"},
		{"adapter", "probe", "--provider", "claude", "--executable", executablePath},
		{"adapter", "register"},
		{"adapter", "register", "--db", dbPath},
		{"adapter", "register", "--db", dbPath, "--token", "some-file"},
		{"adapter", "list"},
		{"adapter", "show"},
		{"adapter", "show", "--db", dbPath},
	}
	for _, args := range cases {
		_, _, code := runCLI(t, args...)
		if code != exitUsage {
			t.Errorf("run(%v): code=%d, want exitUsage", args, code)
		}
	}
}

// TestAdapterProbe_UnknownProviderIsUsageError proves an unrecognized
// --provider value is rejected before ever touching the registry.
func TestAdapterProbe_UnknownProviderIsUsageError(t *testing.T) {
	dbPath := adapterTestDB(t)
	executablePath := writeAdapterExecutable(t, "binary-v1")
	_, _, code := runCLI(t, "adapter", "probe", "--db", dbPath, "--provider", "not-a-real-provider", "--executable", executablePath)
	if code != exitUsage {
		t.Fatalf("probe with unknown provider: code=%d, want exitUsage", code)
	}
}
