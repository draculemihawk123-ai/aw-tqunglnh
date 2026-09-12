package httpapi_test

import (
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func TestResolveLimit_EmptyDefaultsToDefaultPageLimit(t *testing.T) {
	got, err := httpapi.ResolveLimit("")
	if err != nil {
		t.Fatalf("ResolveLimit(\"\") error = %v", err)
	}
	if got != httpapi.DefaultPageLimit {
		t.Fatalf("ResolveLimit(\"\") = %d, want DefaultPageLimit (%d)", got, httpapi.DefaultPageLimit)
	}
}

func TestResolveLimit_WithinBoundsReturnsAsIs(t *testing.T) {
	got, err := httpapi.ResolveLimit("25")
	if err != nil {
		t.Fatalf("ResolveLimit(\"25\") error = %v", err)
	}
	if got != 25 {
		t.Fatalf("ResolveLimit(\"25\") = %d, want 25", got)
	}
}

func TestResolveLimit_AboveMaxClampsToMaxPageLimit(t *testing.T) {
	got, err := httpapi.ResolveLimit("100000")
	if err != nil {
		t.Fatalf("ResolveLimit(\"100000\") error = %v", err)
	}
	if got != httpapi.MaxPageLimit {
		t.Fatalf("ResolveLimit(\"100000\") = %d, want MaxPageLimit (%d)", got, httpapi.MaxPageLimit)
	}
}

func TestResolveLimit_ZeroOrNegativeReturnsErrInvalidLimit(t *testing.T) {
	for _, raw := range []string{"0", "-1", "-100"} {
		t.Run(raw, func(t *testing.T) {
			_, err := httpapi.ResolveLimit(raw)
			if !errors.Is(err, httpapi.ErrInvalidLimit) {
				t.Fatalf("ResolveLimit(%q) error = %v, want ErrInvalidLimit", raw, err)
			}
		})
	}
}

func TestResolveLimit_NonNumericReturnsErrInvalidLimit(t *testing.T) {
	for _, raw := range []string{"abc", "1.5", "  ", "10x"} {
		t.Run(raw, func(t *testing.T) {
			_, err := httpapi.ResolveLimit(raw)
			if !errors.Is(err, httpapi.ErrInvalidLimit) {
				t.Fatalf("ResolveLimit(%q) error = %v, want ErrInvalidLimit", raw, err)
			}
		})
	}
}

func TestResolveLimit_ExactlyMaxPageLimitReturnsAsIs(t *testing.T) {
	raw := "200"
	got, err := httpapi.ResolveLimit(raw)
	if err != nil {
		t.Fatalf("ResolveLimit(%q) error = %v", raw, err)
	}
	if got != httpapi.MaxPageLimit {
		t.Fatalf("ResolveLimit(%q) = %d, want MaxPageLimit (%d)", raw, got, httpapi.MaxPageLimit)
	}
}
