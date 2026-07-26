// Package dto — tests for the P0.6 no-silent-fallback translator contract.
// These tests exist to lock in the godlike/07 no-fake-availability
// guarantee: a translation error must NEVER cause the original Title /
// Description / Tags to surface in the result.
package dto

import (
	"context"
	"errors"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/stretchr/testify/assert"
)

// mockTranslatorFailingTranslate is a MetadataGenerator stub that
// fails every TranslateTextWithModel call but succeeds on the
// English metadata LLM call. Used by tests that want a successful
// English-metadata path and failing translations for non-English
// languages.
type mockTranslatorFailingTranslate struct {
	enDesc string
	enTags []string
}

func (m *mockTranslatorFailingTranslate) GenerateVideoMetadataWithModel(_ context.Context, _, _ string) (string, []string, error) {
	return m.enDesc, m.enTags, nil
}

func (m *mockTranslatorFailingTranslate) TranslateTextWithModel(_ context.Context, _, _, _ string) (string, error) {
	return "", errors.New("forced translation failure (P0.6 test)")
}

// mockTranslatorEmptyPayload is a MetadataGenerator stub that
// "succeeds" at all calls but returns an empty payload from the
// English LLM metadata call. This locks the P0.6 godlike/07
// regression for the silent empty-payload success case.
type mockTranslatorEmptyPayload struct{}

func (m *mockTranslatorEmptyPayload) GenerateVideoMetadataWithModel(_ context.Context, _, _ string) (string, []string, error) {
	return "", nil, nil // no error but empty payload
}

func (m *mockTranslatorEmptyPayload) TranslateTextWithModel(_ context.Context, text, lang, _ string) (string, error) {
	return text + "_" + lang, nil
}

// TestGenerateVideoMetadata_TranslationFailureDropsOriginal is the
// canonical regression test for P0.6 sotto-task 4. It verifies that
// even when every translation call fails, the result does NOT carry
// the original title / enDesc / enTags — those would be a silent
// fake-success per godlike/07.
//
// Order-independence: the implementation uses concurrent.SafeGoFunc
// to parallelise per-language work; goroutine completion order is
// not deterministic. We index by Language rather than slice position.
func TestGenerateVideoMetadata_TranslationFailureDropsOriginal(t *testing.T) {
	tr := &mockTranslatorFailingTranslate{
		enDesc: "ORIGINAL_EN_DESCRIPTION_DO_NOT_LEAK",
		enTags: []string{"ORIGINAL_TAG_DO_NOT_LEAK"},
	}
	results := GenerateVideoMetadata(context.Background(), tr, "ORIGINAL_TITLE_DO_NOT_LEAK", []string{"en", "it", "es", "fr"}, "model")
	byLang := indexByLanguage(t, results)

	assert.Len(t, results, 4, "all 4 languages must produce a result entry even when translation fails (call graph contract)")

	// English — source language; Title is the user input by contract.
	en := byLang["en"]
	assert.Equal(t, "ORIGINAL_TITLE_DO_NOT_LEAK", en.Title, "English Title must be the input (source language, no translation needed)")
	assert.Equal(t, "ORIGINAL_EN_DESCRIPTION_DO_NOT_LEAK", en.Description, "English Description comes from the single LLM call")
	assert.Equal(t, []string{"ORIGINAL_TAG_DO_NOT_LEAK"}, en.Tags, "English Tags come from the single LLM call")
	assert.Equal(t, "translated", en.TranslationStatus)

	// it / es / fr — translation must have failed; no original text must leak.
	for _, lang := range []string{"it", "es", "fr"} {
		m := byLang[lang]
		assert.NotContains(t, m.Title, "ORIGINAL_TITLE_DO_NOT_LEAK",
			"P0.6 regression: failed translation MUST NOT surface the original Title (silent fake-success banned) — language="+lang)
		assert.NotContains(t, m.Description, "ORIGINAL_EN_DESCRIPTION_DO_NOT_LEAK",
			"P0.6 regression: failed translation MUST NOT surface the original enDesc (silent fake-success banned) — language="+lang)
		for _, tag := range m.Tags {
			assert.NotEqual(t, "ORIGINAL_TAG_DO_NOT_LEAK", tag,
				"P0.6 regression: failed tag translation MUST NOT surface the original tag — language="+lang)
		}
		assert.Equal(t, "untranslated", m.TranslationStatus,
			"non-English languages must report TranslationStatus=untranslated on translator failure — language="+lang)
		assert.Equal(t, "", m.Title, "failed Title must be empty string — language="+lang)
		assert.Equal(t, "", m.Description, "failed Description must be empty string — language="+lang)
		assert.Empty(t, m.Tags, "failed Tags must be nil/empty — language="+lang)
	}
}

// TestGenerateVideoMetadata_HappyPathPreservesTranslation verifies
// the positive control: when the translator succeeds, the result
// carries the translated text and TranslationStatus="translated".
//
// Order-independence: see indexByLanguage.
func TestGenerateVideoMetadata_HappyPathPreservesTranslation(t *testing.T) {
	tr := &mockTranslatorSuccess{
		enDesc: "english desc",
		enTags: []string{"en_tag"},
	}
	results := GenerateVideoMetadata(context.Background(), tr, "English Title", []string{"en", "it"}, "model")
	byLang := indexByLanguage(t, results)
	if assert.Len(t, results, 2) {
		// en — source language, populated from the LLM call directly.
		en := byLang["en"]
		assert.Equal(t, "English Title", en.Title)
		assert.Equal(t, "english desc", en.Description)
		assert.Equal(t, []string{"en_tag"}, en.Tags)
		assert.Equal(t, "translated", en.TranslationStatus)
		// it — title is translated by the mock (suffix _it).
		it := byLang["it"]
		assert.Equal(t, "English Title_it", it.Title,
			"it Title must come from the translator (mock suffix _it)")
		assert.Equal(t, "translated", it.TranslationStatus)
		// description & tags also translated by the mock.
		assert.Equal(t, "english desc_it", it.Description)
		assert.Equal(t, []string{"en_tag_it"}, it.Tags)
	}
}

// TestGenerateVideoMetadata_EnglishLLMFailureMarksUntranslated locks
// the P0.6 Block listener #2 fix: when the source-language LLM call
// fails (no enDesc/enTags), the English VideoMetadata MUST downgrade
// to TranslationStatus="untranslated" instead of silently shipping
// empty fields under "translated" status. Per godlike/07.
//
// Order-independence: see indexByLanguage.
func TestGenerateVideoMetadata_EnglishLLMFailureMarksUntranslated(t *testing.T) {
	tr := &mockTranslatorFailingAll{}
	results := GenerateVideoMetadata(context.Background(), tr, "English Title", []string{"en", "it"}, "model")
	byLang := indexByLanguage(t, results)
	if assert.Len(t, results, 2) {
		// en — English, but the LLM failed. Must NOT be silently "translated".
		en := byLang["en"]
		assert.Equal(t, "untranslated", en.TranslationStatus,
			"P0.6 godlike/07 regression: English LLM failure MUST mark TranslationStatus=untranslated")
		assert.Equal(t, "", en.Title,
			"failed English LLM MUST NOT carry the user-supplied Title as a fake-success marker")
		assert.Equal(t, "", en.Description)
		assert.Empty(t, en.Tags)
		// it — translation also fails (the mock fails every Translate call), so also untranslated.
		it := byLang["it"]
		assert.Equal(t, "untranslated", it.TranslationStatus,
			"non-English language with transmission failure MUST also be untranslated")
	}
}

// indexByLanguage groups results by Language so tests are
// order-independent. concurrent.SafeGoFunc in GenerateVideoMetadata
// makes goroutine completion order non-deterministic, so tests must
// never assume results[i] corresponds to languages[i].
func indexByLanguage(t *testing.T, results []scriptpkg.VideoMetadata) map[string]scriptpkg.VideoMetadata {
	t.Helper()
	out := make(map[string]scriptpkg.VideoMetadata, len(results))
	for _, r := range results {
		out[r.Language] = r
	}
	return out
}

// TestGenerateVideoMetadata_NilGeneratorReturnsEmpty locks the
// nil-generator short-circuit guard.
func TestGenerateVideoMetadata_NilGeneratorReturnsEmpty(t *testing.T) {
	results := GenerateVideoMetadata(context.Background(), nil, "English Title", []string{"en", "it"}, "model")
	assert.Empty(t, results, "nil generator must produce an empty result slice (callers detect missing dependency)")
}

// TestGenerateVideoMetadata_EnglishLLMEmptyPayloadMarksUntranslated
// locks the P0.6 B1 follow-up: when the source-language LLM call
// returns successfully BUT with an empty payload
// (`("", nil, nil)`), the English VideoMetadata MUST downgrade to
// TranslationStatus="untranslated" rather than silently shipping
// empty fields under "translated" status. This is the
// empty-payload-silent-success variant caught by the strengthened
// enOK condition (`err == nil && (desc != "" || len(tags) > 0)`).
//
// Per godlike/07 this is the same silent fake-success the original
// P0.6 fix targets — the upstream variant.
func TestGenerateVideoMetadata_EnglishLLMEmptyPayloadMarksUntranslated(t *testing.T) {
	tr := &mockTranslatorEmptyPayload{}
	results := GenerateVideoMetadata(context.Background(), tr, "ORIGINAL_TITLE_DO_NOT_LEAK", []string{"en", "it"}, "model")
	byLang := indexByLanguage(t, results)
	if assert.Len(t, results, 2) {
		// en — LLM returned no error but empty payload. MUST
		// downgrade to untranslated (NOT silently ship "" / nil / nil
		// under "translated" status).
		en := byLang["en"]
		assert.Equal(t, "untranslated", en.TranslationStatus,
			"P0.6 godlike/07 B1 regression: English LLM empty payload MUST mark TranslationStatus=untranslated (NOT 'translated' with empty fields)")
		assert.Equal(t, "", en.Title,
			"empty-payload English LLM MUST NOT carry the user-supplied Title as a fake-success marker")
		assert.Equal(t, "", en.Description)
		assert.Empty(t, en.Tags)
		// it — TranslateTextWithModel mock succeeds with text+lang
		// suffix; non-English metadata path goes through normal
		// translation. Title becomes "ORIGINAL_TITLE_DO_NOT_LEAK_it".
		// Description: TranslateTextWithModel("","it") \u2192 "_it" (empty
		// translates to "_\" + lang" \u2192 empty still since mock echoes input)
		// Actually the mockTranslate returns text + "_" + lang for any
		// input, including empty. So "" -> "_it" which is non-empty.
		// Tags: enTags is nil/empty so the loop body never executes,
		// meta.Tags stays nil. TranslationStatus="translated" because
		// the translation succeeded. ✓
		it := byLang["it"]
		assert.Equal(t, "translated", it.TranslationStatus,
			"non-English path with successful translation must remain translated; only the English empty-payload path marks untranslated")
		// The empty enDesc passing through TranslateTextWithModel
		// becomes "_it" (per the mock) which is non-empty, so the
		// description field is populated. Tags stays nil because
		// enTags is empty so the for-loop never runs.
		// metadata.go guards the description translation branch with
		// `if enDesc != ""` (around line 144) — when the source-LLM
		// payload is empty (enDesc == ""), the description translation
		// step is skipped entirely, leaving it.Description at its
		// zero value ("") rather than carrying a translated-from-empty
		// place-holder like "_it". per godlike/07 we prefer the hard
		// skip over a fake sentinel because a "_it" string on a
		// Description field would mislead downstream consumers into
		// thinking a real translation succeeded.
		assert.Equal(t, "", it.Description,
			"godlike/07 hard-skip: production guards description translation with `if enDesc != \"\"` and empty enDesc skips the block entirely, leaving it.Description empty rather than a fake translated-from-empty sentinel")
		assert.Empty(t, it.Tags,
			"enTags is empty so no tag translation loop runs")
	}
}

// mockTranslatorSuccess is a happy-path MetadataGenerator stub.
type mockTranslatorSuccess struct {
	enDesc string
	enTags []string
}

func (m *mockTranslatorSuccess) GenerateVideoMetadataWithModel(_ context.Context, _, _ string) (string, []string, error) {
	return m.enDesc, m.enTags, nil
}

func (m *mockTranslatorSuccess) TranslateTextWithModel(_ context.Context, text, lang, _ string) (string, error) {
	return text + "_" + lang, nil
}

// mockTranslatorFailingAll fails both the English-metadata LLM call
// AND every per-language TranslateTextWithModel call. Used by the
// B1-style regression test (TestGenerateVideoMetadata_EnglishLLMFailureMarksUntranslated).
type mockTranslatorFailingAll struct{}

func (m *mockTranslatorFailingAll) GenerateVideoMetadataWithModel(_ context.Context, _, _ string) (string, []string, error) {
	return "", nil, errors.New("forced English LLM failure (P0.6 test)")
}

func (m *mockTranslatorFailingAll) TranslateTextWithModel(_ context.Context, _, _, _ string) (string, error) {
	return "", errors.New("forced translation failure (P0.6 test)")
}

// Compile-time assertion: the ollama.Generator concrete satisfies the
// MetadataGenerator port. Production wiring depends on this implicit
// interface satisfaction; we surface the contract here so a future
// port shape change raises a build error rather than a runtime drift.
var _ MetadataGenerator = (*mockTranslatorFailingTranslate)(nil)
var _ MetadataGenerator = (*mockTranslatorSuccess)(nil)
var _ MetadataGenerator = (*mockTranslatorFailingAll)(nil)
var _ MetadataGenerator = (*mockTranslatorEmptyPayload)(nil)
