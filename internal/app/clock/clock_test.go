package clock

import (
	"testing"
	"time"
)

func TestSystemReturnsUTC(t *testing.T) {
	got := System{}.Now()
	if got.Location() != time.UTC {
		t.Fatalf("System.Now() location = %v, want UTC", got.Location())
	}
}

func TestSystemNeverZero(t *testing.T) {
	now := System{}.Now()
	if now.IsZero() {
		t.Fatal("System.Now() must never be the zero time")
	}
}

func TestFixedNormalizesToUTC(t *testing.T) {
	local := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("TEST", 3600))
	f := NewFixed(local)
	got := f.Now()
	if got.Location() != time.UTC {
		t.Fatalf("Fixed.Now() location = %v, want UTC", got.Location())
	}
	if !got.Equal(local) {
		t.Fatalf("Fixed.Now() = %v, want the same instant as %v", got, local)
	}
}

func TestFixedIsStableUntilAdvanced(t *testing.T) {
	f := NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	first := f.Now()
	second := f.Now()
	if !first.Equal(second) {
		t.Fatalf("Fixed.Now() should not change between calls without Advance: %v != %v", first, second)
	}
}

func TestFixedAdvance(t *testing.T) {
	f := NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	got := f.Advance(time.Hour)
	want := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Advance result = %v, want %v", got, want)
	}
	if !f.Now().Equal(want) {
		t.Fatalf("Now() after Advance = %v, want %v", f.Now(), want)
	}
}

func TestFixedAdvanceRejectsNegativeDuration(t *testing.T) {
	f := NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Advance with a negative duration should panic")
		}
	}()
	f.Advance(-time.Hour)
}
