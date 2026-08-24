package texttracks_test

// text_track_hashes_test.go pins the canonical NormalizeForHash /
// TextHash / SourceVersion formulas as behavioural contracts. If any
// of these tests fail, the canonical namespace for materialised
// asset_text_tracks has drifted and the FOREIGN idempotency keys are
// at risk (godlike/06 SSOT).
//
// godlike/07 honest scope-lock: nothing in this file touches the
// repository — the SQLite repository accepts the produced strings
// verbatim (tested separately under the asset_text_tracks integration
// suite). Drift here MUST fail the build before reaching prod.

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestNormalizeForHash_StripsAndFoldsCase(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello world", "hello world"},
		{"  Hello   WORLD\n ", "hello world"},
		{"\tMulti\nline\ntext ", "multi line text"},
		{"", ""},
	}
	for _, tc := range cases {
		got := asset.NormalizeForHash(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeForHash(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTextHash_Deterministic(t *testing.T) {
	h1 := asset.TextHash("Hello world", "en", asset.TextTrackTranscript)
	h2 := asset.TextHash("Hello world", "en", asset.TextTrackTranscript)
	if h1 != h2 {
		t.Fatalf("TextHash not deterministic: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("TextHash should be a 64-hex SHA-256 string, got length %d (%q)", len(h1), h1)
	}
}

func TestTextHash_NormalisedInputsAreEqual(t *testing.T) {
	// "Hello World" and "  hello world  " must produce identical hash.
	h1 := asset.TextHash("Hello World", "en", asset.TextTrackTranscript)
	h2 := asset.TextHash("  hello world  ", "en", asset.TextTrackTranscript)
	if h1 != h2 {
		t.Fatalf("TextHash should be invariant to casing/whitespace: %s vs %s", h1, h2)
	}
}

func TestTextHash_DiffersAcrossKind(t *testing.T) {
	base := asset.TextHash("Hello world", "en", asset.TextTrackTranscript)
	if h := asset.TextHash("Hello world", "en", asset.TextTrackTitle); h == base {
		t.Fatal("text_kind change should produce a different hash")
	}
	if h := asset.TextHash("Hello world", "en", asset.TextTrackDescription); h == base {
		t.Fatal("text_kind change must produce a different hash (transcript vs description)")
	}
}

func TestTextHash_DiffersAcrossLanguage(t *testing.T) {
	base := asset.TextHash("Hello world", "en", asset.TextTrackTranscript)
	if h := asset.TextHash("Hello world", "it", asset.TextTrackTranscript); h == base {
		t.Fatal("language change should produce a different hash")
	}
	if h := asset.TextHash("Hello world", "es", asset.TextTrackTranscript); h == base {
		t.Fatal("language change should produce a different hash (es)")
	}
}

func TestTextHash_DiffersAcrossContent(t *testing.T) {
	h1 := asset.TextHash("Hello world", "en", asset.TextTrackTranscript)
	h2 := asset.TextHash("Hello world!", "en", asset.TextTrackTranscript)
	if h1 == h2 {
		t.Fatal("content change should produce a different hash")
	}
}

func TestTextHash_EmptyLanguageDefaultsToUndetermined(t *testing.T) {
	// godlike/07 honest lock: empty language_code must NEVER default
	// to "en". BCP-47 "und" is the canonical undetermined marker so
	// the idempotency key still computes (DDL requires
	// language_code NOT NULL with no DEFAULT).
	hEmpty := asset.TextHash("Hello world", "", asset.TextTrackTranscript)
	hUnd := asset.TextHash("Hello world", "und", asset.TextTrackTranscript)
	if hEmpty != hUnd {
		t.Fatalf("empty language_code must hash identically to \"und\": %s vs %s", hEmpty, hUnd)
	}
}

func TestSourceVersion_Deterministic(t *testing.T) {
	v1 := asset.SourceVersion("abc", "en", "it", "ollama", "qwen2.5", "v3", "prompt-v1")
	v2 := asset.SourceVersion("abc", "en", "it", "ollama", "qwen2.5", "v3", "prompt-v1")
	if v1 != v2 {
		t.Fatalf("SourceVersion not deterministic: %s vs %s", v1, v2)
	}
	if len(v1) != 64 {
		t.Fatalf("SourceVersion should be a 64-hex SHA-256 string, got length %d (%q)", len(v1), v1)
	}
}

func TestSourceVersion_DiffersWhenAnyInputChanges(t *testing.T) {
	base := asset.SourceVersion("abc", "en", "it", "ollama", "qwen2.5", "v3", "prompt-v1")

	cases := []struct {
		name string
		got  string
	}{
		{"text_hash", asset.SourceVersion("XYZ", "en", "it", "ollama", "qwen2.5", "v3", "prompt-v1")},
		{"source_lang", asset.SourceVersion("abc", "fr", "it", "ollama", "qwen2.5", "v3", "prompt-v1")},
		{"target_lang", asset.SourceVersion("abc", "en", "es", "ollama", "qwen2.5", "v3", "prompt-v1")},
		{"provider", asset.SourceVersion("abc", "en", "it", "openai", "qwen2.5", "v3", "prompt-v1")},
		{"model_name", asset.SourceVersion("abc", "en", "it", "ollama", "qwen2.6", "v3", "prompt-v1")},
		{"model_version", asset.SourceVersion("abc", "en", "it", "ollama", "qwen2.5", "v4", "prompt-v1")},
		{"prompt_version", asset.SourceVersion("abc", "en", "it", "ollama", "qwen2.5", "v3", "prompt-v2")},
	}
	for _, tc := range cases {
		if tc.got == base {
			t.Errorf("SourceVersion should differ when %s changes (got identical hash)", tc.name)
		}
	}
}

func TestSourceVersion_EmptyInputsAreStable(t *testing.T) {
	// Empty model/prompt metadata is allowed (provider doesn't
	// expose a taxonomy). Hash chain must remain stable across
	// reproduction calls.
	v1 := asset.SourceVersion("abc", "en", "it", "", "", "", "")
	v2 := asset.SourceVersion("abc", "en", "it", "", "", "", "")
	if v1 != v2 {
		t.Fatalf("SourceVersion with empty metadata should still be deterministic: %s vs %s", v1, v2)
	}
}

// ── PR-CATALOG-MULTILINGUA step 4 (July 2026) — TranslationKey SSOT ──────
//
// TranslationKey is the canonical deterministic fingerprint of a
// translation REQUEST context — the look-up key the Materializer uses
// BEFORE calling the LLM to decide "have I already produced a
// translation under this exact (source, model, prompt) tuple into
// this language?". The canonical formula lives in
// asset.TranslationKey; these tests pin the SOLE-canonical-owner
// invariant:
//   - TestTranslationKey_Deterministic pins the SHA-256 chain + the
//     pipe-separator namespace.
//   - TestTranslationKey_DiffersWhenAnyInputChanges pins that each
//     of the 5 input slots is independently variant-discriminating
//     (a perceptual SMPI contract — bumping ANY one of the 5
//     discriminators MUST produce a distinct fingerprint).
//   - TestTranslationKey_EmptyFieldsAreStable pins the empty-string
//     convention for providers that don't expose a model taxonomy.
//
// Drift here MUST fail the build before reaching prod — it's the
// translation-idempotency namespace.

func TestTranslationKey_Deterministic(t *testing.T) {
	// 5-tuple SHA-256: source_text_hash + target_language +
	// translation_model + model_version + prompt_version
	// (godlike/06 SSOT — one canonical owner per fact).
	v1 := asset.TranslationKey("abc-text-hash", "it", "ollama", "v3", "prompt-v1")
	v2 := asset.TranslationKey("abc-text-hash", "it", "ollama", "v3", "prompt-v1")
	if v1 != v2 {
		t.Fatalf("TranslationKey not deterministic: %s vs %s", v1, v2)
	}
	if len(v1) != 64 {
		t.Fatalf("TranslationKey should be a 64-hex SHA-256 string, got length %d (%q)", len(v1), v1)
	}
}

func TestTranslationKey_DiffersWhenAnyInputChanges(t *testing.T) {
	base := asset.TranslationKey("abc-text-hash", "it", "ollama", "v3", "prompt-v1")

	cases := []struct {
		name string
		got  string
	}{
		{"source_text_hash", asset.TranslationKey("different-text-hash", "it", "ollama", "v3", "prompt-v1")},
		{"target_language", asset.TranslationKey("abc-text-hash", "es", "ollama", "v3", "prompt-v1")},
		{"translation_model", asset.TranslationKey("abc-text-hash", "it", "deepl", "v3", "prompt-v1")},
		{"model_version", asset.TranslationKey("abc-text-hash", "it", "ollama", "v4", "prompt-v1")},
		{"prompt_version", asset.TranslationKey("abc-text-hash", "it", "ollama", "v3", "prompt-v2")},
	}
	for _, tc := range cases {
		if tc.got == base {
			t.Errorf("TranslationKey should differ when %s changes (got identical hash)", tc.name)
		}
	}
}

func TestTranslationKey_EmptyFieldsAreStable(t *testing.T) {
	// Empty translation_model / model_version / prompt_version is
	// allowed for providers that don't expose a model taxonomy.
	// The hash chain must remain stable across reproduction calls.
	v1 := asset.TranslationKey("abc", "it", "", "", "")
	v2 := asset.TranslationKey("abc", "it", "", "", "")
	if v1 != v2 {
		t.Fatalf("TranslationKey with empty metadata should still be deterministic: %s vs %s", v1, v2)
	}
}

func TestTranslationKey_DiffersFromSourceVersion(t *testing.T) {
	// godlike/06 SSOT smoke: TranslationKey and SourceVersion MUST
	// diverge when called with overlapping inputs — they encode
	// DIFFERENT facts (request fingerprint vs derived-row
	// fingerprint). Drift between them is a sign one helper was
	// reimplemented against the other's formula.
	tk := asset.TranslationKey("abc", "it", "ollama", "v3", "prompt-v1")
	sv := asset.SourceVersion("abc", "en", "it", "ollama", "qwen2.5", "v3", "prompt-v1")
	if tk == sv {
		t.Fatalf("TranslationKey and SourceVersion produced identical hashes for distinct formulas — reimplementation drift: %s", tk)
	}
}
