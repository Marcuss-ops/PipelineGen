package timeutil

import (
	"testing"
	"time"
)

func TestParseRFC3339(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{"empty", "", time.Time{}},
		{"invalid", "not-a-time", time.Time{}},
		{"valid UTC", "2026-06-09T12:00:00Z", time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)},
		{"valid with offset", "2026-06-09T14:00:00+02:00", time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRFC3339(tc.in)
			if !got.Equal(tc.want) {
				t.Errorf("ParseRFC3339(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRFC3339StringAlias(t *testing.T) {
	if ParseRFC3339String("2026-01-01T00:00:00Z") != ParseRFC3339("2026-01-01T00:00:00Z") {
		t.Error("ParseRFC3339String should be an alias for ParseRFC3339")
	}
}

func TestParseRFC3339PtrString(t *testing.T) {
	empty := ""
	cases := []struct {
		name string
		in   *string
		want *time.Time
	}{
		{"nil pointer", nil, nil},
		{"empty string", &empty, nil},
		{"valid", ptr("2026-06-09T12:00:00Z"), ptrTime(time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC))},
		{"invalid", ptr("not-a-time"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRFC3339PtrString(tc.in)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("nil mismatch: got %v, want %v", got, tc.want)
			}
			if got != nil && tc.want != nil && !got.Equal(*tc.want) {
				t.Errorf("got %v, want %v", *got, *tc.want)
			}
		})
	}
}

func TestFormatRFC3339(t *testing.T) {
	if got := FormatRFC3339(time.Time{}); got != "" {
		t.Errorf("zero time should format to empty string, got %q", got)
	}
	ts := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if got := FormatRFC3339(ts); got != "2026-06-09T12:00:00Z" {
		t.Errorf("got %q, want %q", got, "2026-06-09T12:00:00Z")
	}
}

func TestFormatRFC3339NormalizesToUTC(t *testing.T) {
	tz, _ := time.LoadLocation("Europe/Rome")
	ts := time.Date(2026, 6, 9, 14, 0, 0, 0, tz)
	if got := FormatRFC3339(ts); got != "2026-06-09T12:00:00Z" {
		t.Errorf("expected UTC normalization, got %q", got)
	}
}

func TestFormatPtrRFC3339(t *testing.T) {
	ts := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	tsStr := "2026-06-09T12:00:00Z"

	if got := FormatPtrRFC3339(nil); got != nil {
		t.Errorf("nil pointer should format to nil, got %v", got)
	}
	if got := FormatPtrRFC3339(&ts); got != tsStr {
		t.Errorf("got %v, want %q", got, tsStr)
	}
}

func TestNow(t *testing.T) {
	before := time.Now().UTC()
	got := Now()
	after := time.Now().UTC()
	if got.Location() != time.UTC {
		t.Errorf("Now() should return UTC time, got location %v", got.Location())
	}
	if got.Before(before) || got.After(after) {
		t.Errorf("Now() = %v, expected between %v and %v", got, before, after)
	}
}

// helpers
func ptr(s string) *string           { return &s }
func ptrTime(t time.Time) *time.Time { return &t }
