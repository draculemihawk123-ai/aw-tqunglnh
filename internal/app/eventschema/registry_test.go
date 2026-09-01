package eventschema_test

import (
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

func decodeEcho(payloadJSON string) (any, error) { return payloadJSON, nil }

func TestRegistry_IsRegistered(t *testing.T) {
	r := eventschema.NewRegistry()
	if r.IsRegistered("Foo", 1) {
		t.Fatal("IsRegistered should be false before any Register call")
	}
	r.Register("Foo", 1, decodeEcho)
	if !r.IsRegistered("Foo", 1) {
		t.Fatal("IsRegistered should be true after Register")
	}
	if r.IsRegistered("Foo", 2) {
		t.Fatal("IsRegistered should not bleed across schema versions")
	}
}

func TestRegistry_Register_DuplicatePanics(t *testing.T) {
	r := eventschema.NewRegistry()
	r.Register("Foo", 1, decodeEcho)
	defer func() {
		if recover() == nil {
			t.Fatal("Register with a duplicate (eventType, schemaVersion) should panic")
		}
	}()
	r.Register("Foo", 1, decodeEcho)
}

func TestRegistry_Decode_NotRegistered_ReturnsErrNotRegistered(t *testing.T) {
	r := eventschema.NewRegistry()
	_, err := r.Decode("Foo", 1, `{}`)
	if !errors.Is(err, eventschema.ErrNotRegistered) {
		t.Fatalf("err = %v, want eventschema.ErrNotRegistered", err)
	}
}

func TestRegistry_Decode_UsesRegisteredDecoder(t *testing.T) {
	r := eventschema.NewRegistry()
	r.Register("Foo", 1, decodeEcho)
	got, err := r.Decode("Foo", 1, `payload`)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != "payload" {
		t.Fatalf("Decode = %v, want %q", got, "payload")
	}
}

func TestRegistry_RegisterUpcaster_WithoutDecoderPanics(t *testing.T) {
	r := eventschema.NewRegistry()
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterUpcaster for a version with no registered decoder should panic")
		}
	}()
	r.RegisterUpcaster("Foo", 1, func(previous any) (any, error) { return previous, nil })
}

func TestRegistry_RegisterUpcaster_DuplicatePanics(t *testing.T) {
	r := eventschema.NewRegistry()
	r.Register("Foo", 1, decodeEcho)
	r.RegisterUpcaster("Foo", 1, func(previous any) (any, error) { return previous, nil })
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterUpcaster with a duplicate (eventType, from) should panic")
		}
	}()
	r.RegisterUpcaster("Foo", 1, func(previous any) (any, error) { return previous, nil })
}

func TestRegistry_DecodeLatest_NoUpcastNeeded(t *testing.T) {
	r := eventschema.NewRegistry()
	r.Register("Foo", 1, decodeEcho)
	got, err := r.DecodeLatest("Foo", 1, `payload`, 1)
	if err != nil {
		t.Fatalf("DecodeLatest: %v", err)
	}
	if got != "payload" {
		t.Fatalf("DecodeLatest = %v, want %q", got, "payload")
	}
}

func TestRegistry_DecodeLatest_ChainsUpcasters(t *testing.T) {
	r := eventschema.NewRegistry()
	r.Register("Foo", 1, decodeEcho)
	r.Register("Foo", 2, decodeEcho)
	r.Register("Foo", 3, decodeEcho)
	r.RegisterUpcaster("Foo", 1, func(previous any) (any, error) { return previous.(string) + "->v2", nil })
	r.RegisterUpcaster("Foo", 2, func(previous any) (any, error) { return previous.(string) + "->v3", nil })

	got, err := r.DecodeLatest("Foo", 1, `v1`, 3)
	if err != nil {
		t.Fatalf("DecodeLatest: %v", err)
	}
	if got != "v1->v2->v3" {
		t.Fatalf("DecodeLatest = %v, want %q", got, "v1->v2->v3")
	}
}

func TestRegistry_DecodeLatest_MissingUpcaster_ReturnsError(t *testing.T) {
	r := eventschema.NewRegistry()
	r.Register("Foo", 1, decodeEcho)
	r.Register("Foo", 2, decodeEcho)
	// No upcaster registered from v1 -> v2.
	_, err := r.DecodeLatest("Foo", 1, `v1`, 2)
	if err == nil {
		t.Fatal("DecodeLatest should fail when a required upcaster is missing")
	}
}
