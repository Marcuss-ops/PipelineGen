// Package asset — bcp47_test.go: pin the canonical BCP-47 normalization
// rules + supported-languages whitelist (PR-PY-CLIPS-CORRETTE-TRADOTTE
// Fase 1.b, July 2026).
//
// The test file is the SSOT for the rejection rules: any change to
// `bcp47.Normalize` that would accept a previously-rejected input
// MUST be reflected here + the SSOT comment in bcp47.go.
package asset

import (
	"errors"
	"strings"
	"testing"
)

// TestNormalize_AcceptsCanonicalAndVariants covers the well-formed input
// surface. Each case asserts the canonical BCP-47 output shape
// (lowercase language + uppercase region + hyphen separator).
func TestNormalize_AcceptsCanonicalAndVariants(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare-en", "en", "en"},
		{"bare-en-lowercase", "en", "en"},
		{"bare-en-uppercase", "EN", "en"},
		{"en-US-canonical", "en-US", "en-US"},
		{"en-US-lowercase", "en-us", "en-US"},
		{"en-US-uppercase", "EN-US", "en-US"},
		{"en-US-mixed", "En-Us", "en-US"},
		{"en-GB-canonical", "en-GB", "en-GB"},
		{"pt-BR", "pt-BR", "pt-BR"},
		{"pt-BR-uppercase", "PT-BR", "pt-BR"},
		{"it-trim", "  it  ", "it"},
		{"it-IT-canonical", "it-IT", "it-IT"},
		{"es-ES", "es-ES", "es-ES"},
		{"fr-CA", "fr-CA", "fr-CA"},
		{"de-CH", "de-CH", "de-CH"},
		{"bare-it", "it", "it"},
		{"bare-es", "es", "es"},
		{"bare-fr", "fr", "fr"},
		{"bare-de", "de", "de"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Normalize(c.in)
			if err != nil {
				t.Fatalf("Normalize(%q) error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestNormalize_RejectsMalformed pins the rejection surface. Any
// change to accept a previously-rejected input is a deliberate
// architectural change that must update the SSOT comment in bcp47.go.
func TestNormalize_RejectsMalformed(t *testing.T) {
	// "pt_br" intentionally NOT here — 2+_+2 IS a valid BCP-47 pair (see TestNormalize_AcceptsCanonicalAndVariants {pt-BR-lowercase-underscore}); the Rejects list must stay consistent with the Accepts list.
	cases := []string{
		"portuguese",     // full language name
		"english",        // full language name
		"italian",        // full language name
		"POR",            // 3-letter ISO 639-2 code
		"eng",            // 3-letter ISO 639-2 code
		"ita",            // 3-letter ISO 639-2 code
		"en-USA",         // 3-letter region
		"en_usa",         // 3-letter region
		"es-ESP",         // 3-letter region
		"zh-Hans",        // 4-letter script subtag
		"en-US-CA",       // 3-part language+region+region
		"en_US.UTF-8",    // CLDR locale suffix
		"en_US_POSIX",    // CLDR locale suffix
		"123",            // digits only
		"en-1",           // digit region
		"x",              // 1-letter
		"xyz",            // 3-letter bare
		"en us",          // whitespace inside
		"english-US",     // full language name + region
		"  portuguese  ", // full name with whitespace
		"pt-BR-Maringá",  // 3-part with city
		"en_US",          // PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b: underscore separator rejected (BCP-47 strictly uses hyphen)
		"pt_BR",          // PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b: underscore separator rejected
		"pt_br",          // PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b: underscore separator rejected
		"en_us",          // PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b: underscore separator rejected
		"it_IT",          // PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b: underscore separator rejected
		"es_ES",          // PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b: underscore separator rejected
	}
	for _, in := range cases {
		t.Run("reject_"+in, func(t *testing.T) {
			_, err := Normalize(in)
			if err == nil {
				t.Errorf("Normalize(%q) MUST return an error but returned nil", in)
			}
		})
	}
}

// TestNormalize_EmptyCollapsesToUndetermined pins the godlike/07
// no-fake-availability invariant: empty input MUST collapse to
// BCP-47 "und", NOT silently default to "en".
func TestNormalize_EmptyCollapsesToUndetermined(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"\t",
		"\n",
		"\t\n  ",
	}
	for _, in := range cases {
		t.Run("empty_"+in, func(t *testing.T) {
			got, err := Normalize(in)
			if err != nil {
				t.Fatalf("Normalize(%q) must NOT error on empty input; got %v", in, err)
			}
			if got != "und" {
				t.Errorf("Normalize(%q) = %q, want \"und\" (BCP-47 undetermined)", in, got)
			}
		})
	}
}

// TestIsSupported_PinsWhitelist verifies the canonical whitelist
// membership. Any change to SupportedLanguages is a deliberate
// architectural decision.
func TestIsSupported_PinsWhitelist(t *testing.T) {
	supported := []string{"it", "en", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}
	for _, code := range supported {
		if !IsSupported(code) {
			t.Errorf("IsSupported(%q) = false, want true (canonical whitelist)", code)
		}
	}
	unsupported := []string{
		"",
		"und",
		"en-US",
		"ja",      // not in whitelist
		"zh",      // not in whitelist
		"ko",      // not in whitelist
		"en-XX",   // unsupported region
		"pt-XX",   // unsupported region
		"es-XX",   // unsupported region
		"english", // full name
	}
	for _, code := range unsupported {
		if IsSupported(code) {
			t.Errorf("IsSupported(%q) = true, want false (not in whitelist)", code)
		}
	}
}

// TestSupportedLanguages_HasMinimumCoverage pins the user-spec (Fase 1.b):
// the canonical whitelist MUST include it, en, pl, ru, de, es, pt-BR,
// fr, tr, id at minimum. Adding languages is allowed; REMOVING from
// this list is a deliberate architectural change.
func TestSupportedLanguages_HasMinimumCoverage(t *testing.T) {
	required := []string{"it", "en", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}
	for _, req := range required {
		found := false
		for _, s := range SupportedLanguages {
			if s == req {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SupportedLanguages MUST contain %q (user-spec Fase 1.b minimum); got %v", req, SupportedLanguages)
		}
	}
}

// TestErrLanguageUndeterminable_StableMessageFormat pins the error
// message format. Operator dashboards grep on the "language
// undeterminable" prefix; a drift here breaks dashboards.
func TestErrLanguageUndeterminable_StableMessageFormat(t *testing.T) {
	err := &ErrLanguageUndeterminable{
		AssetID: "yt_test_001_10_30_v1",
		Reason:  "all 5 chain levels exhausted without surfacing a language",
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "language undeterminable:") {
		t.Errorf("error message must start with 'language undeterminable:'; got %q", msg)
	}
	if !strings.Contains(msg, "asset=yt_test_001_10_30_v1") {
		t.Errorf("error message must contain assetID; got %q", msg)
	}
	if !strings.Contains(msg, "reason=all 5 chain levels exhausted without surfacing a language") {
		t.Errorf("error message must contain reason; got %q", msg)
	}
}

// TestIsLanguageUndeterminable_Probe pins the canonical probe.
func TestIsLanguageUndeterminable_Probe(t *testing.T) {
	typed := &ErrLanguageUndeterminable{AssetID: "x", Reason: "y"}
	if !IsLanguageUndeterminable(typed) {
		t.Error("IsLanguageUndeterminable(typed) = false, want true")
	}
	wrapped := errorsWrap(typed, "wrapping context")
	if !IsLanguageUndeterminable(wrapped) {
		t.Error("IsLanguageUndeterminable(wrapped) = false, want true (errors.As probe)")
	}
	other := errors.New("some other error")
	if IsLanguageUndeterminable(other) {
		t.Error("IsLanguageUndeterminable(other) = true, want false")
	}
	if IsLanguageUndeterminable(nil) {
		t.Error("IsLanguageUndeterminable(nil) = true, want false")
	}
}

// errorsWrap is a thin helper to keep the test file self-contained.
func errorsWrap(err error, msg string) error {
	return errorsWrapImpl{err: err, msg: msg}
}

type errorsWrapImpl struct {
	err error
	msg string
}

func (e errorsWrapImpl) Error() string { return e.msg + ": " + e.err.Error() }
func (e errorsWrapImpl) Unwrap() error { return e.err }
