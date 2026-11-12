package runapp

import (
	"path/filepath"
	"runtime"
	"testing"
)

// A relative argv[0] that names a path is the file under the process directory; a bare name stays for the PATH lookup.
func TestResolveExecutable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backend")
	abs := filepath.Join(t.TempDir(), "bin", "tool")
	cases := []struct{ argv0, want string }{
		{"./mvnw.cmd", filepath.Join(dir, "mvnw.cmd")},
		{"./mvnw", filepath.Join(dir, "mvnw")},
		{"scripts/dev.sh", filepath.Join(dir, "scripts", "dev.sh")},
		{"pnpm", "pnpm"},
		{"mvnw.cmd", "mvnw.cmd"},
		{abs, abs},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, struct{ argv0, want string }{`.\mvnw.cmd`, filepath.Join(dir, "mvnw.cmd")})
	}
	for _, c := range cases {
		if got := resolveExecutable(c.argv0, dir); got != c.want {
			t.Errorf("resolveExecutable(%q) = %q, want %q", c.argv0, got, c.want)
		}
	}
}
