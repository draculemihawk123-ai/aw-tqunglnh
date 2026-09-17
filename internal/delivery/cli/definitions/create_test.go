package definitions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	clidefinitions "github.com/taQuangLing/agent-workflow/internal/delivery/cli/definitions"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func TestRunDefinitionCreate_GeneratesIdempotencyKeyAndCreatesDraft(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{"definitionId":"blk-1","name":"widget"}`)

	if err := clidefinitions.RunDefinitionCreate(context.Background(), deps, []string{"--kind", "BLOCK"}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("RunDefinitionCreate() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		IdempotencyKey string                                `json:"idempotencyKey"`
		Replayed       bool                                  `json:"replayed"`
		Result         appdefinitions.CreateDefinitionResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.IdempotencyKey == "" {
		t.Fatal("no --idempotency-key given, so RunDefinitionCreate must have generated and returned one")
	}
	if envelope.Replayed {
		t.Fatal("first run reported Replayed = true, want false")
	}
	if envelope.Result.DefinitionID != "blk-1" || envelope.Result.Kind != definition.KindBlock {
		t.Fatalf("result = %+v, want DefinitionID=blk-1 Kind=BLOCK", envelope.Result)
	}
}

// TestRunDefinitionCreate_ReplaySameIdempotencyKey_NeverCreatesTwice is
// V6-15E's own "Replay" Verify bullet applied to `aw definition create`:
// the exact same --idempotency-key resubmitted must return the identical
// stored result and report Replayed=true, never create a second
// Definition.
func TestRunDefinitionCreate_ReplaySameIdempotencyKey_NeverCreatesTwice(t *testing.T) {
	deps := newTestDeps(t)
	args := []string{"--kind", "BLOCK", "--idempotency-key", "key-1"}
	body := `{"definitionId":"blk-1","name":"widget"}`

	var first bytes.Buffer
	if err := clidefinitions.RunDefinitionCreate(context.Background(), deps, args, strings.NewReader(body), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunDefinitionCreate() error = %v", err)
	}

	var second bytes.Buffer
	if err := clidefinitions.RunDefinitionCreate(context.Background(), deps, args, strings.NewReader(body), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second RunDefinitionCreate() error = %v", err)
	}
	var secondEnvelope struct {
		Replayed bool                                  `json:"replayed"`
		Result   appdefinitions.CreateDefinitionResult `json:"result"`
	}
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second stdout %s: %v", second.String(), err)
	}
	if !secondEnvelope.Replayed {
		t.Fatal("second RunDefinitionCreate() with the identical idempotency key reported Replayed = false, want true")
	}
	if secondEnvelope.Result.DefinitionID != "blk-1" {
		t.Fatalf("replay result = %+v, want the exact original DefinitionID", secondEnvelope.Result)
	}

	// A second, genuinely distinct create attempt for the SAME
	// DefinitionID (different idempotency key) must fail rather than
	// silently succeed a second time — proving the first call really did
	// persist (fake.DefinitionsRepository.CreateDefinition itself rejects
	// a reused id).
	var third bytes.Buffer
	err := clidefinitions.RunDefinitionCreate(context.Background(), deps, []string{"--kind", "BLOCK", "--idempotency-key", "key-2"}, strings.NewReader(body), &third, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a genuinely new command reusing the same DefinitionID succeeded, want an error (definition already exists)")
	}
}

func TestRunDefinitionCreate_ProjectScoped(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{"definitionId":"blk-1","name":"widget"}`)

	if err := clidefinitions.RunDefinitionCreate(context.Background(), deps, []string{"--kind", "BLOCK", "--project-id", "proj-a"}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("RunDefinitionCreate() error = %v, stderr = %s", err, stderr.String())
	}

	// Visible via its own project's `show`, not via global `show`.
	var showOut bytes.Buffer
	if err := clidefinitions.RunDefinitionShow(context.Background(), deps, []string{"--kind", "BLOCK", "--project-id", "proj-a", "blk-1"}, &showOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunDefinitionShow(own project) error = %v", err)
	}
	var globalOut bytes.Buffer
	err := clidefinitions.RunDefinitionShow(context.Background(), deps, []string{"--kind", "BLOCK", "blk-1"}, &globalOut, &bytes.Buffer{})
	if err == nil {
		t.Fatal("RunDefinitionShow(global) for a project-scoped definition succeeded, want ErrDefinitionNotFound")
	}
}

func TestRunDefinitionCreate_MissingKind_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionCreate(context.Background(), deps, nil, strings.NewReader(`{"definitionId":"x","name":"y"}`), &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunDefinitionCreate(no --kind) error = %v, want a cli.UsageError", err)
	}
}

func TestRunDefinitionCreate_EmptyBodyFields_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionCreate(context.Background(), deps, []string{"--kind", "BLOCK"}, strings.NewReader(`{}`), &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunDefinitionCreate(empty body) error = %v, want a cli.UsageError", err)
	}
}
