package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func serveTestDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "agentkit-serve.db")
}

// syncBuffer wraps bytes.Buffer with a mutex: serve's own goroutine writes
// its stdout announcement concurrently with the test's main goroutine
// polling for it (waitForServeAddress) — a plain bytes.Buffer is not safe
// for that, and the race detector proves it (a real DATA RACE was caught
// here, not a pre-existing/unrelated flake: Write from serve's goroutine
// racing String from waitForServeAddress).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestServe_RequiresDBFlag(t *testing.T) {
	var stdout bytes.Buffer
	err := serve(context.Background(), []string{"--artifact-root", t.TempDir()}, &stdout)
	if err == nil || !isUsageError(err) {
		t.Fatalf("err = %v, want a usage error about --db", err)
	}
}

func TestServe_RequiresArtifactRootFlag(t *testing.T) {
	var stdout bytes.Buffer
	err := serve(context.Background(), []string{"--db", serveTestDB(t)}, &stdout)
	if err == nil || !isUsageError(err) {
		t.Fatalf("err = %v, want a usage error about --artifact-root", err)
	}
}

func TestServe_RejectsMissingArtifactRootDirectory(t *testing.T) {
	var stdout bytes.Buffer
	err := serve(context.Background(), []string{
		"--db", serveTestDB(t),
		"--artifact-root", filepath.Join(t.TempDir(), "does-not-exist"),
	}, &stdout)
	if err == nil {
		t.Fatal("expected an error for a non-existent --artifact-root")
	}
}

// TestServe_StartsServesHealthAndShutsDownGracefully drives serve exactly
// as `aw serve` would, real SQLite database and real artifact root
// directory, and proves the full lifecycle V6-01 promises: it starts,
// announces its bound address on stdout, answers /health/live and
// /health/ready over a real HTTP round trip, and stops cleanly once its
// context is canceled (the same context a real SIGINT/SIGTERM would
// cancel via runServe's own signal.NotifyContext wiring).
func TestServe_StartsServesHealthAndShutsDownGracefully(t *testing.T) {
	dbPath := serveTestDB(t)
	artifactRoot := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	var stdout syncBuffer
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serve(ctx, []string{
			"--db", dbPath,
			"--artifact-root", artifactRoot,
			"--host", "127.0.0.1",
			"--port", "0",
		}, &stdout)
	}()

	addr := waitForServeAddress(t, &stdout)

	client := &http.Client{Timeout: 5 * time.Second}
	liveResp, err := client.Get("http://" + addr + "/health/live")
	if err != nil {
		t.Fatalf("GET /health/live: %v", err)
	}
	liveResp.Body.Close()
	if liveResp.StatusCode != http.StatusOK {
		t.Fatalf("/health/live status = %d, want 200", liveResp.StatusCode)
	}

	readyResp, err := client.Get("http://" + addr + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	readyResp.Body.Close()
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("/health/ready status = %d, want 200 (db+artifact-root+routes should all be ready)", readyResp.StatusCode)
	}

	// Close the idle keep-alive connection ourselves rather than leaving
	// Shutdown to wait for it to go idle on its own — makes shutdown timing
	// deterministic instead of depending on incidental client behavior.
	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve returned error after graceful shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down within 10s of context cancellation")
	}
}

func TestServe_ReadyFailsIfArtifactRootRemoved(t *testing.T) {
	dbPath := serveTestDB(t)
	artifactRoot := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout syncBuffer
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serve(ctx, []string{
			"--db", dbPath,
			"--artifact-root", artifactRoot,
			"--host", "127.0.0.1",
			"--port", "0",
		}, &stdout)
	}()
	defer func() {
		cancel()
		<-serveDone
	}()

	addr := waitForServeAddress(t, &stdout)
	client := &http.Client{Timeout: 5 * time.Second}

	// Sanity: ready before removal.
	resp, err := client.Get("http://" + addr + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status before removal = %d, want 200", resp.StatusCode)
	}

	if err := os.RemoveAll(artifactRoot); err != nil {
		t.Fatalf("remove artifact root: %v", err)
	}

	resp2, err := client.Get("http://" + addr + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status after removing artifact root = %d, want 503", resp2.StatusCode)
	}
	var body struct {
		Checks []struct{ Check string } `json:"checks"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	found := false
	for _, c := range body.Checks {
		if c.Check == "artifact_root" {
			found = true
		}
	}
	if !found {
		t.Fatalf("checks = %+v, want artifact_root named as a failing check", body.Checks)
	}
}

// waitForServeAddress polls stdout for the {"address":"..."} line serve
// writes right after it starts listening, and returns the address.
func waitForServeAddress(t *testing.T, stdout *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		line := strings.TrimSpace(stdout.String())
		if line != "" {
			var body struct {
				Address string `json:"address"`
			}
			if err := json.Unmarshal([]byte(line), &body); err == nil && body.Address != "" {
				return body.Address
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("serve never announced its bound address on stdout")
	return ""
}
