// Package artlist_phrase — preprocess.go: pure helpers extracted
// from processor_clip_search.go (DedupeEmpty) +
// flow_helpers_artlist.go (TranslateEach + contextualQuery).
//
// godlike/06 SSOT (one canonical owner per fact): each helper lives
// ONLY here. No other package may redefine DedupeEmpty, TranslateEach,
// contextualQuery, mergeHits, or TranslationResult. The pre-PR
// duplicates (processor_clip_search.go::Process inline dedup +
// flow_helpers_artlist.go::artlistSearchPhrase +
// flow_helpers.go::contextualQuery) are retired in the CUTOVER
// phase of this wave (after the 7-day soak).
package artlist_phrase

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

const defaultPhraseParallelism = 4

// DedupeEmpty returns a new slice with empty/whitespace-only strings
// removed and duplicates suppressed by first-occurrence. Preserves
// the input order. Returns nil for nil or empty input.
//
// Pre-PR the equivalent logic was inline in
// processor_clip_search.go::Process. Folding it into a named helper
// makes the contract testable and ensures the service + the
// processor agree on the dedup contract (godlike/06
// one-canonical-owner-per-fact).
func DedupeEmpty(phrases []string) []string {
	if len(phrases) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(phrases))
	out := make([]string, 0, len(phrases))
	for _, p := range phrases {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TranslationResult pairs a translated phrase with any translation
// error. Translated is "" when Err is non-nil (the translation step
// failed and we do not silently fall back to the source phrase —
// godlike/07 no-fake-availability).
type TranslationResult struct {
	Translated string
	Err        error
}

// ErrEmptyTranslation is the canonical sentinel for the "translator
// returned empty text" failure class. Use errors.Is to probe.
var ErrEmptyTranslation = emptyTranslationSentinel{}

type emptyTranslationSentinel struct{}

func (emptyTranslationSentinel) Error() string {
	return "artlist_phrase: translator returned empty text"
}

// Is enables errors.Is(err, ErrEmptyTranslation) probing.
func (emptyTranslationSentinel) Is(target error) bool {
	_, ok := target.(emptyTranslationSentinel)
	return ok
}

// ErrTranslatorNil is the canonical sentinel for the
// "PhraseTranslator port is nil at composition time" failure class.
// The service captures it per-phrase so callers see a typed
// diagnostic rather than a nil-pointer panic at the search call.
// Use errors.Is to probe.
var ErrTranslatorNil = translatorNilSentinel{}

type translatorNilSentinel struct{}

func (translatorNilSentinel) Error() string {
	return "artlist_phrase: PhraseTranslator port not wired (fail-closed at composition)"
}

// Is enables errors.Is(err, ErrTranslatorNil) probing.
func (translatorNilSentinel) Is(target error) bool {
	_, ok := target.(translatorNilSentinel)
	return ok
}

// TranslateEach translates each phrase via the canonical translator.
// Failures are captured per-phrase (not propagated as a batch error)
// so the caller can decide per-phrase whether to search.
//
// Empty/duplicate phrases are filtered by DedupeEmpty before
// translation. The returned map keys are the deduped, trimmed
// phrases — exactly the keys the caller will see in
// PhraseMatch.Phrase.
func TranslateEach(ctx context.Context, translator PhraseTranslator, phrases []string) map[string]TranslationResult {
	deduped := DedupeEmpty(phrases)
	out := make(map[string]TranslationResult, len(deduped))
	if len(deduped) == 0 {
		return out
	}
	results := concurrent.ParallelMap(deduped, phraseParallelism(len(deduped)), func(_ int, phrase string) TranslationResult {
		return translatePhrase(ctx, translator, phrase)
	})
	for i, phrase := range deduped {
		out[phrase] = results[i]
	}
	return out
}

func translatePhrase(ctx context.Context, translator PhraseTranslator, phrase string) TranslationResult {
	if translator == nil {
		return TranslationResult{Err: ErrTranslatorNil}
	}
	translated, err := translator.Translate(ctx, phrase)
	if err != nil {
		return TranslationResult{Err: err}
	}
	translated = strings.TrimSpace(translated)
	if translated == "" {
		return TranslationResult{Err: ErrEmptyTranslation}
	}
	return TranslationResult{Translated: translated}
}

// contextualQuery builds the secondary search query that the legacy
// SearchScriptAssets path issued alongside the translated phrase
// (pre-PR flow_helpers.go::contextualQuery). The new service issues
// the same two queries per phrase (translated + contextual) to
// preserve behavioral parity.
//
// Empty title returns just the translated phrase. Empty translated
// returns just the title. Both empty returns "" (caller already
// guards against this).
func contextualQuery(title, translated string) string {
	title = strings.TrimSpace(title)
	translated = strings.TrimSpace(translated)
	if title == "" {
		return translated
	}
	if translated == "" {
		return title
	}
	return title + " " + translated
}

// mergeHits merges two hit slices and caps the result at maxLen.
// Preserves the first-occurrence order across h1 then h2 (matches
// the legacy SearchScriptAssets behavior: translated query results
// take priority over contextual query results). Duplicates (same
// AssetID) are suppressed. Returns nil for maxLen<=0 or empty input.
func mergeHits(h1, h2 []ports.AssetSearchHit, maxLen int) []ports.AssetSearchHit {
	if maxLen <= 0 || len(h1)+len(h2) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(h1)+len(h2))
	out := make([]ports.AssetSearchHit, 0, maxLen)
	for _, h := range h1 {
		if _, ok := seen[h.AssetID]; ok {
			continue
		}
		seen[h.AssetID] = struct{}{}
		out = append(out, h)
		if len(out) >= maxLen {
			return out
		}
	}
	for _, h := range h2 {
		if _, ok := seen[h.AssetID]; ok {
			continue
		}
		seen[h.AssetID] = struct{}{}
		out = append(out, h)
		if len(out) >= maxLen {
			return out
		}
	}
	return out
}

func phraseParallelism(count int) int {
	if count <= 0 {
		return 1
	}
	if count < defaultPhraseParallelism {
		return count
	}
	return defaultPhraseParallelism
}
