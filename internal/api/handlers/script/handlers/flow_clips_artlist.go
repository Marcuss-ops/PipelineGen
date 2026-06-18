package handlers

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/association"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// ── SearchArtlistClips ───────────────────────────────────────────────────────

// SearchArtlistClips processes each artlist phrase in parallel, translating
// to English and searching Qdrant for matching Artlist clips.
func SearchArtlistClips(ctx context.Context, svc ClipServices, title string, phrases []string) []ScriptArtlistClipSuggestion {
	// Pre-filter empty phrases so ParallelMap doesn't waste slots on no-ops.
	validPhrases := make([]string, 0, len(phrases))
	for _, p := range phrases {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			validPhrases = append(validPhrases, trimmed)
		}
	}
	if len(validPhrases) == 0 {
		return nil
	}

	artlistTarget := []assetSearchTarget{{source: "artlist", mediaType: "video"}}

	concurrency := 5
	if concurrency > len(validPhrases) {
		concurrency = len(validPhrases)
	}

	results := concurrent.ParallelMap(validPhrases, concurrency, func(idx int, phrase string) artlistPhraseResult {
		enPhrase := artlistSearchPhrase(ctx, svc, phrase)
		folderLink, folderName, folderID := resolveArtlistFolderForPhrase(ctx, svc, phrase)
		suggestion := ScriptArtlistClipSuggestion{
			Phrase:     phrase,
			FolderLink: folderLink,
			FolderName: folderName,
			FolderID:   folderID,
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

// artlistPhraseResult holds the result of processing a single artlist phrase.
type artlistPhraseResult struct {
	suggestion ScriptArtlistClipSuggestion
	valid      bool
}

// artlistSearchPhrase translates an artlist phrase to English for Qdrant search.
func artlistSearchPhrase(ctx context.Context, svc ClipServices, phrase string) string {
	if svc.Translator == nil || strings.TrimSpace(phrase) == "" {
		return phrase
	}
	translated, err := svc.Translator.TranslateTextWithModel(ctx, phrase, "english", svc.MetadataModel)
	if err != nil || strings.TrimSpace(translated) == "" {
		if svc.Logger != nil {
			svc.Logger.Debug("artlist phrase translation failed, keeping original",
				zap.String("phrase", phrase),
				zap.Error(err),
			)
		}
		return phrase
	}
	if svc.Logger != nil {
		svc.Logger.Debug("artlist phrase translated for Qdrant search",
			zap.String("original", phrase),
			zap.String("english", translated),
		)
	}
	return translated
}

// resolveArtlistFolderForPhrase finds the Drive folder associated with a phrase.
func resolveArtlistFolderForPhrase(ctx context.Context, svc ClipServices, phrase string) (string, string, string) {
	if svc.AssocSvc == nil || strings.TrimSpace(phrase) == "" {
		return "", "", ""
	}
	req := association.CandidatesRequest{
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

// enqueueArtlistBackgroundJob enqueues an Artlist download job for a phrase.
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
	if _, err := svc.JobsSvc.Enqueue(bgCtx, &jobservice.EnqueueRequest{
		Type:       models.JobTypeArtlistRun,
		Payload:    payload,
		MaxRetries: 2,
	}); err != nil && svc.Logger != nil {
		svc.Logger.Warn("failed to enqueue background artlist job",
			zap.String("phrase", phrase),
			zap.Error(err),
		)
	}
}
