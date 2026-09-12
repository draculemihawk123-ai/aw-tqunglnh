package httpapi_test

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

type widget struct {
	Name string `json:"name"`
}

func TestDecodeJSON_ValidBodyDecodes(t *testing.T) {
	req := httptest.NewRequest("POST", "/widgets", strings.NewReader(`{"name":"gizmo"}`))
	var dst widget
	if err := httpapi.DecodeJSON(req, 1<<20, &dst); err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if dst.Name != "gizmo" {
		t.Fatalf("dst.Name = %q, want gizmo", dst.Name)
	}
}

func TestDecodeJSON_MalformedSyntaxReturnsTypedError(t *testing.T) {
	req := httptest.NewRequest("POST", "/widgets", strings.NewReader(`{"name":`))
	var dst widget
	err := httpapi.DecodeJSON(req, 1<<20, &dst)
	if !errors.Is(err, httpapi.ErrMalformedJSON) {
		t.Fatalf("err = %v, want ErrMalformedJSON", err)
	}
}

func TestDecodeJSON_UnknownFieldReturnsTypedError(t *testing.T) {
	req := httptest.NewRequest("POST", "/widgets", strings.NewReader(`{"name":"gizmo","extra":true}`))
	var dst widget
	err := httpapi.DecodeJSON(req, 1<<20, &dst)
	if !errors.Is(err, httpapi.ErrMalformedJSON) {
		t.Fatalf("err = %v, want ErrMalformedJSON for unknown field", err)
	}
}

func TestDecodeJSON_TrailingDataReturnsTypedError(t *testing.T) {
	req := httptest.NewRequest("POST", "/widgets", strings.NewReader(`{"name":"gizmo"}{"name":"second"}`))
	var dst widget
	err := httpapi.DecodeJSON(req, 1<<20, &dst)
	if !errors.Is(err, httpapi.ErrMalformedJSON) {
		t.Fatalf("err = %v, want ErrMalformedJSON for trailing JSON value", err)
	}
}

func TestDecodeJSON_OversizedBodyReturnsTypedError(t *testing.T) {
	body := `{"name":"` + strings.Repeat("x", 1000) + `"}`
	req := httptest.NewRequest("POST", "/widgets", strings.NewReader(body))
	var dst widget
	err := httpapi.DecodeJSON(req, 16, &dst) // limit far smaller than body
	if !errors.Is(err, httpapi.ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
}
