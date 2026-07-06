// Package usecase — artlist translation and search helpers.
//
// translation.go owns the artlist-phrase → English → Qdrant search
// pipeline: SearchArtlistClips, artlistSearchPhrase,
// resolveArtlistFolderForPhrase, enqueueArtlistBackgroundJob.
// Extracted from flow_helpers.go
// (July 2026, LONG-FILES-SPLIT-2026-07-06).
package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// artlistPhraseResult is the internal envelope for parallel artlist search results.
type artlistPhraseResult struct {
	suggestion ScriptArtlistClipSuggestion
	valid      bool
}

// ── SearchArtlistClips ──────────────────────────────────────────────────────

// SearchArtlistClips processes each artlist phrase in parallel, translating
// to English and searching Qdrant for matching Artlist clips.
func SearchArtlistClips(ctx context.Context, svc ClipServices, title string, phrases []string) []ScriptArtlistClipSuggestion {
	validPhrases := make([]string, 0, len(phrases))
	for _, p := range phrases {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			validPhrases = append(validPhrases, trimmed)
		}
	}
	if len(validPhrases) == 0 {
		return nil
	}

	artlistTarget := []AssetSearchTarget{{Source: "artlist", MediaType: "video"}}

	concurrency := 5
	if concurrency > len(validPhrases) {
		concurrency = len(validPhrases)
	}

	results := concurrent.ParallelMap(validPhrases, concurrency, func(idx int, phrase string) artlistPhraseResult {
		// Resolve the Drive folder before the translation step: that
		// lookup only needs the user-supplied phrase and is unaffected
		// by translation success/failure. If the translation then
		// fails, the suggestion still carries FolderLink/FolderID/Name
		// for the operator audit trail.
		folderLink, folderName, folderID := resolveArtlistFolderForPhrase(ctx, svc, phrase)
		suggestion := ScriptArtlistClipSuggestion{
			Phrase:     phrase,
			FolderLink: folderLink,
			FolderName: folderName,
			FolderID:   folderID,
		}
		enPhrase, txErr := artlistSearchPhrase(ctx, svc, phrase)
		if txErr != nil {
			// P0.6 (June 2026): silent translation failure is banned
			// (godlike/07). Surface the error on the suggestion and
			// intentionally leave Clips empty / do not enqueue a
			// background job with the original phrase. The user-
			// supplied Phrase stays populated so the API response is
			// contract-stable (callers can decide whether to retry).
			suggestion.TranslationError = txErr.Error()
			return artlistPhraseResult{suggestion: suggestion, valid: true}
		}
		cq := contextualQuery(title, enPhrase)
		clips := SearchScriptAssets(ctx, svc, []string{cq, enPhrase}, artlistTarget, 2)
		if len(clips) > 0 {
			suggestion.Clips = clips
			return artlistPhraseResult{suggestion: suggestion, valid: true}
		}
		enqueueArtlistBackgroundJob(ctx, svc, enPhrase)
		return artlistPhraseResult{suggestion: suggestion, valid: true}
	})

	out := make([]ScriptArtlistClipSuggestion, 0, len(results))
	for _, r := range results {
		if r.valid {
			out = append(out, r.suggestion)
		}
	}
	return out
}

// artlistSearchPhrase translates an artlist phrase to "english" so the
// downstream Qdrant search runs over the canonical English form.
//
// Per P0.6 (June 2026) the silent-success "fallback to input phrase" anti-pattern
// was removed (godlike/07 no-fake-availability). On translator error or empty
// translation output, this function now returns ("", err) — the caller
// decides what to do (e.g. surface via ScriptArtlistClipSuggestion.TranslationError
// and skip the search).
//
// Fase 9 step 2 (July 2026, Spina Dorsale): migrated from the legacy
// 4-arg `svc.Translator.TranslateTextWithModel` (TranslatorService
// straggler) to the canonical `svc.TranslationPort.Translate(ctx, cmd)`
// surface. The legacy `Svc.Translator` field stays populated for
// the godlike/07 EXPAND window per the umbrella deprecation record
// architecture/deprecations.yaml#TRANSLATION-LEGACY-SERVICES-MIGRATION.
func artlistSearchPhrase(ctx context.Context, svc ClipServices, phrase string) (string, error) {
	if svc.TranslationPort == nil {
		return "", fmt.Errorf("artlist translator not configured")
	}
	if strings.TrimSpace(phrase) == "" {
		return "", fmt.Errorf("artlist phrase is empty")
	}

	// Resolve the effective model via ModelPolicy: cmd.ModelPolicy.Model
	// wins when set (server default-token "ollama"+model string from
	// svc.MetadataModel). cmd.SourceLang left empty — the underlying
	// gen.TranslateTextWithModel consumes only TargetLang; leaving
	// SourceLang empty signals "inference" to downstream auditors
	// without affecting wire behavior of the legacy gen method.
	var modelPolicy *translation.ModelPolicy
	if svc.MetadataModel != "" {
		modelPolicy = &translation.ModelPolicy{
			Provider: "ollama",
			Model:    svc.MetadataModel,
		}
	}

	cmd := translation.TranslationCommand{
		SourceLang:  "",
		TargetLang:  "english",
		Text:        phrase,
		ModelPolicy: modelPolicy,
	}

	result, err := svc.TranslationPort.Translate(ctx, cmd)
	if err != nil {
		if svc.Logger != nil {
			svc.Logger.Debug("artlist phrase translation failed",
				zap.String("phrase", phrase),
				zap.Error(err),
			)
		}
		return "", fmt.Errorf("artlist phrase translation: %w", err)
	}
	translated := result.TranslatedText
	if strings.TrimSpace(translated) == "" {
		if svc.Logger != nil {
			svc.Logger.Debug("artlist phrase translation returned empty",
				zap.String("phrase", phrase),
			)
		}
		return "", fmt.Errorf("artlist phrase translation returned empty output for %q", phrase)
	}
	if svc.Logger != nil {
		svc.Logger.Debug("artlist phrase translated for Qdrant search",
			zap.String("original", phrase),
			zap.String("english", translated),
		)
	}
	return translated, nil
}

func resolveArtlistFolderForPhrase(ctx context.Context, svc ClipServices, phrase string) (string, string, string) {
	if svc.AssocSvc == nil || strings.TrimSpace(phrase) == "" {
		return "", "", ""
	}
	req := AssociationCandidatesRequest{
		Topic:    phrase,
		Subject:  phrase,
		Keywords: []string{phrase},
		TopK:     1,
	}
	resp, err := svc.AssocSvc.BuildCandidates(ctx, req)
	if err != nil || resp == nil || len(resp.Candidates) == 0 {
		return "", "", ""
	}
	candidate := resp.Candidates[0]
	folderID := strings.TrimSpace(candidate.FolderID)
	link := strings.TrimSpace(candidate.Link)
	if link == "" && folderID != "" {
		link = "https://drive.google.com/drive/folders/" + folderID
	}
	if folderID != "" && svc.DriveSvc != nil {
		active, checkErr := svc.DriveSvc.FileIsNotTrashed(ctx, folderID)
		if checkErr != nil || !active {
			return "", "", ""
		}
	}
	return link, strings.TrimSpace(candidate.Name), folderID
}

func enqueueArtlistBackgroundJob(ctx context.Context, svc ClipServices, phrase string) {
	if svc.JobsSvc == nil || svc.ArtlistFolder == "" {
		return
	}
	if svc.ArtlistFolder == "" {
		if svc.Logger != nil {
			svc.Logger.Warn("skipping background artlist job: no root folder configured",
				zap.String("phrase", phrase),
			)
		}
		return
	}
	bgCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	payload := map[string]any{
		"term":           phrase,
		"limit":          3,
		"root_folder_id": svc.ArtlistFolder,
	}
	if _, err := svc.JobsSvc.Enqueue(bgCtx, &job.EnqueueRequest{
		Type:       "media.artlist",
		Payload:    payload,
		MaxRetries: 2,
	}); err != nil && svc.Logger != nil {
		svc.Logger.Warn("failed to enqueue background artlist job",
			zap.String("phrase", phrase),
			zap.Error(err),
		)
	}
}
