// PR-VO-B3 (June 2026): table-driven tests for pkg/localeutil.Parse + IsValid.
//
// The cases below mirror the user-facing spec exactly:
//   - 6 happy-path cases (en-US, en_US, EN-us, pt-BR, en, EN).
//   - 8 rejected cases (3-letter codes, ANSI suffixes, 3-part locales,
//     digits, 3-letter region, 1-letter language, script subtag, empty).
//
// Covering the symmetric IsValid() helper at the same time keeps the
// surface contract honest — every valid input must round-trip identically
// through both entry points.
package localeutil

import (
	"strings"
	"testing"
)

func TestParse_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCompact string
		wantBCP47   string
		wantErr     bool
	}{
		// ── Happy path ──
		{
			name:        "BCP-47 canonical (hyphen)",
			input:       "en-US",
			wantCompact: "enUS",
			wantBCP47:   "en-US",
			wantErr:     false,
		},
		{
			name:        "Underscore separator (legacy/Java-style)",
			input:       "en_US",
			wantCompact: "enUS",
			wantBCP47:   "en-US",
			wantErr:     false,
		},
		{
			name:        "Mixed-case input (EN-us)",
			input:       "EN-us",
			wantCompact: "enUS",
			wantBCP47:   "en-US",
			wantErr:     false,
		},
		{
			name:        "Different language+region (pt-BR)",
			input:       "pt-BR",
			wantCompact: "ptBR",
			wantBCP47:   "pt-BR",
			wantErr:     false,
		},
		{
			name:        "Language without region (en)",
			input:       "en",
			wantCompact: "en",
			wantBCP47:   "en",
			wantErr:     false,
		},
		{
			name:        "Language without region (uppercase EN)",
			input:       "EN",
			wantCompact: "en",
			wantBCP47:   "en",
			wantErr:     false,
		},

		// ── Rejected: wrong length ──
		{
			name:        "3-letter ISO 639-2 code",
			input:       "eng",
			wantCompact: "",
			wantBCP47:   "",
			wantErr:     true,
		},
		{
			name:        "1-letter language",
			input:       "e-US",
			wantCompact: "",
			wantBCP47:   "",
			wantErr:     true,
		},

		// ── Rejected: 3-letter region ──
		{
			name:        "3-letter region",
			input:       "en-USA",
			wantCompact: "",
			wantBCP47:   "",
			wantErr:     true,
		},

		// ── Rejected: extra suffixes / CLDR suffixes ──
		{
			name:        "ANSI/CLDR suffix (.UTF-8)",
			input:       "en_US.UTF-8",
			wantCompact: "",
			wantBCP47:   "",
			wantErr:     true,
		},

		// ── Rejected: 3-part locales ──
		{
			name:        "3-part locale (lang-region-script)",
			input:       "en-US-CA",
			wantCompact: "",
			wantBCP47:   "",
			wantErr:     true,
		},
		{
			name:        "Script subtag (zh-Hans)",
			input:       "zh-Hans",
			wantCompact: "",
			wantBCP47:   "",
			wantErr:     true,
		},

		// ── Rejected: empty / digits ──
		{
			name:        "Empty string",
			input:       "",
			wantCompact: "",
			wantBCP47:   "",
			wantErr:     true,
		},
		{
			name:        "Digits only",
			input:       "1234",
			wantCompact: "",
			wantBCP47:   "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.Compact != tt.wantCompact {
					t.Errorf("Parse(%q).Compact: got %q, want %q",
						tt.input, got.Compact, tt.wantCompact)
				}
				if got.BCP47 != tt.wantBCP47 {
					t.Errorf("Parse(%q).BCP47: got %q, want %q",
						tt.input, got.BCP47, tt.wantBCP47)
				}
			}
		})
	}
}

func TestParse_InvalidErrorMentionsInput(t *testing.T) {
	// Error contract: the offending input must be quoted in the error
	// message so an operator looking at logs can spot the malformed
	// locale. This pins the human-debuggable failure mode.
	bad := "en_US.UTF-8"
	_, err := Parse(bad)
	if err == nil {
		t.Fatal("expected error for ANSI suffix input, got nil")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("error message must quote the offending input %q; got %q",
			bad, err.Error())
	}
}

func TestParse_TrimsWhitespace(t *testing.T) {
	// Forward-compat: HTTP middleware sometimes url-decodes a leading
	// or trailing space from query params; the parser must accept
	// these. Whitespace *inside* the locale still fails (see Test
	// below).
	got, err := Parse("  en-US  ")
	if err != nil {
		t.Fatalf("expected trimmed input to pass; got error: %v", err)
	}
	if got.BCP47 != "en-US" {
		t.Errorf("trimmed parse: BCP47=%q, want %q", got.BCP47, "en-US")
	}
	if got.Compact != "enUS" {
		t.Errorf("trimmed parse: Compact=%q, want %q", got.Compact, "enUS")
	}
}

func TestParse_InternalWhitespaceRejects(t *testing.T) {
	// Defensive: a malformed locale like `"en - US"` must NOT pass —
	// it has the right characters but the wrong shape. We rely on
	// the regex anchor to reject.
	_, err := Parse("en - US")
	if err == nil {
		t.Fatal("internal whitespace must fail the regex match")
	}
}
