package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/association"
	segmentnorm "velox/go-master/internal/service/catalognormalizer"
	"velox/go-master/internal/service/timeline"
	"velox/go-master/pkg/sliceutil"
	"velox/go-master/pkg/textutil"
	"go.uber.org/zap"
)

const timelineCacheVersion = "v14"

func BuildTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, nodeScraperDir, sourceText, narrative string, stockRepo, artlistRepo, clipsRepo *clips.Repository, artlistService *artlistSvc.Service, assocService *association.Service) (*TimelinePlan, error) {
	startedAt := time.Now()
	zap.L().Info("Building timeline plan", zap.String("topic", req.Topic))

	cache := timeline.NewCache(clipsRepo, gen)
	cacheKey := cache.BuildKey(req.Topic, req.Template, sourceText, narrative, req.Duration)

	// Try cache first
	if rows, err := cache.LoadPlan(ctx, cacheKey); err == nil && len(rows) > 0 {
		zap.L().Info("timeline plan cache hit", zap.String("topic", req.Topic))
		return convertCacheRowsToPlan(rows), nil
	}

	// 1. LLM SEGMENTATION
	rawPlan, err := chooseTimelinePlanWithLLM(ctx, gen, req.Topic, req.Duration, sourceText, narrative)
	if err != nil {
		rawPlan = fallbackTimelinePlan(req.Topic, req.Duration, narrative)
	}

	// Apply structured timeline if available
	if structuredPlan, ok := buildStructuredTimelinePlan(req.Topic, req.Duration, sourceText); ok && len(structuredPlan.Segments) > 1 {
		rawPlan = structuredPlan
	}

	// Prepare segments for batch query generation
	var batchSegments []BatchSegmentInput
	for i, rawSeg := range rawPlan.Segments {
		batchSegments = append(batchSegments, BatchSegmentInput{
			Index:     i + 1,
			Subject:   strings.TrimSpace(rawSeg.Subject),
			Narrative: strings.TrimSpace(rawSeg.NarrativeText),
		})
	}

	// BATCH LLM QUERY GENERATION
	var batchResults map[int]VisualQueryResult
	if gen != nil && len(batchSegments) > 0 {
		batchResults = GenerateBatchArtlistVisualQueries(ctx, gen, req.Topic, batchSegments, DefaultMaxQueries)
	}

	// Build timeline plan
	plan := &TimelinePlan{
		PrimaryFocus:  req.Topic,
		SegmentCount:  len(rawPlan.Segments),
		TotalDuration: req.Duration,
		Segments:      make([]TimelineSegment, 0, len(rawPlan.Segments)),
	}

	normalizer := segmentnorm.NewService(stockRepo, clipsRepo, artlistRepo, zap.L())

	for i, rawSeg := range rawPlan.Segments {
		seg := buildSegment(ctx, req, rawSeg, i, dataDir, stockRepo, assocService, normalizer)
		
		// Populate visual fields from batch results
		populateVisualFields(&seg, batchResults)
		
		// Fallback: if visual fields not populated, generate individually
		if seg.VisualSubject == "" && gen != nil {
			zap.L().Info("visual fields empty, generating individually",
				zap.Int("segment_index", seg.Index),
			)
			visualResult := GenerateArtlistVisualQuery(ctx, gen, req.Topic, seg.Subject, seg.NarrativeText, DefaultMaxQueries)
			if visualResult.VisualSubject != "" {
				seg.VisualSubject = visualResult.VisualSubject
				seg.VisualCaption = visualResult.VisualCaption
				seg.SearchSuggestions = sliceutil.UniqueStrings(append(seg.SearchSuggestions, visualResult.Queries...))
			}
		}
		
		// Check semantic relevance and reject bad matches
		if !isSemanticallyRelevant(seg, req.Topic) {
			seg.StockMatches = nil
			seg.ArtlistMatches = nil
		}
		
		// If no matches, search Artlist DB
		if len(seg.StockMatches) == 0 && len(seg.ArtlistMatches) == 0 && artlistService != nil {
			searchArtlistFromDB(ctx, &seg, artlistService)
		}
		
		// Store in cache and add to plan
		storeSegmentInCache(ctx, cache, cacheKey, req, seg, narrative)
		plan.Segments = append(plan.Segments, seg)
	}

	zap.L().Info("timeline plan completed", zap.Duration("elapsed", time.Since(startedAt)))
	return plan, nil
}

// buildSegment creates a TimelineSegment from raw LLM output
func buildSegment(ctx context.Context, req ScriptDocsRequest, rawSeg timelineLLMSegment, idx int, dataDir string, stockRepo *clips.Repository, assocService *association.Service, normalizer *segmentnorm.Service) TimelineSegment {
	seg := TimelineSegment{
		Index:             idx + 1,
		StartTime:        rawSeg.StartTime,
		EndTime:          rawSeg.EndTime,
		Timestamp:        fmt.Sprintf("%.0f-%.0f", rawSeg.StartTime, rawSeg.EndTime),
		Subject:          strings.TrimSpace(rawSeg.Subject),
		NarrativeText:    strings.TrimSpace(rawSeg.NarrativeText),
		Keywords:         rawSeg.Keywords,
		Entities:         rawSeg.Entities,
		SearchSuggestions: rawSeg.SearchSuggestions,
	}

	// Resolve subject
	if req.Topic != "" && strings.EqualFold(seg.Subject, req.Topic) {
		seg.Subject = resolveTimelineSegmentSubject(ctx, req, seg, dataDir, stockRepo, assocService)
	}

	// Normalize
	if normalized, _ := normalizer.NormalizeSegment(ctx, segmentnorm.SegmentInput{
		Topic:         req.Topic,
		Duration:      req.Duration,
		Template:      req.Template,
		Subject:       seg.Subject,
		NarrativeText: seg.NarrativeText,
		Keywords:      seg.Keywords,
		Entities:      seg.Entities,
	}); normalized != nil {
		seg.CanonicalSubject = normalized.CanonicalSubject
		seg.CanonicalKeywords = sliceutil.UniqueStrings(normalized.CanonicalKeywords)
		seg.CanonicalEntities = sliceutil.UniqueStrings(normalized.CanonicalEntities)
	}

	// Associate assets
	associateSegment(ctx, &seg, assocService, req.Topic)

	return seg
}

// populateVisualFields populates visual fields from batch results
func populateVisualFields(seg *TimelineSegment, batchResults map[int]VisualQueryResult) {
	if batchResults == nil {
		return
	}
	if r, ok := batchResults[seg.Index]; ok {
		seg.VisualSubject = r.VisualSubject
		seg.VisualCaption = r.VisualCaption
		seg.SearchSuggestions = sliceutil.UniqueStrings(append(seg.SearchSuggestions, r.Queries...))
	}
}

// searchArtlistFromDB searches Artlist clips in the database only (no live search)
func searchArtlistFromDB(ctx context.Context, seg *TimelineSegment, artlistService *artlistSvc.Service) {
	if len(seg.SearchSuggestions) == 0 {
		return
	}
	
	var artlistClips []association.ScoredMatch
	for _, query := range seg.SearchSuggestions {
		clipPtrs := artlistService.SearchClips(ctx, query)
		for _, c := range clipPtrs {
			artlistClips = append(artlistClips, association.ScoredMatch{
				Title:  c.Name,
				Path:   c.LocalPath,
				Score:  80,
				Source: "artlist_dynamic",
				Link:   c.DriveLink,
			})
		}
	}
	if len(artlistClips) > 0 {
		seg.ArtlistMatches = artlistClips
	}
}
func convertCacheRowsToPlan(rows []clips.SegmentEmbeddingRecord) *TimelinePlan {
	if len(rows) == 0 {
		return nil
	}
	plan := &TimelinePlan{
		PrimaryFocus:  rows[0].Topic,
		SegmentCount:  len(rows),
		TotalDuration: rows[0].Duration,
		Segments:      make([]TimelineSegment, 0, len(rows)),
	}
	for _, row := range rows {
		var seg TimelineSegment
		if err := json.Unmarshal([]byte(row.SegmentJSON), &seg); err != nil {
			seg = TimelineSegment{
				Index:            row.SegmentIndex,
				Subject:          row.RawSubject,
				CanonicalSubject: row.CanonicalSubject,
				Keywords:         textutil.SplitCSV(row.RawKeywordsJSON),
				Entities:         textutil.SplitCSV(row.RawEntitiesJSON),
			}
		}
		if seg.Index == 0 {
			seg.Index = row.SegmentIndex
		}
		plan.Segments = append(plan.Segments, seg)
	}
	return plan
	return plan
}

func storeSegmentInCache(ctx context.Context, c *timeline.Cache, cacheKey string, req ScriptDocsRequest, seg TimelineSegment, narrative string) error {
	bestSource, bestPath, bestLink, bestScore := bestMatchFromSegment(seg)
	payload, err := json.Marshal(seg)
	if err != nil {
		return err
	}

	embeddingText := strings.TrimSpace(strings.Join([]string{
		seg.CanonicalSubject,
		strings.Join(seg.CanonicalKeywords, " "),
		strings.Join(seg.CanonicalEntities, " "),
		seg.NarrativeText,
	}, " | "))
	if embeddingText == "" {
		embeddingText = strings.TrimSpace(narrative)
	}

	embeddingJSON, _ := c.GenerateEmbedding(ctx, embeddingText)

	return c.StoreSegment(ctx, cacheKey, &clips.SegmentEmbeddingRecord{
		ScriptKey:             cacheKey,
		SourceHash:            c.HashSegment(req.Topic, req.Template, req.Duration, seg.NarrativeText, seg.Keywords, seg.Entities),
		Topic:                 req.Topic,
		Language:              req.Language,
		Template:              req.Template,
		Duration:              req.Duration,
		SegmentIndex:          seg.Index,
		RawSubject:            seg.Subject,
		CanonicalSubject:      seg.CanonicalSubject,
		RawKeywordsJSON:       marshalStringSliceJSON(seg.Keywords),
		CanonicalKeywordsJSON: marshalStringSliceJSON(seg.CanonicalKeywords),
		RawEntitiesJSON:       marshalStringSliceJSON(seg.Entities),
		CanonicalEntitiesJSON: marshalStringSliceJSON(seg.CanonicalEntities),
		SegmentJSON:           string(payload),
		EmbeddingJSON:         embeddingJSON,
		BestSource:            bestSource,
		BestPath:              bestPath,
		BestLink:              bestLink,
		BestScore:             bestScore,
	})
}

func bestMatchFromSegment(seg TimelineSegment) (string, string, string, int) {
	bestSource := ""
	bestPath := ""
	bestLink := ""
	bestScore := 0

	// Use only Stock and Artlist matches
	for _, matches := range [][]association.ScoredMatch{seg.StockMatches, seg.ArtlistMatches} {
		for _, m := range matches {
			if m.Score > bestScore {
				bestScore = m.Score
				bestSource = m.Source
				bestPath = m.Path
				bestLink = m.Link
			}
		}
	}
	return bestSource, bestPath, bestLink, bestScore
}

func firstNonEmptySlice(primary, fallback []string) []string {
	return sliceutil.FirstNonEmpty(primary, fallback)
}

func resolveTimelineSegmentSubject(ctx context.Context, req ScriptDocsRequest, seg TimelineSegment, dataDir string, stockRepo *clips.Repository, assocService *association.Service) string {
	topic := strings.TrimSpace(req.Topic)
	rawSubject := strings.TrimSpace(seg.Subject)

	if assocService != nil {
		if direct, ok, err := assocService.FindDirectStockFolderCandidate(ctx, topic, rawSubject); err == nil && ok && direct != nil {
			if topic != "" && looksLikePersonName(topic) {
				return topic
			}
			if name := strings.TrimSpace(direct.Name); name != "" {
				return name
			}
		}
	}

	if entitySubject := preferredEntitySubject(&timelineLLMSegment{
		Subject:  rawSubject,
		Entities: seg.Entities,
	}, topicTokens(topic)); entitySubject != "" {
		return entitySubject
	}

	if subjectMatchesTopic(rawSubject, topicTokens(topic)) {
		return rawSubject
	}
	// conciseSubject disabled: produces bad subjects from first tokens
	if topic != "" {
		return topic
	}
	return rawSubject
}

func injectPreferredAssociation(seg *TimelineSegment) {
	if seg == nil {
		return
	}
	// If we already have strong matches, don't inject from preferred
	if len(seg.StockMatches) > 0 || len(seg.ArtlistMatches) > 0 {
		return
	}
	if strings.TrimSpace(seg.PreferredStockGroup) == "" || len(seg.PreferredStockPaths) == 0 {
		return
	}

	title := firstNonEmpty(seg.CanonicalSubject, seg.Subject, "Asset")
	link := ""
	path := ""
	if len(seg.PreferredStockPaths) > 0 {
		path = strings.TrimSpace(seg.PreferredStockPaths[0])
	}
	if len(seg.PreferredStockPaths) > 1 {
		link = strings.TrimSpace(seg.PreferredStockPaths[1])
	}
	if link == "" && strings.HasPrefix(strings.ToLower(path), "http") {
		link = path
		path = ""
	}

	match := association.ScoredMatch{
		Title:   title,
		Path:    path,
		Score:   80,
		Link:    link,
		Details: seg.PreferredStockReason,
	}

	switch strings.ToLower(strings.TrimSpace(seg.PreferredStockGroup)) {
	case "stock_folder", "stock_drive":
		match.Source = "drive_stock"
		seg.StockMatches = append(seg.StockMatches, match)
	case "artlist_folder":
		match.Source = string(timelineAssetSourceArtlistFolder)
		seg.ArtlistMatches = append(seg.ArtlistMatches, match)
	}
}

func marshalStringSliceJSON(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func associateSegment(ctx context.Context, seg *TimelineSegment, assocService *association.Service, topic string) {
	if assocService == nil {
		return
	}

	input := association.SegmentInput{
		Topic:     topic,
		Subject:   segmentAssociationSubject(seg),
		Keywords:  segmentAssociationKeywords(seg),
		Entities:  segmentAssociationEntities(seg),
		Narrative: seg.NarrativeText,
	}

	matches := assocService.Associate(ctx, input)
	for _, m := range matches {
		switch m.Source {
		case "drive_stock", "stock_folder", "clip_drive":
			seg.StockMatches = append(seg.StockMatches, m)
		case "artlist_folder", "artlist_stock", "artlist_dynamic", "artlist_clip":
			seg.ArtlistMatches = append(seg.ArtlistMatches, m)
		default:
			// Ignore unrecognized sources
		}
	}
}



// fallbackTimelinePlan creates a basic segment if LLM fails
func fallbackTimelinePlan(topic string, duration int, narrative string) *timelineLLMPlan {
	return &timelineLLMPlan{
		PrimaryFocus: topic,
		Segments: []timelineLLMSegment{
			{
				Index:           1,
				StartTime:       0,
				EndTime:         float64(duration),
				Subject:         topic,
				NarrativeText:   narrative,
				OpeningSentence: firstSentence(narrative),
				ClosingSentence: lastSentence(narrative),
			},
		},
	}
}

func firstSentence(text string) string {
	sentences := textutil.ExtractSentences(text)
	if len(sentences) > 0 {
		return sentences[0]
	}
	return textutil.Truncate(text, 120)
}

func lastSentence(text string) string {
	sentences := textutil.ExtractSentences(text)
	if len(sentences) > 0 {
		return sentences[len(sentences)-1]
	}
	return textutil.Truncate(text, 120)
}

func applyAssociationHints(seg *TimelineSegment, resp *association.CandidatesResponse) {
	if seg == nil || resp == nil || len(resp.Candidates) == 0 {
		return
	}
	best := resp.Candidates[0]
	seg.PreferredStockReason = best.Reason
	seg.PreferredStockGroup = best.Source
	preferredLink := association.NormalizeDriveFolderLink(best.Link, best.FolderID)
	seg.PreferredStockPaths = sliceutil.UniqueStrings(sliceutil.TrimStrings([]string{best.Path, preferredLink}))
}

// isSemanticallyRelevant checks if the segment's matches are actually relevant to the topic/subject.
// Returns false if matches are based on weak keywords (e.g., "rain", "net") while ignoring the main topic.
func isSemanticallyRelevant(seg TimelineSegment, topic string) bool {
	// Collect all match titles/paths
	allMatches := make([]string, 0)
	for _, m := range seg.StockMatches {
		allMatches = append(allMatches, m.Title, m.Path)
	}
	for _, m := range seg.ArtlistMatches {
		allMatches = append(allMatches, m.Title, m.Path)
	}

	if len(allMatches) == 0 {
		return false
	}

	// Get key terms from topic and subject
	keyTerms := make(map[string]bool)
	for _, term := range strings.Fields(strings.ToLower(topic)) {
		keyTerms[term] = true
	}
	for _, term := range strings.Fields(strings.ToLower(seg.Subject)) {
		keyTerms[term] = true
	}
	if seg.VisualSubject != "" {
		for _, term := range strings.Fields(strings.ToLower(seg.VisualSubject)) {
			keyTerms[term] = true
		}
	}

	// Check if any match contains key terms
	for _, match := range allMatches {
		matchLower := strings.ToLower(match)
		for term := range keyTerms {
			if strings.Contains(matchLower, term) {
				return true
			}
		}
	}

	// No match contains any key term - likely a semantic mismatch
	zap.L().Warn("semantic mismatch detected",
		zap.String("topic", topic),
		zap.String("subject", seg.Subject),
		zap.String("visual_subject", seg.VisualSubject),
		zap.Strings("matches", allMatches),
	)
	return false
}
