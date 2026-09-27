package apicontract

import "testing"

// EmbeddedResultFixture mirrors runtime.StartWorkflowRunResult's own shape
// closely enough to exercise describeSchema's anonymous-field flattening in
// isolation, without depending on the real, much larger runtime package.
// Exported (like every real embedded Result type this fix targets) — an
// embedded field's own reflect.StructField.PkgPath follows its TYPE NAME's
// exportedness, not its fields', so an unexported fixture type here would
// make every field below look unexported and get skipped entirely,
// silently passing for the wrong reason.
type EmbeddedResultFixture struct {
	RunID string `json:"runId"`
	State string `json:"state"`
}

// FixtureResponseWithEmbedding mirrors internal/delivery/httpapi/run's own
// StartRunResponse exactly: an untagged anonymous struct field plus one
// ordinary sibling field.
type FixtureResponseWithEmbedding struct {
	EmbeddedResultFixture
	ValidActions []string `json:"validActions"`
}

// TaggedEmbedFixture proves an anonymous field that DOES carry a json tag
// is left as an ordinary named field, never flattened — a deliberate
// author decision encoding/json itself respects.
type TaggedEmbedFixture struct {
	EmbeddedResultFixture `json:"embedded"`
}

func TestDescribeSchema_FlattensUntaggedAnonymousStructField(t *testing.T) {
	ref := describeSchema(FixtureResponseWithEmbedding{})

	names := make(map[string]Field, len(ref.Fields))
	for _, f := range ref.Fields {
		names[f.JSONTag] = f
	}
	if len(ref.Fields) != 3 {
		t.Fatalf("got %d fields, want 3 (runId, state, validActions) — flattened: %+v", len(ref.Fields), ref.Fields)
	}
	for _, wantTag := range []string{"runId", "state", "validActions"} {
		if _, ok := names[wantTag]; !ok {
			t.Errorf("missing flattened field with json tag %q: %+v", wantTag, ref.Fields)
		}
	}
	for _, f := range ref.Fields {
		if f.Name == "EmbeddedResultFixture" {
			t.Fatalf("embedded struct field was NOT flattened — still present as its own field: %+v", f)
		}
	}
}

func TestDescribeSchema_LeavesATaggedAnonymousFieldAsOrdinaryNamedField(t *testing.T) {
	ref := describeSchema(TaggedEmbedFixture{})

	if len(ref.Fields) != 1 {
		t.Fatalf("got %d fields, want exactly 1 (the tagged embed, left un-flattened): %+v", len(ref.Fields), ref.Fields)
	}
	if ref.Fields[0].JSONTag != "embedded" {
		t.Errorf("JSONTag = %q, want %q (a tagged anonymous field must never be flattened)", ref.Fields[0].JSONTag, "embedded")
	}
}
