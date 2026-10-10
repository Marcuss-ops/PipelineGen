// Package translation — fallback_translator.go: a TranslationPort that
// fans a request through a primary provider and, on failure, falls back to
// a secondary provider.
//
// Canonical use (PR-ARGOS-TRANSLATION, Aug 2026): Argos Translate is the
// deterministic, CPU-only primary; Ollama is the quality fallback. The
// chain is fail-soft and never fakes a success — godlike/07.
//
// Quality gate (Sept 2026): "the primary answered with a non-empty string" is
// NOT the same as "the primary translated correctly". Argos can drop a clause,
// loop, copy the source, or answer in the wrong language — all with a
// non-empty answer that the old chain accepted and certified. Every answer is
// now assessed by AssessTranslation (quality.go) before it is accepted:
//
//   - a degenerate PRIMARY answer is not returned; the request continues to the
//     fallback provider (the LLM), which is the whole point of having one;
//   - a degenerate FALLBACK answer is a loud typed error
//     (*ErrDegenerateTranslation) — never a wrong subtitle persisted under a
//     real translation key;
//   - when no fallback is wired, a degenerate primary answer is returned with
//     the typed error so the caller can fail the language instead of burning it.
package translation

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// FallbackTranslator routes each Translate call through `primary` and, when
// the primary errors, returns empty output, or produces a degenerate answer,
// retries through `fallback`. The winning provider's provenance (UsedProvider
// / UsedModel) is preserved on the returned result so the materializer
// persists the honest source.
type FallbackTranslator struct {
	primary  TranslationPort
	fallback TranslationPort
	log      *zap.Logger
}

// NewFallbackTranslator constructs the chain. Either provider may be nil
// (a nil provider is skipped); at least one must be non-nil or Translate
// returns a typed error.
func NewFallbackTranslator(primary, fallback TranslationPort, log *zap.Logger) *FallbackTranslator {
	return &FallbackTranslator{primary: primary, fallback: fallback, log: log}
}

// Translate implements TranslationPort.
func (f *FallbackTranslator) Translate(ctx context.Context, cmd TranslationCommand) (TranslationResult, error) {
	if f == nil {
		return TranslationResult{}, ErrUnimplemented
	}

	if f.primary != nil {
		res, err := f.primary.Translate(ctx, cmd)
		if err == nil && res.TranslatedText != "" {
			if assess := AssessTranslation(cmd.Text, res.TranslatedText, cmd.SourceLang, cmd.TargetLang); assess.Acceptable() {
				return res, nil
			} else {
				// Keep the primary answer so a no-fallback chain can still
				// surface it alongside the typed error.
				f.logDegenerate("primary", cmd, res, assess)
				if f.fallback == nil {
					return res, &ErrDegenerateTranslation{
						SourceLang: cmd.SourceLang,
						TargetLang: cmd.TargetLang,
						Provider:   res.UsedProvider,
						Issues:     assess.Issues,
						Reason:     assess.Reason,
					}
				}
			}
		} else {
			if f.log != nil {
				reason := "primary provider returned empty"
				if err != nil {
					reason = err.Error()
				}
				f.log.Warn("translation: primary provider failed, falling back",
					zap.String("source", cmd.SourceLang),
					zap.String("target", cmd.TargetLang),
					zap.String("reason", reason),
				)
			}
		}
	}

	if f.fallback != nil {
		res, err := f.fallback.Translate(ctx, cmd)
		if err != nil {
			return res, err
		}
		if res.TranslatedText == "" {
			return res, fmt.Errorf("translation: fallback provider returned empty output")
		}
		if assess := AssessTranslation(cmd.Text, res.TranslatedText, cmd.SourceLang, cmd.TargetLang); !assess.Acceptable() {
			f.logDegenerate("fallback", cmd, res, assess)
			return res, &ErrDegenerateTranslation{
				SourceLang: cmd.SourceLang,
				TargetLang: cmd.TargetLang,
				Provider:   res.UsedProvider,
				Issues:     assess.Issues,
				Reason:     assess.Reason,
			}
		}
		return res, nil
	}

	return TranslationResult{}, fmt.Errorf("translation: no provider available for %s->%s", cmd.SourceLang, cmd.TargetLang)
}

// logDegenerate records why an answer was rejected. Observability only: the
// verdict itself is deterministic and does not depend on the logger.
func (f *FallbackTranslator) logDegenerate(role string, cmd TranslationCommand, res TranslationResult, assess Assessment) {
	if f.log == nil {
		return
	}
	issues := make([]string, 0, len(assess.Issues))
	for _, issue := range assess.Issues {
		issues = append(issues, string(issue))
	}
	f.log.Warn("translation: degenerate answer rejected",
		zap.String("role", role),
		zap.String("provider", res.UsedProvider),
		zap.String("model", res.UsedModel),
		zap.String("source", cmd.SourceLang),
		zap.String("target", cmd.TargetLang),
		zap.Strings("issues", issues),
		zap.String("reason", assess.Reason),
		zap.Float64("length_ratio", assess.LengthRatio),
	)
}

// TranslateBatch implements BatchTranslationPort (P3b, anti-muda): the
// expensive leg is batched, the cheap leg stays per-segment.
//
// Every segment first goes through `primary` alone (today: Argos sidecar —
// CPU-only, no prompt tax). Segments the primary answers acceptably keep
// that answer; only the failed/degenerate remainder is sent to `fallback`
// in ONE batched call per ChunkSize window (today: Ollama — the call
// dominated by the ~130-token system prompt). Without this, a chain could
// only batch when the whole request skipped the primary, so every fallback
// cue paid a full LLM round-trip alone.
//
// Provenance: UsedProvider is the primary's when it served every segment,
// the fallback's when it served every segment, else "mixed" (godlike/07:
// never attribute a mixed answer to one provider).
func (f *FallbackTranslator) TranslateBatch(ctx context.Context, cmd BatchTranslationCommand) (BatchTranslationResult, error) {
	if f == nil {
		return BatchTranslationResult{}, ErrUnimplemented
	}
	if len(cmd.Segments) == 0 {
		return BatchTranslationResult{}, nil
	}
	if f.primary == nil && f.fallback == nil {
		return BatchTranslationResult{}, ErrUnimplemented
	}

	out := make([]BatchTranslationSegment, len(cmd.Segments))
	pending := make([]int, 0, len(cmd.Segments))
	primaryProvider := ""
	for i, segment := range cmd.Segments {
		out[i] = segment
		if f.primary == nil {
			pending = append(pending, i)
			continue
		}
		res, err := f.primary.Translate(ctx, TranslationCommand{
			SourceLang: cmd.SourceLang,
			TargetLang: cmd.TargetLang,
			Text:       segment.Text,
		})
		if err != nil {
			pending = append(pending, i)
			continue
		}
		if res.TranslatedText == "" {
			pending = append(pending, i)
			continue
		}
		if assess := AssessTranslation(segment.Text, res.TranslatedText, cmd.SourceLang, cmd.TargetLang); !assess.Acceptable() {
			f.logDegenerate("primary", TranslationCommand{
				SourceLang: cmd.SourceLang,
				TargetLang: cmd.TargetLang,
				Text:       segment.Text,
			}, res, assess)
			pending = append(pending, i)
			continue
		}
		out[i].Text = res.TranslatedText
		if primaryProvider == "" {
			primaryProvider = res.UsedProvider
		}
	}
	if len(pending) == 0 {
		return BatchTranslationResult{Segments: out, UsedProvider: primaryProvider, CacheStatus: "miss"}, nil
	}
	if f.fallback == nil {
		first := cmd.Segments[pending[0]]
		return BatchTranslationResult{}, &ErrDegenerateTranslation{
			SourceLang: cmd.SourceLang,
			TargetLang: cmd.TargetLang,
			Reason:     fmt.Sprintf("primary failed %d of %d segments and no fallback is wired (first: %q)", len(pending), len(cmd.Segments), first.Text),
		}
	}

	// The fallback leg: batch-capable fallback gets one call per window,
	// otherwise degrade to per-segment fallback calls (yesterday's cost).
	if batchFallback, ok := f.fallback.(BatchTranslationPort); ok {
		window := cmd.ChunkSize
		if window < 1 {
			window = len(pending)
		}
		fallbackProvider := ""
		for start := 0; start < len(pending); start += window {
			end := start + window
			if end > len(pending) {
				end = len(pending)
			}
			windowSegments := make([]BatchTranslationSegment, 0, end-start)
			for _, index := range pending[start:end] {
				windowSegments = append(windowSegments, BatchTranslationSegment{ID: cmd.Segments[index].ID, Text: cmd.Segments[index].Text})
			}
			res, err := batchFallback.TranslateBatch(ctx, BatchTranslationCommand{
				SourceLang:  cmd.SourceLang,
				TargetLang:  cmd.TargetLang,
				Segments:    windowSegments,
				ModelPolicy: cmd.ModelPolicy,
				ChunkSize:   len(windowSegments),
			})
			if err != nil {
				return BatchTranslationResult{}, err
			}
			if len(res.Segments) != len(windowSegments) {
				return BatchTranslationResult{}, fmt.Errorf("translation: fallback batch returned %d segments for %d requested", len(res.Segments), len(windowSegments))
			}
			byID := make(map[string]string, len(res.Segments))
			for _, segment := range res.Segments {
				byID[segment.ID] = segment.Text
			}
			for _, index := range pending[start:end] {
				text, ok := byID[cmd.Segments[index].ID]
				if !ok || text == "" {
					return BatchTranslationResult{}, fmt.Errorf("translation: fallback batch missing segment %q", cmd.Segments[index].ID)
				}
				out[index].Text = text
			}
			if fallbackProvider == "" {
				fallbackProvider = res.UsedProvider
			}
		}
		provider := fallbackProvider
		if primaryProvider != "" {
			provider = "mixed"
		}
		return BatchTranslationResult{Segments: out, UsedModel: "", UsedProvider: provider, CacheStatus: "miss"}, nil
	}

	for _, index := range pending {
		res, err := f.fallback.Translate(ctx, TranslationCommand{
			SourceLang: cmd.SourceLang,
			TargetLang: cmd.TargetLang,
			Text:       cmd.Segments[index].Text,
		})
		if err != nil {
			return BatchTranslationResult{}, err
		}
		if res.TranslatedText == "" {
			return BatchTranslationResult{}, fmt.Errorf("translation: fallback provider returned empty output")
		}
		out[index].Text = res.TranslatedText
	}
	provider := ""
	if primaryProvider != "" {
		provider = "mixed"
	}
	return BatchTranslationResult{Segments: out, UsedProvider: provider, CacheStatus: "miss"}, nil
}

// Compile-time assertion: *FallbackTranslator satisfies TranslationPort.
var _ TranslationPort = (*FallbackTranslator)(nil)

// Compile-time assertion: *FallbackTranslator satisfies BatchTranslationPort.
var _ BatchTranslationPort = (*FallbackTranslator)(nil)
