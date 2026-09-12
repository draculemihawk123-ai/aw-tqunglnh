package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func validDescriptor() httpapi.RouteDescriptor {
	return httpapi.RouteDescriptor{
		Method:         http.MethodGet,
		Path:           "/widgets",
		OperationID:    "listWidgets",
		ScopeKind:      httpapi.ScopeProject,
		RequestSchema:  struct{}{},
		ResponseSchema: struct{}{},
		Handler:        func(w http.ResponseWriter, r *http.Request) {},
	}
}

func TestRouteRegistry_Register_DuplicateMethodAndPathPanics(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	registry.Register(validDescriptor())

	defer func() {
		if recover() == nil {
			t.Fatal("Register with a duplicate (Method, Path) should panic")
		}
	}()
	registry.Register(validDescriptor())
}

func TestRouteRegistry_Register_SamePathDifferentMethodDoesNotPanic(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	registry.Register(validDescriptor())

	d := validDescriptor()
	d.Method = http.MethodPost
	d.OperationID = "createWidget"
	registry.Register(d) // must not panic
}

func TestRouteRegistry_Register_MissingRequiredFieldPanics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*httpapi.RouteDescriptor)
	}{
		{"missing Method", func(d *httpapi.RouteDescriptor) { d.Method = "" }},
		{"missing Path", func(d *httpapi.RouteDescriptor) { d.Path = "" }},
		{"missing OperationID", func(d *httpapi.RouteDescriptor) { d.OperationID = "" }},
		{"missing ScopeKind", func(d *httpapi.RouteDescriptor) { d.ScopeKind = "" }},
		{"missing RequestSchema", func(d *httpapi.RouteDescriptor) { d.RequestSchema = nil }},
		{"missing ResponseSchema", func(d *httpapi.RouteDescriptor) { d.ResponseSchema = nil }},
		{"missing Handler", func(d *httpapi.RouteDescriptor) { d.Handler = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDescriptor()
			tt.mutate(&d)
			registry := httpapi.NewRouteRegistry()
			defer func() {
				if recover() == nil {
					t.Fatalf("Register with %s should panic", tt.name)
				}
			}()
			registry.Register(d)
		})
	}
}

func TestRouteRegistry_Descriptors_ReturnsInRegistrationOrder(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	first := validDescriptor()
	second := validDescriptor()
	second.Method = http.MethodPost
	second.OperationID = "createWidget"
	registry.Register(first)
	registry.Register(second)

	got := registry.Descriptors()
	if len(got) != 2 {
		t.Fatalf("Descriptors() returned %d entries, want 2", len(got))
	}
	if got[0].OperationID != "listWidgets" || got[1].OperationID != "createWidget" {
		t.Fatalf("Descriptors() = %+v, want registration order preserved", got)
	}
}
