package definition_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func TestKind_Valid(t *testing.T) {
	valid := []definition.Kind{
		definition.KindWorkflow, definition.KindBlock, definition.KindSkill, definition.KindLayer,
		definition.KindEngineeringPack, definition.KindAgentProfile, definition.KindCommand,
		definition.KindGate, definition.KindPolicy,
	}
	if len(valid) != 9 {
		t.Fatalf("test fixture lists %d kinds, want all 9 DefinitionKinds", len(valid))
	}
	for _, k := range valid {
		if !k.Valid() {
			t.Errorf("Kind(%q).Valid() = false, want true", k)
		}
	}
	if definition.Kind("NOT_A_REAL_KIND").Valid() {
		t.Error("an unknown kind reported Valid() = true")
	}
}

func TestCanTransition_LegalEdges(t *testing.T) {
	legal := []struct{ from, to definition.Status }{
		{definition.StatusDraft, definition.StatusActive},
		{definition.StatusDraft, definition.StatusArchived},
		{definition.StatusActive, definition.StatusArchived},
	}
	for _, edge := range legal {
		if err := definition.CanTransition(edge.from, edge.to); err != nil {
			t.Errorf("CanTransition(%s, %s) = %v, want nil", edge.from, edge.to, err)
		}
	}
}

func TestCanTransition_IllegalEdges(t *testing.T) {
	illegal := []struct{ from, to definition.Status }{
		{definition.StatusArchived, definition.StatusActive}, // terminal, no way back
		{definition.StatusArchived, definition.StatusDraft},
		{definition.StatusActive, definition.StatusDraft}, // no going backward
		{definition.StatusDraft, definition.StatusDraft},  // no-op is not a transition
		{definition.StatusActive, definition.StatusActive},
		{definition.StatusArchived, definition.StatusArchived},
	}
	for _, edge := range illegal {
		err := definition.CanTransition(edge.from, edge.to)
		if !errors.Is(err, definition.ErrIllegalTransition) {
			t.Errorf("CanTransition(%s, %s) = %v, want ErrIllegalTransition", edge.from, edge.to, err)
		}
	}
}

func TestCanTransition_UnknownStatus(t *testing.T) {
	err := definition.CanTransition(definition.Status("BOGUS"), definition.StatusActive)
	if !errors.Is(err, definition.ErrIllegalTransition) {
		t.Fatalf("CanTransition(unknown, ACTIVE) = %v, want ErrIllegalTransition", err)
	}
}

func TestCanPublish_DraftAndActiveAllowed(t *testing.T) {
	if err := definition.CanPublish(definition.StatusDraft); err != nil {
		t.Errorf("CanPublish(DRAFT) = %v, want nil", err)
	}
	if err := definition.CanPublish(definition.StatusActive); err != nil {
		t.Errorf("CanPublish(ACTIVE) = %v, want nil", err)
	}
}

func TestCanPublish_ArchivedRejected(t *testing.T) {
	if err := definition.CanPublish(definition.StatusArchived); err == nil {
		t.Fatal("CanPublish(ARCHIVED) = nil, want an error")
	}
}

func TestScope_GlobalAndProject(t *testing.T) {
	global := definition.GlobalScope()
	if !global.IsGlobal() {
		t.Fatal("GlobalScope().IsGlobal() = false, want true")
	}
	projectScope := definition.ProjectScope(project.ProjectID("proj-1"))
	if projectScope.IsGlobal() {
		t.Fatal("ProjectScope(...).IsGlobal() = true, want false")
	}
	if projectScope.ProjectID == nil || *projectScope.ProjectID != "proj-1" {
		t.Fatalf("ProjectScope(...).ProjectID = %v, want proj-1", projectScope.ProjectID)
	}
}

func TestCreate_StartsAtDraftGenerationOne(t *testing.T) {
	fields, err := definition.Create(definition.CreateRequest{
		Kind: definition.KindBlock, Scope: definition.GlobalScope(), Name: "my-block",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if fields.Status != definition.StatusDraft {
		t.Errorf("Create().Status = %s, want DRAFT", fields.Status)
	}
	if fields.Version != 1 {
		t.Errorf("Create().Version = %d, want 1", fields.Version)
	}
	if fields.Kind != definition.KindBlock || fields.Name != "my-block" {
		t.Errorf("Create() = %+v, want Kind=BLOCK Name=my-block", fields)
	}
}

func TestCreate_RejectsUnknownKind(t *testing.T) {
	_, err := definition.Create(definition.CreateRequest{Kind: "NOT_REAL", Name: "x"})
	if err == nil {
		t.Fatal("Create with an unknown kind should fail")
	}
}

func TestCreate_RejectsEmptyName(t *testing.T) {
	_, err := definition.Create(definition.CreateRequest{Kind: definition.KindBlock, Name: "   "})
	if err == nil {
		t.Fatal("Create with a blank name should fail")
	}
}

func TestArchive_FromDraftAndActiveSucceed(t *testing.T) {
	for _, start := range []definition.Status{definition.StatusDraft, definition.StatusActive} {
		fields := definition.Fields{Kind: definition.KindSkill, Status: start, Version: 3}
		archived, err := definition.Archive(fields)
		if err != nil {
			t.Fatalf("Archive from %s: %v", start, err)
		}
		if archived.Status != definition.StatusArchived {
			t.Errorf("Archive from %s produced status %s, want ARCHIVED", start, archived.Status)
		}
		if archived.Version != 4 {
			t.Errorf("Archive from %s produced version %d, want 4 (incremented)", start, archived.Version)
		}
	}
}

func TestArchive_AlreadyArchived_Rejected(t *testing.T) {
	fields := definition.Fields{Kind: definition.KindSkill, Status: definition.StatusArchived, Version: 5}
	_, err := definition.Archive(fields)
	if !errors.Is(err, definition.ErrIllegalTransition) {
		t.Fatalf("Archive(already ARCHIVED) = %v, want ErrIllegalTransition", err)
	}
}

func TestActivate_FromDraftSucceeds(t *testing.T) {
	fields := definition.Fields{Kind: definition.KindGate, Status: definition.StatusDraft, Version: 1}
	activated, err := definition.Activate(fields)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if activated.Status != definition.StatusActive || activated.Version != 2 {
		t.Fatalf("Activate() = %+v, want Status=ACTIVE Version=2", activated)
	}
}

func TestActivate_FromArchived_Rejected(t *testing.T) {
	fields := definition.Fields{Kind: definition.KindGate, Status: definition.StatusArchived, Version: 5}
	_, err := definition.Activate(fields)
	if !errors.Is(err, definition.ErrIllegalTransition) {
		t.Fatalf("Activate(ARCHIVED) = %v, want ErrIllegalTransition", err)
	}
}

func validVersionFieldsRequest(now time.Time) definition.NewVersionFieldsRequest {
	return definition.NewVersionFieldsRequest{
		ID: "ver-1", DefinitionID: "def-1", Kind: definition.KindWorkflow,
		VersionNumber: 3, SchemaVersion: 1,
		CanonicalSource: `{"nodes":[]}`, SourceHash: "sha256:source",
		CompiledSnapshot: `{"nodes":[],"dependencies":[]}`, CompiledHash: "sha256:compiled",
		PublishedBy: "operator-1", PublishedAt: now,
	}
}

func TestNewVersionFields_ValidAndAccessors(t *testing.T) {
	now := time.Now().UTC()
	v, err := definition.NewVersionFields(validVersionFieldsRequest(now))
	if err != nil {
		t.Fatalf("NewVersionFields: %v", err)
	}
	if v.ID() != "ver-1" || v.DefinitionID() != "def-1" || v.Kind() != definition.KindWorkflow ||
		v.VersionNumber() != 3 || v.SchemaVersion() != 1 ||
		v.CanonicalSource() != `{"nodes":[]}` || v.SourceHash() != "sha256:source" ||
		v.CompiledSnapshot() != `{"nodes":[],"dependencies":[]}` || v.CompiledHash() != "sha256:compiled" ||
		v.PublishedBy() != "operator-1" || !v.PublishedAt().Equal(now) {
		t.Fatalf("NewVersionFields accessors = %+v, want matching constructor args", v)
	}
}

func TestNewVersionFields_RejectsMissingFields(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name   string
		mutate func(req definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest
	}{
		{"empty id", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest { r.ID = ""; return r }},
		{"empty definitionID", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.DefinitionID = ""
			return r
		}},
		{"unknown kind", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.Kind = "NOT_REAL"
			return r
		}},
		{"zero version number", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.VersionNumber = 0
			return r
		}},
		{"zero schema version", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.SchemaVersion = 0
			return r
		}},
		{"empty canonical source", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.CanonicalSource = ""
			return r
		}},
		{"empty source hash", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.SourceHash = ""
			return r
		}},
		{"empty compiled snapshot", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.CompiledSnapshot = ""
			return r
		}},
		{"empty compiled hash", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.CompiledHash = ""
			return r
		}},
		{"empty publishedBy", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.PublishedBy = ""
			return r
		}},
		{"zero publishedAt", func(r definition.NewVersionFieldsRequest) definition.NewVersionFieldsRequest {
			r.PublishedAt = time.Time{}
			return r
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := definition.NewVersionFields(c.mutate(validVersionFieldsRequest(now)))
			if err == nil {
				t.Fatalf("NewVersionFields(%s) should have failed", c.name)
			}
		})
	}
}

func TestNewVersionFields_ClonesDependencyManifest(t *testing.T) {
	now := time.Now().UTC()
	req := validVersionFieldsRequest(now)
	req.Dependencies = definition.DependencyManifest{
		Pins: []definition.DependencyPin{{Kind: definition.KindBlock, DefinitionID: "def-2", VersionID: "ver-2"}},
	}
	v, err := definition.NewVersionFields(req)
	if err != nil {
		t.Fatalf("NewVersionFields: %v", err)
	}
	req.Dependencies.Pins[0].VersionID = "mutated"
	if v.Dependencies().Pins[0].VersionID != "ver-2" {
		t.Fatal("VersionFields.Dependencies() was affected by mutating the caller's original manifest — NewVersionFields must clone it")
	}
	v.Dependencies().Pins[0].VersionID = "also-mutated"
	if v.Dependencies().Pins[0].VersionID != "ver-2" {
		t.Fatal("mutating a Dependencies() result affected VersionFields' own state — Dependencies() must return a clone")
	}
}

// TestVersionFields_NoExportedMutationMethod is V2-01's own "không có
// update/delete public trên Version" design constraint, checked the same
// way internal/archtest checks structural invariants elsewhere in this
// codebase: reflect over VersionFields' method set and fail if anything
// beyond the known read-only accessors is exported.
func TestVersionFields_NoExportedMutationMethod(t *testing.T) {
	allowed := map[string]bool{
		"ID": true, "DefinitionID": true, "Kind": true,
		"VersionNumber": true, "SchemaVersion": true,
		"CanonicalSource": true, "SourceHash": true,
		"CompiledSnapshot": true, "CompiledHash": true, "Dependencies": true,
		"PublishedBy": true, "PublishedAt": true,
	}
	typ := reflect.TypeOf(definition.VersionFields{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !allowed[name] {
			t.Errorf("VersionFields has unexpected exported method %q — Version must expose only read-only accessors", name)
		}
	}
}
