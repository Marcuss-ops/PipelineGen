// Package scripts — flow helpers extracted from api/script/flow.go (PR2, June 2026).
//
// These are the standalone functions that don't use ScriptFlowHandler as a
// receiver. They take ClipServices and other dependencies as explicit params.
package scripts

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Local type stubs for removed packages ───────────────────────────────────
// realtime.MatchAsset and association.CandidatesRequest were defined in
// packages that no longer exist (removed from remote, June 2026). These
// local types preserve compilation until the real types are restored.

// RealtimeMatchAsset mirrors the removed realtime.MatchAsset.
type RealtimeMatchAsset struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Source    string  `json:"source"`
	Score     float64 `json:"score"`
	DriveLink string  `json:"drive_link"`
}

// AssociationCandidatesRequest mirrors the removed association.CandidatesRequest.
type AssociationCandidatesRequest struct {
	Topic     string   `json:"topic"`
	Subject   string   `json:"subject"`
	Narrative string   `json:"narrative"`
	Keywords  []string `json:"keywords"`
	Entities  []string `json:"entities"`
	TopK      int      `json:"top_k"`
}

// AssociationCandidate mirrors a single candidate from the removed association package.
type AssociationCandidate struct {
	Database string  `json:"database"`
	Source   string  `json:"source"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Link     string  `json:"link"`
	FolderID string  `json:"folder_id"`
	Score    float64 `json:"score"`
	Reason   string  `json:"reason"`
}

// AssociationCandidatesResponse mirrors the removed association response.
type AssociationCandidatesResponse struct {
	Candidates []AssociationCandidate `json:"candidates"`
}

// ── SearchScriptAssets ──────────────────────────────────────────────────────

// SearchScriptAssets searches for assets across multiple query-target pairs
// and returns the top suggestions. Falls back to auto-harvest when empty.
func SearchScriptAssets(ctx context.Context, svc ClipServices, queries []string, targets []AssetSearchTarget, limit int) []ScriptAssetSuggestion {
	if svc.RealtimeSvc == nil || len(queries) == 0 || len(targets) == 0 {
		return nil
	}

	topicKeywords := ""
	for _, q := range queries {
		cleaned := extractTopicKeywords(q)
		if len(strings.Fields(cleaned)) > len(strings.Fields(topicKeywords)) {
			topicKeywords = cleaned
		}
	}

	seen := make(map[string]struct{})
	out := make([]ScriptAssetSuggestion, 0, limit)

	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		for _, target := range targets {
			assets, err := svc.RealtimeSvc.SearchClips(ctx, query, target.Source, target.MediaType, limit, 0.7)
			if err != nil {
				continue
			}
			remaining := limit - len(out)
			if remaining <= 0 {
				return out
			}
			results := filterSearchAssets(assets, topicKeywords, seen, remaining)
			out = append(out, results...)
			if len(out) >= limit {
				return out
			}
		}
	}

	if len(out) == 0 && svc.HarvestSvc != nil && len(queries) > 0 {
		for _, q := range queries {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			if svc.Logger != nil {
				svc.Logger.Info("auto-harvest triggered: no clips found for query",
					zap.String("query", q))
			}
			svc.HarvestSvc.EnqueueHarvest(ctx, q, 3, "youtube_1080p_7s")
		}
	}

	return out
}

func filterSearchAssets(matches []RealtimeMatchAsset, topicKeywords string, seen map[string]struct{}, limit int) []ScriptAssetSuggestion {
	out := make([]ScriptAssetSuggestion, 0, minInt(limit, len(matches)))
	for _, asset := range matches {
		if len(out) >= limit {
			break
		}
		if _, ok := seen[asset.ID]; ok {
			continue
		}
		if asset.Source != "artlist" && !topicRelevant(asset.Name, topicKeywords) {
			continue
		}
		seen[asset.ID] = struct{}{}
		out = append(out, ScriptAssetSuggestion{
			ID:        asset.ID,
			Name:      asset.Name,
			Source:    asset.Source,
			Score:     asset.Score,
			DriveLink: asset.DriveLink,
		})
	}
	return out
}

func topicRelevant(assetName, topicKeywords string) bool {
	if topicKeywords == "" {
		return true
	}
	nameLower := strings.ToLower(assetName)
	topicWords := strings.Fields(topicKeywords)
	for _, w := range topicWords {
		if len(w) < 4 {
			continue
		}
		if strings.Contains(nameLower, w) {
			return true
		}
		if len(w) >= 4 {
			for _, nw := range strings.Fields(nameLower) {
				if len(nw) < 4 {
					continue
				}
				if w[:3] == nw[:3] {
					return true
				}
			}
		}
	}
	return false
}

// ── SearchArtlistClips ──────────────────────────────────────────────────────

type artlistPhraseResult struct {
	suggestion ScriptArtlistClipSuggestion
	valid      bool
}

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
		Type:       "artlist.run",
		Payload:    payload,
		MaxRetries: 2,
	}); err != nil && svc.Logger != nil {
		svc.Logger.Warn("failed to enqueue background artlist job",
			zap.String("phrase", phrase),
			zap.Error(err),
		)
	}
}

// ── BuildPhraseClipSuggestions + SearchIntroClips ───────────────────────────

// BuildPhraseClipSuggestions searches for clips matching each important phrase.
func BuildPhraseClipSuggestions(ctx context.Context, svc ClipServices, title string, insights ScriptInsights, targets []AssetSearchTarget) []ScriptPhraseClipSuggestion {
	if svc.RealtimeSvc == nil || len(targets) == 0 {
		return nil
	}

	phrases := sliceutil.UniqueLimitedStrings(insights.ImportantPhrases, 5)
	out := make([]ScriptPhraseClipSuggestion, 0, len(phrases))
	for _, phrase := range phrases {
		localQuery := extractSearchKeywords(phrase, title, insights.SpecialNames)
		if localQuery == "" {
			continue
		}
		topicQuery := contextualQuery(title, localQuery)
		queries := []string{topicQuery, localQuery}
		queries = sliceutil.UniqueLimitedStrings(queries, 2)
		clips := SearchScriptAssets(ctx, svc, queries, targets, 1)
		if len(clips) == 0 {
			continue
		}
		out = append(out, ScriptPhraseClipSuggestion{
			Phrase: phrase,
			Clips:  clips,
		})
		if len(out) >= 5 {
			break
		}
	}
	return out
}

// SearchIntroClips searches for intro clip candidates matching the topic.
func SearchIntroClips(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights, targets []AssetSearchTarget) []ScriptAssetSuggestion {
	if svc.RealtimeSvc == nil || len(targets) == 0 {
		return nil
	}

	queries := make([]string, 0, 6)
	if t := strings.TrimSpace(title); t != "" {
		queries = append(queries, t)
	}
	sentences := textutil.SplitScriptSentences(script)
	if len(sentences) > 0 {
		queries = append(queries, sentences[:sliceutil.MinInt(2, len(sentences))]...)
	}
	if len(insights.SpecialNames) > 0 {
		queries = append(queries, insights.SpecialNames[:sliceutil.MinInt(3, len(insights.SpecialNames))]...)
	}
	queries = sliceutil.UniqueLimitedStrings(queries, 6)
	if len(queries) == 0 {
		return nil
	}
	return SearchScriptAssets(ctx, svc, queries, targets, 2)
}

// ── Query helpers ────────────────────────────────────────────────────────────

func extractSearchKeywords(phrase, title string, specialNames []string) string {
	var keywords []string
	for _, name := range specialNames {
		if textutil.ContainsCI(phrase, name) {
			keywords = append(keywords, name)
		}
	}
	if title != "" {
		for _, w := range strings.Fields(title) {
			if len(w) < 3 {
				continue
			}
			if textutil.ContainsCI(phrase, w) {
				keywords = append(keywords, w)
			}
		}
	}
	if len(keywords) < 3 {
		for _, w := range strings.Fields(phrase) {
			clean := strings.Trim(strings.ToLower(w), ".,;:!?\"'")
			if len(clean) < 3 || textutil.IsStopWord(clean) {
				continue
			}
			keywords = append(keywords, clean)
		}
	}
	keywords = sliceutil.UniqueLimitedStrings(keywords, 4)
	return strings.Join(keywords, " ")
}

func extractTopicKeywords(title string) string {
	if title == "" {
		return ""
	}
	words := strings.Fields(title)
	var kept []string
	for _, w := range words {
		clean := strings.Trim(strings.ToLower(w), ".,;:!?\"'()")
		if len(clean) < 3 || textutil.IsStopWord(clean) {
			continue
		}
		kept = append(kept, clean)
	}
	if len(kept) > 7 {
		kept = kept[:7]
	}
	return strings.Join(kept, " ")
}

func contextualQuery(title, phrase string) string {
	keywords := extractTopicKeywords(title)
	if keywords == "" {
		return phrase
	}
	return keywords + " " + phrase
}

// ── Entity image enrichment ─────────────────────────────────────────────────

// EnrichSpecialNamesWithImages searches for or generates images for each special name.
func EnrichSpecialNamesWithImages(ctx context.Context, svc ClipServices, specialNames []string) []ScriptEntityImage {
	if svc.ImgSvc == nil || len(specialNames) == 0 {
		return nil
	}

	var mu sync.Mutex
	results := make([]ScriptEntityImage, 0, len(specialNames))
	group, groupCtx := concurrent.WithContext(ctx)

	for _, name := range specialNames {
		name := strings.TrimSpace(name)
		if name == "" || len(name) < 2 {
			continue
		}

		group.Go("entity-image-"+name, func() error {
			img := enrichSingleEntity(groupCtx, svc, name)
			mu.Lock()
			results = append(results, img)
			mu.Unlock()
			return nil
		})
	}

	_ = group.Wait()
	return results
}

func enrichSingleEntity(ctx context.Context, svc ClipServices, name string) ScriptEntityImage {
	img := ScriptEntityImage{EntityName: name}

	if isLikelyNonEntityWord(name) {
		if svc.Logger != nil {
			svc.Logger.Debug("Skipping non-entity word", zap.String("name", name))
		}
		img.Error = "skipped: not a named entity"
		return img
	}

	entityCtx, entityCancel := context.WithTimeout(ctx, 90*time.Second)
	defer entityCancel()

	asset, err := svc.ImgSvc.SearchAndDownload(entityCtx, name, name, name, "en", nil)
	if err == nil && asset != nil {
		populateEntityImage(&img, asset, "")
		return img
	}
	if svc.Logger != nil {
		svc.Logger.Info("Web search found no image for entity, trying AI generation",
			zap.String("entity", name),
			zap.Error(err),
		)
	}

	aiCtx, aiCancel := context.WithTimeout(ctx, 60*time.Second)
	defer aiCancel()
	aiAsset, aiErr := svc.ImgSvc.GenerateSmartImage(aiCtx, name,
		"Portrait or representative image of "+name,
		"realistic",
		[]string{"Portrait or representative image of " + name},
		[]string{"entity", name}, 1024, 1024, "", false)
	if aiErr == nil && aiAsset != nil {
		populateEntityImage(&img, aiAsset, "ai")
		return img
	}

	if svc.Logger != nil {
		svc.Logger.Warn("Both web search and AI generation failed for entity",
			zap.String("entity", name),
			zap.Error(aiErr),
		)
	}
	img.Error = "no image found (web search and AI generation both failed)"
	return img
}

func isLikelyNonEntityWord(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || len(name) < 3 {
		return true
	}
	first := name[0]
	if first >= 'a' && first <= 'z' {
		return true
	}
	return false
}

func populateEntityImage(img *ScriptEntityImage, asset *asset.ImageAsset, forcedSource string) {
	img.ImageHash = asset.Hash
	img.ImageURL = asset.SourceURL
	img.PathRel = asset.PathRel
	img.Description = asset.Description
	if forcedSource != "" {
		img.Source = forcedSource
	} else {
		img.Source = extractSourceFromMeta(asset.MetadataJSON)
	}
	fileID := strings.TrimSpace(asset.DriveFileID)
	if fileID != "" {
		img.DriveLink = drive.FileURLFromID(fileID)
	}
}

func extractSourceFromMeta(metaJSON string) string {
	if metaJSON == "" || metaJSON == "{}" {
		return "web"
	}
	meta := strings.ToLower(metaJSON)
	switch {
	case strings.Contains(meta, "\"wikipedia\""):
		return "wikipedia"
	case strings.Contains(meta, "\"searxng\""):
		return "searxng"
	case strings.Contains(meta, "\"duckduckgo\""):
		return "duckduckgo"
	default:
		return "web"
	}
}

// ── Entity extraction ───────────────────────────────────────────────────────

// ExtractScriptEntities extracts entities from a script text and returns
// the JSON-serialized entity analysis.
func ExtractScriptEntities(ctx context.Context, extractor EntityScriptExtractor, script string, model string) (string, error) {
	if extractor == nil {
		return "", nil
	}

	segments := textutil.SplitScriptSentences(script)
	if len(segments) == 0 {
		script = strings.TrimSpace(script)
		if script != "" {
			segments = []string{script}
		}
	}
	if len(segments) > 12 {
		segments = sliceutil.GroupSentences(segments, 4)
	}

	analysis, err := extractor.ExtractEntitiesFromScriptWithModel(ctx, segments, 12, model)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(analysis)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ── BuildTextOnlyScriptPlan ─────────────────────────────────────────────────

// BuildTextOnlyScriptPlan builds a plan for text-only script generation.
func BuildTextOnlyScriptPlan(
	topic, sourceText, guidelines, title, language, tone, model string,
	forceRefresh, saveToDB bool, targetWords int,
	promptVersion, editorPromptVersion, qaPromptVersion string,
) *scriptpkg.ScriptGenerationPlan {
	if topic == "" {
		topic = sourceText
	}
	if title == "" {
		title = topic
	}

	plan := &scriptpkg.ScriptGenerationPlan{
		Title:               title,
		Topic:               topic,
		Language:            language,
		Tone:                tone,
		Model:               model,
		Mode:                "generate",
		UseMemory:           !forceRefresh,
		SaveToDB:            saveToDB,
		TargetWords:         targetWords,
		Prompt:              topic,
		SourceText:          sourceText,
		Guidelines:          guidelines,
		PromptVersion:       promptVersion,
		EditorPromptVersion: editorPromptVersion,
		QAPromptVersion:     qaPromptVersion,
	}
	return plan
}

// ── ResolveRecommendedDriveFolder ────────────────────────────────────────────

// ResolveRecommendedDriveFolder recommends a Drive folder for the script
// based on topic, keywords, and entities.
func ResolveRecommendedDriveFolder(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights) *ScriptDriveFolderSuggestion {
	if svc.AssocSvc == nil {
		return nil
	}

	keywords := make([]string, 0, 12)
	keywords = append(keywords, insights.ImportantWords...)
	keywords = append(keywords, insights.ImportantPhrases...)
	keywords = append(keywords, insights.ArtlistPhrases...)
	keywords = sliceutil.UniqueLimitedStrings(keywords, 12)

	entities := sliceutil.UniqueLimitedStrings(insights.SpecialNames, 12)
	req := AssociationCandidatesRequest{
		Topic:     strings.TrimSpace(title),
		Subject:   strings.TrimSpace(title),
		Narrative: strings.TrimSpace(script),
		Keywords:  keywords,
		Entities:  entities,
		TopK:      5,
	}
	resp, err := svc.AssocSvc.BuildCandidates(ctx, req)
	if err != nil || resp == nil || len(resp.Candidates) == 0 {
		return nil
	}

	for _, candidate := range resp.Candidates {
		folderID := strings.TrimSpace(candidate.FolderID)
		if folderID != "" && svc.DriveSvc != nil {
			active, checkErr := svc.DriveSvc.FileIsNotTrashed(ctx, folderID)
			if checkErr != nil {
				if svc.Logger != nil {
					svc.Logger.Warn("failed to check if drive folder is trashed, skipping",
						zap.String("folder_id", folderID),
						zap.String("folder_name", candidate.Name),
						zap.Error(checkErr),
					)
				}
				continue
			}
			if !active {
				if svc.Logger != nil {
					svc.Logger.Info("skipping trashed drive folder",
						zap.String("folder_id", folderID),
						zap.String("folder_name", candidate.Name),
					)
				}
				continue
			}
		}
		link := strings.TrimSpace(candidate.Link)
		if link == "" && folderID != "" {
			link = "https://drive.google.com/drive/folders/" + folderID
		}
		return &ScriptDriveFolderSuggestion{
			Database: strings.TrimSpace(candidate.Database),
			Source:   strings.TrimSpace(candidate.Source),
			Name:     strings.TrimSpace(candidate.Name),
			Path:     strings.TrimSpace(candidate.Path),
			Link:     link,
			FolderID: folderID,
			Score:    int(candidate.Score),
			Reason:   strings.TrimSpace(candidate.Reason),
		}
	}

	return nil
}

// minInt is a local helper (avoid import cycle from sliceutil).
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
