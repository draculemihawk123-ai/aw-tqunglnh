// Command aw-release-build is V8-08's own release build tool
// (docs/design/10-v8-alpha-hardening.md V8-08, ROADMAP's "Reproducible
// cross-platform build"): builds a real `aw`/`aw.exe` release binary with a
// real V7 UI build (a `pnpm build` output of web/) embedded into it via
// internal/adapters/embeddedui, then records a checksum and this exact
// binary's own release manifest (`aw version --json`) alongside it.
//
// This is its own standalone binary, the same precedent as
// cmd/v6-gate/cmd/v8-security-gate/cmd/docs-coverage-check/
// cmd/aw-maintenance already establish, rather than a new cmd/aw
// subcommand — ADR-028/docs/design/08-v6-api-projections.md's own
// CLI_LOCAL closed-set finding (already hit once this session for
// cmd/aw-maintenance) applies here too: a release-packaging tool has no
// runtime HTTP-parity counterpart and operates on the checkout's own
// source tree, not a running installation.
//
// # Warning: mutates the checkout it is pointed at
//
// --repo-root's own internal/adapters/embeddedui/dist directory is
// REPLACED with --ui-dist's own contents before compiling — this tool is
// meant to run against a disposable CI checkout (this repo's own
// spike-gate.yml wires it that way) or a local checkout the operator
// expects to `git checkout -- internal/adapters/embeddedui/dist` afterward,
// never a working tree with uncommitted embeddedui changes already in
// progress.
//
//	aw-release-build --repo-root . --ui-dist web/dist --out dist/aw
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "aw-release-build:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("aw-release-build", flag.ContinueOnError)
	repoRoot := flags.String("repo-root", ".", "the checkout to build from (its internal/adapters/embeddedui/dist is replaced with --ui-dist's own contents)")
	uiDist := flags.String("ui-dist", "", "a real `pnpm build` output of web/ (index.html + assets/) to embed into the binary")
	out := flags.String("out", "", "path to write the compiled binary to (a .exe extension is added automatically on Windows if omitted)")
	manifestOut := flags.String("manifest-out", "", "path to write the built binary's own `aw version --json` manifest to (default: <out>.manifest.json)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *uiDist == "" {
		return errors.New("--ui-dist is required")
	}
	if *out == "" {
		return errors.New("--out is required")
	}
	if *manifestOut == "" {
		*manifestOut = *out + ".manifest.json"
	}

	embeddedDistDir := filepath.Join(*repoRoot, "internal", "adapters", "embeddedui", "dist")
	if err := replaceDir(embeddedDistDir, *uiDist); err != nil {
		return fmt.Errorf("embed UI build: %w", err)
	}

	// This tool never cross-compiles (it always builds ./cmd/aw for the
	// runtime.GOOS/GOARCH it is itself running on, matching this repo's own
	// per-OS-runner CI matrix), so runtime.GOOS reliably names the real
	// target OS here. Windows requires a real .exe extension to execute a
	// binary at all (unlike the POSIX executable bit alone) — go build -o
	// on this environment does NOT auto-append it for an explicit path
	// with no extension, so this tool must, or the version-manifest smoke
	// step below (and any real release consumer on Windows) fails to run
	// the binary it just produced at all.
	outPath := *out
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(outPath), ".exe") {
		outPath += ".exe"
	}
	outAbs, err := filepath.Abs(outPath)
	if err != nil {
		return fmt.Errorf("resolve --out: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outAbs), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	buildCmd := exec.Command("go", "build", "-trimpath", "-o", outAbs, "./cmd/aw")
	buildCmd.Dir = *repoRoot
	buildCmd.Stdout = stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("go build ./cmd/aw: %w", err)
	}

	checksum, err := sha256File(outAbs)
	if err != nil {
		return fmt.Errorf("checksum %s: %w", outAbs, err)
	}
	checksumPath := outAbs + ".sha256"
	checksumLine := fmt.Sprintf("%s  %s\n", checksum, filepath.Base(outAbs))
	if err := os.WriteFile(checksumPath, []byte(checksumLine), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", checksumPath, err)
	}

	versionCmd := exec.Command(outAbs, "version", "--json")
	manifestJSON, err := versionCmd.Output()
	if err != nil {
		return fmt.Errorf("run %s version --json: %w", outAbs, err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return fmt.Errorf("decode manifest from %s version --json: %w", outAbs, err)
	}
	if err := os.WriteFile(*manifestOut, manifestJSON, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *manifestOut, err)
	}

	fmt.Fprintf(stdout, "aw-release-build: built %s (sha256 %s), manifest written to %s\n", outAbs, checksum, *manifestOut)
	if uiEmbedded, _ := manifest["uiEmbedded"].(bool); !uiEmbedded {
		return fmt.Errorf("release binary %s reports uiEmbedded=false — the UI embed did not take effect (see this tool's own doc comment)", outAbs)
	}
	return nil
}

// replaceDir removes every entry currently under destDir (never destDir
// itself, so a caller relying on its own os.Stat of destDir before/after is
// unaffected) and copies every file from srcDir into it, preserving the
// relative subtree shape — the exact "become srcDir" semantics embedding a
// fresh `pnpm build` output needs.
func replaceDir(destDir, srcDir string) error {
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", destDir, err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(destDir, entry.Name())); err != nil {
			return fmt.Errorf("remove %s: %w", filepath.Join(destDir, entry.Name()), err)
		}
	}
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(destDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, content, 0o644)
	})
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
