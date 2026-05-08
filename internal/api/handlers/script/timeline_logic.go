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

	// BATCH LLM QUERY GENERATION - always generate visual fields first
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

		// ALWAYS populate visual fields first (from batch or individual generation)
		populateVisualFields(&seg, batchResults)

		// If visual fields not populated, generate individually
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

		// Now search Artlist DB using the search suggestions (which are now populated)
		if artlistService != nil && len(seg.SearchSuggestions) > 0 {
			searchArtlistFromDB(ctx, &seg, artlistService)
		}

		// Finally, filter ALL matches for semantic relevance
		// Reject matches that don't align with visual_subject or search_suggestions
		if !hasUsefulVisualMatch(seg, req.Topic) {
			zap.L().Warn("rejecting all matches - no useful visual match",
				zap.Int("segment_index", seg.Index),
				zap.String("subject", seg.Subject),
				zap.String("visual_subject", seg.VisualSubject),
				zap.Strings("search_suggestions", seg.SearchSuggestions),
			)
			seg.StockMatches = nil
			seg.ArtlistMatches = nil
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
// Creates multiple segments based on duration to ensure minimum safety
func fallbackTimelinePlan(topic string, duration int, narrative string) *timelineLLMPlan {
	minSegments := calculateMinSegments(duration)
	segDuration := float64(duration) / float64(minSegments)

	segments := make([]timelineLLMSegment, 0, minSegments)
	sentences := textutil.ExtractSentences(narrative)

	for i := 0; i < minSegments; i++ {
		startTime := float64(i) * segDuration
		endTime := float64(i+1) * segDuration
		if i == minSegments-1 {
			endTime = float64(duration)
		}

		// Distribute sentences across segments
		segNarrative := distributeNarrativeToSegment(narrative, sentences, i, minSegments)
		segSubject := fmt.Sprintf("%s (part %d)", topic, i+1)

		segments = append(segments, timelineLLMSegment{
			Index:           i + 1,
			StartTime:       startTime,
			EndTime:         endTime,
			Subject:         segSubject,
			NarrativeText:   segNarrative,
			OpeningSentence: firstSentence(segNarrative),
			ClosingSentence: lastSentence(segNarrative),
		})
	}

	return &timelineLLMPlan{
		PrimaryFocus: topic,
		Segments:     segments,
	}
}

// calculateMinSegments returns the minimum number of segments based on duration
func calculateMinSegments(duration int) int {
	switch {
	case duration <= 60:
		return 4
	case duration <= 180:
		return 6
	case duration >= 300:
		return 10
	default:
		return max(1, duration/30)
	}
}

// distributeNarrativeToSegment splits narrative text across segments
func distributeNarrativeToSegment(fullNarrative string, sentences []string, segmentIndex, totalSegments int) string {
	if len(sentences) == 0 {
		return fullNarrative
	}

	// Calculate which sentences belong to this segment
	sentencesPerSegment := len(sentences) / totalSegments
	if sentencesPerSegment == 0 {
		sentencesPerSegment = 1
	}

	startIdx := segmentIndex * sentencesPerSegment
	endIdx := startIdx + sentencesPerSegment
	if segmentIndex == totalSegments-1 {
		endIdx = len(sentences)
	}

	if startIdx >= len(sentences) {
		return ""
	}
	if endIdx > len(sentences) {
		endIdx = len(sentences)
	}

	var result strings.Builder
	for i := startIdx; i < endIdx; i++ {
		result.WriteString(sentences[i])
		result.WriteString(" ")
	}
	return strings.TrimSpace(result.String())
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
// DEPRECATED: Use hasUsefulVisualMatch instead.
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

	// Build a comprehensive set of relevant terms from multiple sources
	relevantTerms := buildRelevantTerms(seg, topic)

	// Build a set of terms from search suggestions (these are the queries that found the matches)
	searchTerms := make(map[string]bool)
	for _, s := range seg.SearchSuggestions {
		for _, term := range strings.Fields(strings.ToLower(s)) {
			if len(term) > 2 {
			searchTerms[term] = true
		}
		}
	}

	// Check each match for relevance
	relevantMatchCount := 0
	for _, match := range allMatches {
		if isMatchRelevant(match, relevantTerms, searchTerms) {
			relevantMatchCount++
		}
	}

	// At least some matches must be relevant
	if relevantMatchCount == 0 {
		zap.L().Warn("semantic mismatch detected - no relevant matches",
			zap.String("topic", topic),
			zap.String("subject", seg.Subject),
			zap.String("visual_subject", seg.VisualSubject),
			zap.Strings("search_suggestions", seg.SearchSuggestions),
			zap.Strings("matches", allMatches),
		)
		return false
	}

	return true
}

// buildRelevantTerms builds a comprehensive set of terms from segment data
func buildRelevantTerms(seg TimelineSegment, topic string) map[string]bool {
	terms := make(map[string]bool)

	// Add terms from topic
	for _, term := range strings.Fields(strings.ToLower(topic)) {
		if len(term) > 2 {
			terms[term] = true
		}
	}

	// Add terms from subject (more specific than topic)
	for _, term := range strings.Fields(strings.ToLower(seg.Subject)) {
		if len(term) > 2 {
			terms[term] = true
		}
	}

	// Add terms from visual subject
	if seg.VisualSubject != "" {
		for _, term := range strings.Fields(strings.ToLower(seg.VisualSubject)) {
			if len(term) > 2 {
				terms[term] = true
			}
		}
	}

	// Add terms from keywords
	for _, kw := range seg.Keywords {
		for _, term := range strings.Fields(strings.ToLower(kw)) {
			if len(term) > 2 {
				terms[term] = true
			}
		}
	}

	// Add terms from entities
	for _, ent := range seg.Entities {
		for _, term := range strings.Fields(strings.ToLower(ent)) {
			if len(term) > 2 {
				terms[term] = true
			}
		}
	}

	return terms
}

// isMatchRelevant checks if a match is relevant based on relevant terms and search terms
func isMatchRelevant(match string, relevantTerms, searchTerms map[string]bool) bool {
	matchLower := strings.ToLower(match)

	// Check if match contains any relevant term
	for term := range relevantTerms {
		if strings.Contains(matchLower, term) {
			return true
		}
	}

	// Check if match contains any search term (these are the queries used to find the match)
	for term := range searchTerms {
		if strings.Contains(matchLower, term) {
			return true
		}
	}

	return false
}

// hasUsefulVisualMatch checks if the segment has matches that align with visual_subject and search_suggestions.
// Returns true only if matches exist and are relevant to the visual context.
func hasUsefulVisualMatch(seg TimelineSegment, topic string) bool {
	// Collect all matches
	allMatches := make([]string, 0)
	for _, m := range seg.StockMatches {
		allMatches = append(allMatches, m.Title, m.Path)
	}
	for _, m := range seg.ArtlistMatches {
		allMatches = append(allMatches, m.Title, m.Path)
	}

	// If no matches, clearly not useful
	if len(allMatches) == 0 {
		return false
	}

	// Build visual context from visual_subject and search_suggestions
	visualTerms := make(map[string]bool)

	// Add terms from visual_subject
	if seg.VisualSubject != "" {
		for _, term := range strings.Fields(strings.ToLower(seg.VisualSubject)) {
			if len(term) > 2 {
				visualTerms[term] = true
			}
		}
	}

	// Add terms from search_suggestions (these are the intended search queries)
	for _, s := range seg.SearchSuggestions {
		for _, term := range strings.Fields(strings.ToLower(s)) {
			if len(term) > 2 {
				visualTerms[term] = true
			}
		}
	}

	// Add terms from subject
	for _, term := range strings.Fields(strings.ToLower(seg.Subject)) {
		if len(term) > 2 {
			visualTerms[term] = true
		}
	}

	// If we have no visual context, reject matches (they're likely irrelevant)
	if len(visualTerms) == 0 {
		zap.L().Warn("no visual context available, rejecting matches",
			zap.Int("segment_index", seg.Index),
			zap.String("subject", seg.Subject),
		)
		return false
	}

	// Check if at least one match contains visual terms
	relevantCount := 0
	for _, match := range allMatches {
		matchLower := strings.ToLower(match)
		for term := range visualTerms {
			if strings.Contains(matchLower, term) {
				relevantCount++
				break
			}
		}
	}

	// At least some matches must be relevant
	if relevantCount == 0 {
		zap.L().Warn("no matches align with visual context",
			zap.Int("segment_index", seg.Index),
			zap.String("visual_subject", seg.VisualSubject),
			zap.Strings("search_suggestions", seg.SearchSuggestions),
			zap.Strings("matches", allMatches),
		)
		return false
	}

	return true
}
