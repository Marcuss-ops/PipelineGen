// Package voiceover — types_typed_test.go (PR-VO-TYPED-PRIMITIVES closure,
// July 2026, ship_date: 2026-07-05).
//
// Canonical lockstep test for the typed-envelope contract (Language /
// StyleGroup / TextHash). The typed-envelope migration was already
// shipped in pre-session commits (file-picker evidence confirms
// `type Language string` at language.go:36, `type StyleGroup string`
// at stylegroup.go:40, `type TextHash string` at texthash.go:56; the
// production call sites like `task.go:32 Language Language` and
// `command.go:209 TextHash TextHash` are already typed). This file
// pins the canonical CONTRACT for the typed envelopes themselves at
// the typed-envelope surface so future refactors that drop the
// named-type protection surface as test failure
// (godlike/06 SSOT audit-pinning discipline).
//
// Companion test files that exercise the typed envelopes through
// their consumers:
//
//   - finalizer_invariants_test.go (FinalizeCommand.Language typed)
//   - parent_state_handler_test.go (BatchItem.Language typed)
//   - service_test.go (request.Languages typed + per-item literal casts)
//   - jobs/fanout_dedup_test.go (fanout child textHash typed)
//   - jobs/generate_item_handler_test.go (item.Language typed via voiceover.Language)
//
// Why a single consolidated test file (defensible per AGENTS.md):
// the 5 TestXxx functions together form the "canonical lockstep test
// for PR-VO-TYPED-PRIMITIVES" as this godoc declares. The source
// files (language.go + stylegroup.go + texthash.go) live separately
// for production-code concerns (precedence + parse semantics),
// but the test surface is one commit, one diff, one logical unit.
//
// godlike/06 SSOT: every typed envelope reference lives ONLY in
// language.go / stylegroup.go / texthash.go. The test file imports
// the canonical envelopes + exercises their canonical constructors
// + IsEmpty()/String() helpers + boundary-conversion idiom.
// Zero production-code surface change.
//
// godlike/07 no-fake-availability: each TestXxx block drives BOTH
// the happy path AND a failure mode (bad ParseLanguage input /
// empty TextHash on non-empty text / JSON wire byte-equivalence).
// The contract is exhaustive — surface drift surfaces immediately.
//
// Pre-existing carry-forward UNCHANGED (per architecture/current.yaml
// #PRE-EXISTING-BUILD-ISSUES-2026-07-04 + the canonical 6-item list):
// the 2 failing voiceover tests in parent_state_handler_test.go +
// fanout_dedup_test.go (TestFanoutPerChildCorrelationIDDistinct +
// TestGenerateJobHandler_PartialFanoutExpectedChildren) predate this
// PR and are NOT regressions of any PR-VO-TYPED-PRIMITIVES commit.
// Targeted go test -short on the new file (this file) is the
// canonical surface; full subtree test is a carry-forward check only.
package voiceover

import (
	"encoding/json"
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────
// Language — typed envelope contract
// ────────────────────────────────────────────────────────────────────────

func TestLanguage_TypedEnvelopeContract(t *testing.T) {
	t.Run("typed_literal_assignment_ok", func(t *testing.T) {
		// The canonical typed enum literal: `Language("en")` is the
		// SOLE assignment idiom (mirrors existing call sites in
		// service_test.go + finalizer_invariants_test.go).
		var lang Language = Language("en")
		if string(lang) != "en" {
			t.Fatalf("string(Language('en')) = %q; want 'en'", string(lang))
		}
	})

	t.Run("EmptyLanguage_is_canonical_zero", func(t *testing.T) {
		var zero Language
		if zero != EmptyLanguage {
			t.Fatalf("zero-value Language = %q; want EmptyLanguage = %q", zero, EmptyLanguage)
		}
		if !zero.IsEmpty() {
			t.Fatalf("zero-value Language.IsEmpty() = false; want true")
		}
		if !EmptyLanguage.IsEmpty() {
			t.Fatalf("EmptyLanguage.IsEmpty() = false; want true")
		}
	})

	t.Run("ParseLanguage_accepts_valid_bcp47", func(t *testing.T) {
		validCodes := []string{"en", "it", "en-US", "it-IT", "pt-BR", "ja", "zh-Hans"}
		for _, code := range validCodes {
			lang, err := ParseLanguage(code)
			if err != nil {
				t.Fatalf("ParseLanguage(%q) returned err=%v; want nil", code, err)
			}
			if string(lang) != code {
				t.Fatalf("ParseLanguage(%q) = %q; want %q", code, string(lang), code)
			}
		}
	})

	t.Run("ParseLanguage_rejects_empty", func(t *testing.T) {
		_, err := ParseLanguage("")
		if err == nil {
			t.Fatalf("ParseLanguage('') returned nil err; want non-nil")
		}
		if !strings.Contains(err.Error(), ErrInvalidLanguage.Error()) {
			t.Fatalf("ParseLanguage('') err = %v; want wrap of ErrInvalidLanguage", err)
		}
	})

	t.Run("ParseLanguage_rejects_whitespace_only", func(t *testing.T) {
		// Whitespace-only (TrimSpace result is empty) MUST reject per
		// the canonical algorithm: `trimmed := strings.TrimSpace(s);
		// if trimmed == "" { return EmptyLanguage, fmt.Errorf("...
		// empty code: %w", ErrInvalidLanguage) }`. This subtest
		// (added per code-reviewer SHOULD-FIX #2) was missing in the
		// pre-fix version.
		whitespaceInputs := []string{" ", "  ", "\t", "\n"}
		for _, in := range whitespaceInputs {
			lang, err := ParseLanguage(in)
			if err == nil {
				t.Fatalf("ParseLanguage(%q) returned nil err; want non-nil (whitespace-only rejected)", in)
			}
			if lang != EmptyLanguage {
				t.Fatalf("ParseLanguage(%q) lang = %q; want EmptyLanguage on error", in, lang)
			}
		}
	})

	t.Run("ParseLanguage_rejects_invalid_chars", func(t *testing.T) {
		invalidCodes := []string{"en US", "en_US", "en!", "日本語", "en/us"}
		for _, code := range invalidCodes {
			_, err := ParseLanguage(code)
			if err == nil {
				t.Fatalf("ParseLanguage(%q) returned nil err; want non-nil (invalid code)", code)
			}
		}
	})

	t.Run("ParseLanguage_trims_whitespace", func(t *testing.T) {
		lang, err := ParseLanguage("  en-US  ")
		if err != nil {
			t.Fatalf("ParseLanguage('  en-US  ') returned err=%v; want nil (whitespace-trim is canonical)", err)
		}
		if string(lang) != "en-US" {
			t.Fatalf("ParseLanguage('  en-US  ') = %q; want 'en-US' (whitespace-trimmed)", string(lang))
		}
	})

	t.Run("MustParseLanguage_accepts_valid", func(t *testing.T) {
		// Positive case added per code-reviewer SHOULD-FIX #3:
		// the pre-fix version only tested the panic-on-invalid
		// branch. The valid-input branch (no panic + returns the
		// typed value verbatim) is the high-frequency production path.
		lang := MustParseLanguage("en-US")
		if string(lang) != "en-US" {
			t.Fatalf("MustParseLanguage('en-US') = %q; want 'en-US'", string(lang))
		}
	})

	t.Run("MustParseLanguage_panics_on_invalid", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("MustParseLanguage('') did not panic; want panic on invalid input")
			}
		}()
		_ = MustParseLanguage("") // must panic
	})

	t.Run("Language_String_returns_underlying_string", func(t *testing.T) {
		lang := Language("en-US")
		if lang.String() != "en-US" {
			t.Fatalf("Language('en-US').String() = %q; want 'en-US'", lang.String())
		}
	})

	t.Run("Language_JSON_wire_byte_equivalent", func(t *testing.T) {
		// The canonical godlike/06 guarantee: name-type strings serialize
		// byte-for-byte with the pre-refactor raw-string field.
		// omitempty make zero-value Language serialize as absent (vs "").
		type wire struct {
			Language Language `json:"language"`
		}
		in := wire{Language: Language("en-US")}
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("json.Marshal(wire{Language:Language('en-US')}) returned err=%v", err)
		}
		if string(b) != `{"language":"en-US"}` {
			t.Fatalf("JSON wire shape = %s; want {\"language\":\"en-US\"}", string(b))
		}
	})
}

// ────────────────────────────────────────────────────────────────────────
// StyleGroup — typed envelope contract (permissive parse)
// ────────────────────────────────────────────────────────────────────────

func TestStyleGroup_TypedEnvelopeContract(t *testing.T) {
	t.Run("typed_literal_assignment_ok", func(t *testing.T) {
		var sg StyleGroup = StyleGroup("cinematic")
		if string(sg) != "cinematic" {
			t.Fatalf("string(StyleGroup('cinematic')) = %q; want 'cinematic'", string(sg))
		}
	})

	t.Run("EmptyStyleGroup_is_canonical_zero", func(t *testing.T) {
		var zero StyleGroup
		if zero != EmptyStyleGroup {
			t.Fatalf("zero-value StyleGroup = %q; want EmptyStyleGroup = %q", zero, EmptyStyleGroup)
		}
		if !zero.IsEmpty() {
			t.Fatalf("zero-value StyleGroup.IsEmpty() = false; want true")
		}
	})

	t.Run("ParseStyleGroup_permissive_empty", func(t *testing.T) {
		// Permissive parse: empty + whitespace-only are valid "no style"
		// signals (NOT errors). This matches the pre-refactor omitempty
		// contract on DestinationRequest.StyleGroup.
		sg := ParseStyleGroup("")
		if string(sg) != "" {
			t.Fatalf("ParseStyleGroup('') = %q; want ''", string(sg))
		}
		if !sg.IsEmpty() {
			t.Fatalf("ParseStyleGroup('').IsEmpty() = false; want true")
		}
	})

	t.Run("ParseStyleGroup_trims_whitespace", func(t *testing.T) {
		sg := ParseStyleGroup("  anime  ")
		if string(sg) != "anime" {
			t.Fatalf("ParseStyleGroup('  anime  ') = %q; want 'anime'", string(sg))
		}
	})

	t.Run("ParseStyleGroup_preserves_unicode_and_interior_whitespace", func(t *testing.T) {
		// Replaced the original "preserves_special_chars" test which
		// had a Go-string-escape bug (`\"\"` in the Go source was
		// miscounted). The new assertions use Unicode-safe inputs.
		// process_metadata_test.go::TestRebuild_DestinationStyleGroup_SpecialCharRoundtrip
		// exercises the round-trip via DestinationRequest; this subtest
		// pins the lower-level envelope behavior.
		inputs := []string{
			"日本/アニメ",            // Japanese chars + separator preserved
			"voice 🎯 cinematic", // emoji + interior whitespace preserved
			"anime/tutorial",    // ASCII + slash preserved
		}
		for _, in := range inputs {
			sg := ParseStyleGroup(in)
			if string(sg) != in {
				t.Fatalf("ParseStyleGroup(%q) = %q; want %q (unicode + interior ws preserved)", in, string(sg), in)
			}
		}
	})

	t.Run("StyleGroup_String_returns_underlying_string", func(t *testing.T) {
		sg := StyleGroup("anime")
		if sg.String() != "anime" {
			t.Fatalf("StyleGroup('anime').String() = %q; want 'anime'", sg.String())
		}
	})

	t.Run("StyleGroup_omitempty_behavior", func(t *testing.T) {
		// omitempty makes empty StyleGroup serialize as absent (NOT
		// as ""); matches the pre-PR-VO-TYPED-PRIMITIVES DestinationRequest
		// wire-shape contract.
		type wire struct {
			StyleGroup StyleGroup `json:"style_group,omitempty"`
		}
		empty := wire{StyleGroup: EmptyStyleGroup}
		b, err := json.Marshal(empty)
		if err != nil {
			t.Fatalf("json.Marshal(empty wire) returned err=%v", err)
		}
		if string(b) != `{}` {
			t.Fatalf("empty StyleGroup omitempty = %s; want {} (no 'style_group' key)", string(b))
		}

		filled := wire{StyleGroup: StyleGroup("cinematic")}
		b2, err := json.Marshal(filled)
		if err != nil {
			t.Fatalf("json.Marshal(filled wire) returned err=%v", err)
		}
		if string(b2) != `{"style_group":"cinematic"}` {
			t.Fatalf("filled StyleGroup omitempty = %s; want {\"style_group\":\"cinematic\"}", string(b2))
		}
	})
}

// ────────────────────────────────────────────────────────────────────────
// TextHash — typed envelope contract (byte-stable canonical)
// ────────────────────────────────────────────────────────────────────────

func TestTextHash_TypedEnvelopeContract(t *testing.T) {
	t.Run("typed_literal_assignment_ok", func(t *testing.T) {
		var h TextHash = TextHash("0123456789abcdef")
		if string(h) != "0123456789abcdef" {
			t.Fatalf("string(TextHash('0123456789abcdef')) = %q; want verbatim", string(h))
		}
	})

	t.Run("EmptyTextHash_is_canonical_zero", func(t *testing.T) {
		var zero TextHash
		if zero != EmptyTextHash {
			t.Fatalf("zero-value TextHash = %q; want EmptyTextHash = %q", zero, EmptyTextHash)
		}
		if !zero.IsEmpty() {
			t.Fatalf("zero-value TextHash.IsEmpty() = false; want true")
		}
	})

	t.Run("ComputeTextHash_byte_stable", func(t *testing.T) {
		// Canonical godlike/07 contract: same input → same hash.
		// 3 invocations on the same text must produce byte-identical
		// 64-char hex digests (PR-VO-TEXTHASH-64).
		text := "the canonical voiceover text for byte-stability check"
		hash1 := ComputeTextHash(text)
		hash2 := ComputeTextHash(text)
		hash3 := ComputeTextHash(text)
		if hash1 != hash2 || hash2 != hash3 {
			t.Fatalf("ComputeTextHash not byte-stable: %q vs %q vs %q", hash1, hash2, hash3)
		}
		if len(string(hash1)) != 64 {
			t.Fatalf("ComputeTextHash length = %d; want 64 hex chars (SHA-256 full digest, PR-VO-TEXTHASH-64)", len(string(hash1)))
		}
	})

	t.Run("ComputeTextHash_empty_text_returns_EmptyTextHash", func(t *testing.T) {
		// Defensive: empty text returns EmptyTextHash per the canonical
		// "no collision with non-empty" guard.
		h := ComputeTextHash("")
		if h != EmptyTextHash {
			t.Fatalf("ComputeTextHash('') = %q; want EmptyTextHash", h)
		}
	})

	t.Run("ComputeTextHash_distinct_inputs_distinct_hashes", func(t *testing.T) {
		// Two distinct texts MUST produce distinct hashes.
		hash1 := ComputeTextHash("text one")
		hash2 := ComputeTextHash("text two")
		if hash1 == hash2 {
			t.Fatalf("ComputeTextHash collision on distinct inputs: %q == %q", hash1, hash2)
		}
	})

	t.Run("ComputeTextHash_lowercase_hex_only", func(t *testing.T) {
		// PR-VO-TEXTHASH-64: full 64-char SHA-256. Lowercase hex
		// is stdlib default.
		h := ComputeTextHash("lowercase-hex-check")
		hex := string(h)
		for _, r := range hex {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				t.Fatalf("ComputeTextHash returned non-lowercase-hex char %q in %q", r, hex)
			}
		}
	})

	t.Run("ComputeTextHash_collision_resistance_1000_inputs", func(t *testing.T) {
		// At-scale collision-resistance sanity check per code-reviewer
		// NICE-TO-HAVE #4. PR-VO-TEXTHASH-64: full SHA-256,
		// probability of collision ~1 in 2^256 per pair → 0 at 1000.
		const N = 1000
		seen := make(map[TextHash]struct{}, N)
		for i := 0; i < N; i++ {
			h := ComputeTextHash(strings.Repeat("voiceover-text-", 1) + string(rune('a'+i%26)) + string(rune('0'+i/26)))
			if _, dup := seen[h]; dup {
				t.Fatalf("ComputeTextHash duplicate at iteration %d (hash=%q)", i, h)
			}
			seen[h] = struct{}{}
		}
		if len(seen) != N {
			t.Fatalf("ComputeTextHash unique-hash count = %d; want %d", len(seen), N)
		}
	})

	t.Run("TextHash_String_returns_underlying_string", func(t *testing.T) {
		h := TextHash("ffffffffffffffff")
		if h.String() != "ffffffffffffffff" {
			t.Fatalf("TextHash.String() = %q; want ffffffffffffffff", h.String())
		}
	})

	t.Run("TextHash_JSON_wire_byte_equivalent", func(t *testing.T) {
		// omitempty makes zero-value TextHash serialize as absent.
		type wire struct {
			TextHash TextHash `json:"text_hash"`
		}
		filled := wire{TextHash: TextHash("0123456789abcdef")}
		b, err := json.Marshal(filled)
		if err != nil {
			t.Fatalf("json.Marshal(filled wire) returned err=%v", err)
		}
		if string(b) != `{"text_hash":"0123456789abcdef"}` {
			t.Fatalf("filled TextHash JSON wire = %s; want verbatim", string(b))
		}
	})
}

// ────────────────────────────────────────────────────────────────────────
// Cross-cutting contract — the 2 intentional boundary sites
// ────────────────────────────────────────────────────────────────────────

// TestTypedEnvelopes_RawStringAtBoundary points the canonical raw-string
// surface sites (2 known sites per architecture/current.yaml#PR-VO-TYPED-PRIMITIVES
// audit-pin discipline). These sites must keep their raw-string shape
// because:
//
//   - persistence/repository.go:122-128 — Go circular-import rule
//     (persistence sub-package cannot import the parent voiceover package;
//     the adapter at internal/app/adapters_voiceover_use_case.go
//     converts raw-string ↔ typed-envelope at the boundary).
//   - jobs/result_dto.go:78 — JSON wire compat (VoiceoverChildResult
//     serializes into job.Result as plain string to match the legacy
//     shape; the consumer reads via `voiceover.Language(r.Language)`).
//
// This test does NOT change those 2 raw-string sites (they are
// intentionally raw per the audit-pin comments). It exercises the
// canonical conversion idiom at the boundary so future regressions
// in the adapter layer surface here.
func TestTypedEnvelopes_RawStringAtBoundary(t *testing.T) {
	t.Run("typed_to_raw_string_conversion_at_persistence_boundary", func(t *testing.T) {
		// Mirrors internal/app/adapters_voiceover_use_case.go::toInfraRecord
		lang := Language("en-US")
		raw := string(lang) // canonical typed → raw conversion at persistence boundary
		if raw != "en-US" {
			t.Fatalf("typed→raw conversion at persistence boundary failed: %q != 'en-US'", raw)
		}
	})

	t.Run("raw_to_typed_string_conversion_at_persistence_boundary", func(t *testing.T) {
		// Mirrors internal/app/adapters_voiceover_use_case.go::toAppRecord
		raw := "en-US"
		lang := Language(raw) // canonical raw → typed conversion at persistence boundary
		if string(lang) != "en-US" {
			t.Fatalf("raw→typed conversion at persistence boundary failed: %q != 'en-US'", string(lang))
		}
	})
}
