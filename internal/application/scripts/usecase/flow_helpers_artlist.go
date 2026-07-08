// Package usecase — flow_helpers_artlist.go: artlist-phrase helpers.
//
// Owns: artlistSearchPhrase + SearchArtlistClips.
//
// This file restores the canonical helper surface that was split out of
// flow_helpers.go. ScriptInsightBuilder still calls SearchArtlistClips,
// and the package tests pin the no-silent-fallback translation contract.
package usecase

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"

	translation "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// SearchArtlistClips translates each candidate phrase and returns
// per-phrase artlist suggestions. A translation failure is explicit:
// the phrase is preserved, Clips stays empty, and TranslationError is
// populated so callers never silently search on the untranslated input.
func SearchArtlistClips(ctx context.Context, svc ClipServices, title string, phrases []string) []ScriptArtlistClipSuggestion {
	if len(phrases) == 0 {
		return nil
	}

	out := make([]ScriptArtlistClipSuggestion, 0, len(phrases))
	for _, phrase := range sliceutil.UniqueLimitedStrings(phrases, len(phrases)) {
		phrase = strings.TrimSpace(phrase)
		if phrase == "" {
			continue
		}

		translated, err := artlistSearchPhrase(ctx, svc, phrase)
		sug := ScriptArtlistClipSuggestion{Phrase: phrase}
		if err != nil || translated == "" {
			if err != nil {
				sug.TranslationError = err.Error()
			}
			out = append(out, sug)
			continue
		}

		if svc.RealtimeSvc != nil {
			targets := []AssetSearchTarget{{Source: "artlist", MediaType: "video"}}
			queries := sliceutil.UniqueLimitedStrings([]string{translated, contextualQuery(title, translated)}, 2)
			clips := SearchScriptAssets(ctx, svc, queries, targets, 2)
			sug.Clips = clips
			if len(clips) > 0 {
				// Best-effort fold the top result into the legacy
				// artlist-facing folder fields so callers get a usable
				// link when the realtime search returns artlist hits.
				top := clips[0]
				sug.FolderLink = strings.TrimSpace(top.DriveLink)
				sug.FolderName = strings.TrimSpace(top.Name)
				sug.FolderID = strings.TrimSpace(top.ID)
			}
		}
		out = append(out, sug)
	}
	return out
}

// artlistSearchPhrase translates a phrase to English for the artlist
// search path. It prefers the forward-compatible TranslationPort when
// available, then falls back to the legacy Translator service, and
// finally the legacy 3-arg TextTranslationService. Any failure is
// surfaced explicitly; callers must not silently reuse the source text.
func artlistSearchPhrase(ctx context.Context, svc ClipServices, phrase string) (string, error) {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return "", errors.New("artlist search phrase is empty")
	}

	if svc.TranslationPort != nil {
		res, err := svc.TranslationPort.Translate(ctx, translationCommandForArtlist(phrase))
		if err != nil {
			if svc.Logger != nil {
				svc.Logger.Warn("artlist translation failed via TranslationPort", zap.String("phrase", phrase), zap.Error(err))
			}
			return "", err
		}
		translated := strings.TrimSpace(res.TranslatedText)
		if translated == "" {
			return "", errors.New("artlist translation returned empty text")
		}
		return translated, nil
	}

	if svc.Translator != nil {
		model := strings.TrimSpace(svc.MetadataModel)
		if model == "" {
			model = "default"
		}
		translated, err := svc.Translator.TranslateTextWithModel(ctx, phrase, "en", model)
		if err != nil {
			if svc.Logger != nil {
				svc.Logger.Warn("artlist translation failed via TranslatorService", zap.String("phrase", phrase), zap.Error(err))
			}
			return "", err
		}
		translated = strings.TrimSpace(translated)
		if translated == "" {
			return "", errors.New("artlist translation returned empty text")
		}
		return translated, nil
	}

	if svc.Translation != nil {
		translated, err := svc.Translation.TranslateText(ctx, phrase, "en")
		if err != nil {
			if svc.Logger != nil {
				svc.Logger.Warn("artlist translation failed via TextTranslationService", zap.String("phrase", phrase), zap.Error(err))
			}
			return "", err
		}
		translated = strings.TrimSpace(translated)
		if translated == "" {
			return "", errors.New("artlist translation returned empty text")
		}
		return translated, nil
	}

	return "", errors.New("artlist translation unavailable: no translator service wired")
}

func translationCommandForArtlist(phrase string) translation.TranslationCommand {
	return translation.TranslationCommand{
		SourceLang: "auto",
		TargetLang: "en",
		Text:       phrase,
	}
}
