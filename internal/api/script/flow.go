// Package script (api/script) — flow.go carries the post-generation
// flow-logic helpers extracted from the now-collapsed flow_*.go files.
//
// PR3 (June 2026): this file consolidates seven prior files:
//
//   flow_clips_search.go       (SearchScriptAssets, filterSearchAssets, topicRelevant)
//   flow_clips_artlist.go      (SearchArtlistClips + artlist helpers)
//   flow_clips_helpers.go      (BuildPhraseClipSuggestions, SearchIntroClips, query helpers)
//   flow_entity_images.go      (EnrichSpecialNamesWithImages, ScriptEntityImage)
//   flow_entities.go           (ExtractScriptEntities, EntityScriptExtractor)
//   flow_insights.go           (ScriptInsightBuilder + suggestion types + ResolveRecommendedDriveFolder)
//   flow_shared_helpers.go     (resolveDriveFolderID + findFolderByNameDeep + buildTextOnlyScriptPlan)
//
// Exports preserved 1:1: existing callers (handler_flow.go::ScriptFlowHandler
// and handler_jobs.go) reference these symbols verbatim — no API churn.
package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/association"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Asset search target (shared between search/intro flows) ───────────────

type assetSearchTarget struct {
	source    string
	mediaType string
}

// ── SearchScriptAssets (flow_clips_search.go → flow.go) ─────────────────────

// SearchScriptAssets searches for assets across multiple query-target pairs
// and returns the top suggestions. Falls back to auto-harvest when empty.
func SearchScriptAssets(ctx context.Context, svc ClipServices, queries []string, targets []assetSearchTarget, limit int) []ScriptAssetSuggestion {
	if svc.RealtimeSvc == nil || len(queries) == 0 || len(targets) == 0 {
		return nil
	}

	// Extract clean topic keywords (stop words removed) from the longest
	// query for post-filtering.
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
			assets, err := svc.RealtimeSvc.SearchClips(ctx, query, target.source, target.mediaType, limit, 0.7)
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

	// Auto-harvest: if no clips found and harvest service is available,
	// enqueue background download jobs for the search terms.
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

// filterSearchAssets (lowercased helper, was filterSearchAssets in flow_clips_search.go)
func filterSearchAssets(matches []realtime.MatchAsset, topicKeywords string, seen map[string]struct{}, limit int) []ScriptAssetSuggestion {
	out := make([]ScriptAssetSuggestion, 0, min(limit, len(matches)))
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

// ── SearchArtlistClips (flow_clips_artlist.go → flow.go) ────────────────────

// artlistPhraseResult holds the result of processing a single artlist phrase.
type artlistPhraseResult struct {
	suggestion ScriptArtlistClipSuggestion
	valid      bool
}

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

// ── BuildPhraseClipSuggestions + SearchIntroClips (flow_clips_helpers.go → flow.go) ──

// BuildPhraseClipSuggestions searches for clips matching each important phrase.
func BuildPhraseClipSuggestions(ctx context.Context, svc ClipServices, title string, insights ScriptInsights, targets []assetSearchTarget) []ScriptPhraseClipSuggestion {
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
func SearchIntroClips(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights, targets []assetSearchTarget) []ScriptAssetSuggestion {
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

// ── Entity image enrichment (flow_entity_images.go → flow.go) ──────────────

// ScriptEntityImage represents an enriched image for a named entity.
type ScriptEntityImage struct {
	EntityName  string `json:"entity_name"`
	ImageHash   string `json:"image_hash,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	PathRel     string `json:"path_rel,omitempty"`
	Source      string `json:"source,omitempty"`
	Description string `json:"description,omitempty"`
	Error       string `json:"error,omitempty"`
}

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

// ── Entity extraction (flow_entities.go → flow.go) ──────────────────────────

// EntityScriptExtractor extracts entities from a script.
type EntityScriptExtractor interface {
	ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, entityCount int, model string) (*asset.FullEntityAnalysis, error)
}

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

// ── Script insights + suggestion types (flow_insights.go → flow.go) ─────────

// ScriptInsightBuilder builds structured ScriptInsights from extracted entities.
// Uses ClipServices to delegate sub-operations to standalone functions.
type ScriptInsightBuilder struct {
	Logger      *zap.Logger
	MaxEntities int
	Services    ClipServices
}

// Build constructs ScriptInsights from the entity analysis JSON.
func (b *ScriptInsightBuilder) Build(ctx context.Context, title, script, entitiesJSON string) ScriptInsights {
	insights := ScriptInsights{
		ImportantWords:   []string{},
		ImportantPhrases: []string{},
		SpecialNames:     []string{},
		ArtlistPhrases:   []string{},
	}

	var analysis asset.FullEntityAnalysis
	if err := json.Unmarshal([]byte(strings.TrimSpace(entitiesJSON)), &analysis); err == nil {
		for _, segment := range analysis.SegmentEntities {
			insights.ImportantWords = sliceutil.AppendUniqueStrings(insights.ImportantWords, segment.ParoleImportanti...)
			insights.ImportantPhrases = sliceutil.AppendUniqueStrings(insights.ImportantPhrases, segment.FrasiImportanti...)
			insights.SpecialNames = sliceutil.AppendUniqueStrings(insights.SpecialNames, segment.NomiSpeciali...)
			insights.ArtlistPhrases = sliceutil.AppendUniqueStrings(insights.ArtlistPhrases, segment.ArtlistPhrases...)
		}
	}
	limit := 12
	if b.MaxEntities > 0 {
		limit = b.MaxEntities
	}
	insights.ImportantWords = sliceutil.UniqueLimitedStrings(insights.ImportantWords, limit)
	insights.ImportantPhrases = sliceutil.UniqueLimitedStrings(insights.ImportantPhrases, limit)
	insights.SpecialNames = sliceutil.UniqueLimitedStrings(insights.SpecialNames, limit)
	insights.ArtlistPhrases = sliceutil.UniqueLimitedStrings(insights.ArtlistPhrases, limit)

	if len(insights.ArtlistPhrases) > 0 {
		insights.ArtlistClipSuggestions = SearchArtlistClips(ctx, b.Services, title, insights.ArtlistPhrases)
	}

	if len(insights.SpecialNames) > 0 {
		insights.EntityImages = EnrichSpecialNamesWithImages(ctx, b.Services, insights.SpecialNames)
	}

	if folder := ResolveRecommendedDriveFolder(ctx, b.Services, title, script, insights); folder != nil {
		insights.RecommendedDriveFolder = folder
	}

	clipSources := []assetSearchTarget{
		{source: "youtube", mediaType: "video"},
		{source: "clip_drive", mediaType: "video"},
		{source: "artlist", mediaType: "video"},
		{source: "stock", mediaType: "video"},
	}
	insights.PhraseClipSuggestions = BuildPhraseClipSuggestions(ctx, b.Services, title, insights, clipSources)
	insights.IntroClips = SearchIntroClips(ctx, b.Services, title, script, insights, clipSources)

	return insights
}

// ── Type Definitions ─────────────────────────────────────────────────────────

type ScriptAssetSuggestion struct {
	ID        string  `json:"id,omitempty"`
	Name      string  `json:"name,omitempty"`
	Source    string  `json:"source,omitempty"`
	Score     float64 `json:"score,omitempty"`
	DriveLink string  `json:"drive_link,omitempty"`
}

type ScriptPhraseClipSuggestion struct {
	Phrase string                  `json:"phrase,omitempty"`
	Clips  []ScriptAssetSuggestion `json:"clips,omitempty"`
}

type ScriptDriveFolderSuggestion struct {
	Database string `json:"database,omitempty"`
	Source   string `json:"source,omitempty"`
	Name     string `json:"name,omitempty"`
	Path     string `json:"path,omitempty"`
	Link     string `json:"link,omitempty"`
	FolderID string `json:"folder_id,omitempty"`
	Score    int    `json:"score,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type ScriptArtlistClipSuggestion struct {
	Phrase     string                  `json:"phrase"`
	Clips      []ScriptAssetSuggestion `json:"clips,omitempty"`
	FolderLink string                  `json:"folder_link,omitempty"`
	FolderName string                  `json:"folder_name,omitempty"`
	FolderID   string                  `json:"folder_id,omitempty"`
}

type ScriptInsights struct {
	ImportantWords         []string                      `json:"important_words,omitempty"`
	ImportantPhrases       []string                      `json:"important_phrases,omitempty"`
	SpecialNames           []string                      `json:"special_names,omitempty"`
	ArtlistPhrases         []string                      `json:"artlist_phrases,omitempty"`
	ArtlistClipSuggestions []ScriptArtlistClipSuggestion `json:"artlist_clip_suggestions,omitempty"`
	RecommendedDriveFolder *ScriptDriveFolderSuggestion  `json:"recommended_drive_folder,omitempty"`
	PhraseClipSuggestions  []ScriptPhraseClipSuggestion  `json:"phrase_clip_suggestions,omitempty"`
	IntroClips             []ScriptAssetSuggestion       `json:"intro_clips,omitempty"`
	EntityImages           []ScriptEntityImage           `json:"entity_images,omitempty"`
}

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
	req := association.CandidatesRequest{
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
			Score:    candidate.Score,
			Reason:   strings.TrimSpace(candidate.Reason),
		}
	}

	return nil
}

// ── resolveDriveFolderID + helpers (flow_shared_helpers.go → flow.go) ───────

// resolveDriveFolderID takes an input which could be a raw folder ID or a folder name/path.
// If it is a name or a path, it searches for it or walks/creates folders under the given defaultRootID on Google Drive
// using the uploader, returning the resolved folder ID.
func (h *ScriptFlowHandler) resolveDriveFolderID(ctx context.Context, input, defaultRootID string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultRootID, nil
	}

	// Helper to check if it's already a raw ID:
	// Google Drive IDs are typically 19 to 45 characters of [a-zA-Z0-9_-]
	isRawID := true
	if len(input) < 19 || len(input) > 45 {
		isRawID = false
	} else {
		for _, r := range input {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
				isRawID = false
				break
			}
		}
	}

	if isRawID {
		return input, nil
	}

	// It's a path or name.
	if h.driveUploader == nil || h.driveUploader.Service == nil {
		h.log.Warn("driveUploader not initialized, cannot resolve folder name/path; returning defaultRootID", zap.String("input", input))
		return defaultRootID, nil
	}

	// Dynamic deep search: try to find an existing folder matching this name (1-2 levels deep) under defaultRootID
	if foundID, err := h.findFolderByNameDeep(ctx, input, defaultRootID); err == nil && foundID != "" {
		h.log.Info("found existing folder dynamically on Google Drive", zap.String("name", input), zap.String("folder_id", foundID))
		return foundID, nil
	}

	// Fallback: build the path segments under defaultRootID
	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == '/' || r == '\\'
	})

	currentID := defaultRootID
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := h.driveUploader.GetOrCreateFolder(ctx, part, currentID)
		if err != nil {
			return "", fmt.Errorf("failed to get or create folder %q under %q: %w", part, currentID, err)
		}
		currentID = id
	}

	return currentID, nil
}

// findFolderByNameDeep searches for a folder by name directly under rootID or 1 level deeper (subfolders).
func (h *ScriptFlowHandler) findFolderByNameDeep(ctx context.Context, name, rootID string) (string, error) {
	if h.driveUploader == nil || h.driveUploader.Service == nil {
		return "", fmt.Errorf("drive uploader not initialized")
	}
	targetClean := fileutil.CleanFolderName(name)

	// 1. Search directly under the root folder
	query := fmt.Sprintf("'%s' in parents and trashed = false and mimeType = 'application/vnd.google-apps.folder'", rootID)
	list, err := h.driveUploader.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err == nil && len(list.Files) > 0 {
		for _, file := range list.Files {
			if fileutil.CleanFolderName(file.Name) == targetClean {
				return file.Id, nil
			}
		}

		// 2. Search one level deep (look inside each subfolder of the root)
		for _, subDir := range list.Files {
			subQuery := fmt.Sprintf("'%s' in parents and trashed = false and mimeType = 'application/vnd.google-apps.folder'", subDir.Id)
			subList, subErr := h.driveUploader.Service.Files.List().Q(subQuery).Fields("files(id, name)").Context(ctx).Do()
			if subErr == nil && len(subList.Files) > 0 {
				for _, file := range subList.Files {
					if fileutil.CleanFolderName(file.Name) == targetClean {
						return file.Id, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("folder %q not found", name)
}

// buildTextOnlyScriptPlan builds a plan for text-only script generation.
// This pattern is used by HandleClipScriptGenerateJob (Path 3 text-only fallback).
func buildTextOnlyScriptPlan(
	topic, sourceText, guidelines, title, language, tone, model string,
	forceRefresh, saveToDB bool, targetWords int,
	promptVersion, editorPromptVersion, qaPromptVersion string,
) *script.ScriptGenerationPlan {
	if topic == "" {
		topic = sourceText
	}
	if title == "" {
		title = topic
	}

	plan := &script.ScriptGenerationPlan{
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

// (PR3 fixup: dropped errors and gin-gonic/gin imports — neither symbol
//  was used in this file, only the comment claimed so. errors.Is lives in
//  handler_jobs.go::buildVoiceoverDestination; gin types live in
//  handler_flow.go.)
