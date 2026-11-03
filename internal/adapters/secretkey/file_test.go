package secretkey

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"testing"
)

func TestFileProvider_MakesTheKeyOnceAndKeepsIt0600(t *testing.T) {
	root := t.TempDir()
	p := NewFileProvider(root)
	key, id, err := p.DataKey(context.Background())
	if err != nil || len(key) != KeyBytes || id == "" {
		t.Fatalf("DataKey = %d bytes, %q, %v", len(key), id, err)
	}
	again, id2, err := NewFileProvider(root).DataKey(context.Background())
	if err != nil || !bytes.Equal(key, again) || id != id2 {
		t.Fatalf("a second provider read another key (%v)", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(p.Path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("key file mode = %v, %v; want 0600", info.Mode().Perm(), err)
		}
	}
	if err := os.WriteFile(p.Path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewFileProvider(root).DataKey(context.Background()); err == nil {
		t.Fatal("a damaged key file must be an error, never a new key over it")
	}
}
