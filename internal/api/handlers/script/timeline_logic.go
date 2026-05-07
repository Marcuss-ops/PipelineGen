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

const timelineCacheVersion = "v13"

// BuildTimelinePlan coordinates the LLM planning and asset matching.
func BuildTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, nodeScraperDir, sourceText, narrative string, stockRepo, artlistRepo, clipsRepo *clips.Repository, artlistService *artlistSvc.Service, assocService *association.Service) (*TimelinePlan, error) {
	startedAt := time.Now()
	zap.L().Info("Building timeline plan", zap.String("topic", req.Topic))

	cache := timeline.NewCache(clipsRepo, gen)
	cacheKey := cache.BuildKey(req.Topic, req.Template, sourceText, narrative, req.Duration)

	if rows, err := cache.LoadPlan(ctx, cacheKey); err == nil && len(rows) > 0 {
		zap.L().Info("timeline plan cache hit",
			zap.String("topic", req.Topic),
			zap.String("cache_key", cacheKey),
			zap.Int("segments", len(rows)),
		)
		return convertCacheRowsToPlan(rows), nil
	}

	// 1. LLM SEGMENTATION
	llmStarted := time.Now()
	rawPlan, err := chooseTimelinePlanWithLLM(ctx, gen, req.Topic, req.Duration, sourceText, narrative)
	if err != nil {
		zap.L().Warn("LLM timeline planning failed, using fallback", zap.Error(err), zap.Duration("elapsed", time.Since(llmStarted)))
		rawPlan = fallbackTimelinePlan(req.Topic, req.Duration, narrative)
	} else {
		zap.L().Info("LLM timeline planning successful", zap.Int("segments", len(rawPlan.Segments)), zap.Duration("elapsed", time.Since(llmStarted)))
	}

	structuredOverrideApplied := false
	if structuredPlan, ok := buildStructuredTimelinePlan(req.Topic, req.Duration, sourceText); ok {
		if len(structuredPlan.Segments) > 1 {
			zap.L().Info("structured timeline override applied",
				zap.String("topic", req.Topic),
				zap.Int("structured_segments", len(structuredPlan.Segments)),
				zap.Int("llm_segments", len(rawPlan.Segments)),
			)
			rawPlan = structuredPlan
			structuredOverrideApplied = true
			for i := range rawPlan.Segments {
				rawPlan.Segments[i].Subject = strings.TrimSpace(rawPlan.Segments[i].Subject)
			}
		}
	}

	preserveStructuredSubjects := structuredOverrideApplied

	if len(rawPlan.Segments) == 1 {
		rawPlan.Segments[0].NarrativeText = narrative
		rawPlan.Segments[0].OpeningSentence = firstSentence(narrative)
		rawPlan.Segments[0].ClosingSentence = lastSentence(narrative)
		rawPlan.Segments[0].StartTime = 0
		rawPlan.Segments[0].EndTime = float64(req.Duration)
	}

	// BATCH LLM QUERY GENERATION: Pre-generate queries for all segments
	var batchSegments []BatchSegmentInput
	var batchResults map[int]VisualQueryResult // Accessible in segment loop

	for i, rawSeg := range rawPlan.Segments {
		subject := strings.TrimSpace(rawSeg.Subject)
		narrativeText := strings.TrimSpace(rawSeg.NarrativeText)
		batchSegments = append(batchSegments, BatchSegmentInput{
			Index:     i + 1,
			Subject:   subject,
			Narrative: narrativeText,
		})
	}

	if gen != nil && len(batchSegments) > 0 {
		zap.L().Info("Pre-generating visual queries in batch",
			zap.Int("segment_count", len(batchSegments)),
		)
		batchResults = GenerateBatchArtlistVisualQueries(ctx, gen, req.Topic, batchSegments, DefaultMaxQueries)
		zap.L().Info("Batch query generation completed",
			zap.Int("results_count", len(batchResults)),
		)
	}

	plan := &TimelinePlan{
		PrimaryFocus:  req.Topic,
		SegmentCount:  len(rawPlan.Segments),
		TotalDuration: req.Duration,
		Segments:      make([]TimelineSegment, 0, len(rawPlan.Segments)),
	}
	if clipsRepo != nil {
		if err := cache.ClearKey(ctx, cacheKey); err != nil {
			zap.L().Warn("failed to clear timeline cache key", zap.String("cache_key", cacheKey), zap.Error(err))
		}
	}

	// 2. ASSET MATCHING STRATEGY (MODULAR)
	normalizer := segmentnorm.NewService(stockRepo, clipsRepo, artlistRepo, zap.L())

	for i, rawSeg := range rawPlan.Segments {
		blockText := strings.TrimSpace(rawSeg.NarrativeText)
		opening := strings.TrimSpace(rawSeg.OpeningSentence)
		closing := strings.TrimSpace(rawSeg.ClosingSentence)

		// Fallback to extraction if LLM didn't provide them
		if blockText != "" {
			if opening == "" {
				opening = firstSentence(blockText)
			}
			if closing == "" {
				closing = lastSentence(blockText)
			}
		}

		seg := TimelineSegment{
			Index:             i + 1,
			StartTime:        rawSeg.StartTime,
			EndTime:          rawSeg.EndTime,
			Timestamp:        fmt.Sprintf("%.0f-%.0f", rawSeg.StartTime, rawSeg.EndTime),
			Subject:          strings.TrimSpace(rawSeg.Subject),
			NarrativeText:    blockText,
			OpeningSentence:  opening,
			ClosingSentence:  closing,
			Keywords:         rawSeg.Keywords,
			Entities:         rawSeg.Entities,
			SearchSuggestions: rawSeg.SearchSuggestions,
		}

		if preserveStructuredSubjects {
			seg.Subject = firstNonEmpty(seg.Subject, req.Topic)
		} else {
			seg.Subject = resolveTimelineSegmentSubject(ctx, req, seg, dataDir, stockRepo, assocService)
		}

		if normalized, err := normalizer.NormalizeSegment(ctx, segmentnorm.SegmentInput{
			Topic:         req.Topic,
			Duration:      req.Duration,
			Template:      req.Template,
			Subject:       seg.Subject,
			NarrativeText: seg.NarrativeText,
			Keywords:      seg.Keywords,
			Entities:      seg.Entities,
		}); err == nil && normalized != nil {
			seg.CanonicalSubject = normalized.CanonicalSubject
			seg.CanonicalKeywords = sliceutil.UniqueStrings(normalized.CanonicalKeywords)
			seg.CanonicalEntities = sliceutil.UniqueStrings(normalized.CanonicalEntities)
			seg.NormalizationSource = normalized.NormalizationSource
		}
		if strings.TrimSpace(seg.CanonicalSubject) == "" {
			seg.CanonicalSubject = seg.Subject
		}
		if preserveStructuredSubjects {
			seg.CanonicalSubject = seg.Subject
		}

		associationSubject := firstNonEmpty(seg.CanonicalSubject, seg.Subject)
		if preserveStructuredSubjects {
			associationSubject = firstNonEmpty(seg.Subject, seg.CanonicalSubject)
		}
		associationTopic := req.Topic
		if preserveStructuredSubjects {
			associationTopic = associationSubject
		}

		associationReq := association.CandidatesRequest{
			Topic:      associationTopic,
			SegmentKey: seg.Timestamp,
			Timestamp:  seg.Timestamp,
			Subject:    associationSubject,
			Narrative:  seg.NarrativeText,
			Keywords:   sliceutil.FirstNonEmpty(seg.CanonicalKeywords, seg.Keywords),
			Entities:   sliceutil.FirstNonEmpty(seg.CanonicalEntities, seg.Entities),
			TopK:       3,
		}

		if assocService != nil {
			if candidates, err := assocService.BuildCandidates(ctx, associationReq); err == nil {
				applyAssociationHints(&seg, candidates)
				injectPreferredAssociation(&seg)
			}
		}

		// Eseguiamo l'associazione stratificata
		segStarted := time.Now()
		associateSegment(ctx, &seg, assocService, req.Topic)

		// Always populate visual fields from batch results (generated at the start)
		if batchResults != nil {
			if r, ok := batchResults[seg.Index]; ok {
				seg.VisualSubject = r.VisualSubject
				seg.VisualCaption = r.VisualCaption
				seg.SearchSuggestions = sliceutil.UniqueStrings(
					append(seg.SearchSuggestions, r.Queries...),
				)
				zap.L().Info("populated visual fields from batch results",
					zap.Int("segment_index", seg.Index),
					zap.String("visual_subject", seg.VisualSubject),
					zap.Strings("queries", r.Queries),
				)
			}
		}

		// Check if matches are semantically relevant; reject if not
		if !isSemanticallyRelevant(seg, req.Topic) {
			zap.L().Warn("rejecting semantically irrelevant matches",
				zap.Int("segment_index", seg.Index),
				zap.String("topic", req.Topic),
				zap.String("subject", seg.Subject),
				zap.String("visual_subject", seg.VisualSubject),
			)
			seg.StockMatches = nil
			seg.ArtlistMatches = nil
		}

		// If no matches found (or rejected), generate Artlist queries
		if len(seg.StockMatches) == 0 && len(seg.ArtlistMatches) == 0 {
			zap.L().Info("segment has no matches, will generate LLM-based Artlist queries",
				zap.Int("segment_index", seg.Index),
				zap.String("subject", seg.Subject),
				zap.String("canonical_subject", seg.CanonicalSubject),
			)

			// If visual fields not populated from batch, generate individually
			if seg.VisualSubject == "" {
				visualResult := GenerateArtlistVisualQuery(
					ctx,
					gen,
					req.Topic,
					firstNonEmpty(seg.CanonicalSubject, seg.Subject),
					seg.NarrativeText,
					DefaultMaxQueries,
				)

				seg.VisualSubject = visualResult.VisualSubject
				seg.VisualCaption = visualResult.VisualCaption
				seg.SearchSuggestions = sliceutil.UniqueStrings(
					append(seg.SearchSuggestions, visualResult.Queries...),
				)
			}
		}
		if preserveStructuredSubjects {
			stockFiltered := association.FilterStockMatchesBySubject(seg.StockMatches, seg.Subject)
			artlistFiltered := association.FilterArtlistMatchesBySubject(seg.ArtlistMatches, seg.Subject)

			if len(artlistFiltered) > 0 && !association.HasUsefulStockMatch(stockFiltered) {
				seg.StockMatches = nil
				seg.ArtlistMatches = artlistFiltered
				seg.PreferredStockGroup = "artlist_folder"
				seg.PreferredStockPaths = association.PreferredPathsFromMatches(artlistFiltered)
				seg.PreferredStockReason = "exact artlist subject match"
			} else if len(stockFiltered) > 0 {
				for i := range stockFiltered {
					if strings.EqualFold(strings.TrimSpace(stockFiltered[i].Source), "drive_stock") && association.LooksBroadStockContainer(stockFiltered[i].Path) {
						stockFiltered[i].Path = ""
						stockFiltered[i].Link = ""
					}
				}
				seg.StockMatches = stockFiltered
				seg.PreferredStockPaths = association.PreferredPathsFromMatches(stockFiltered)
				seg.PreferredStockReason = "subject-specific stock match"
				if len(seg.PreferredStockPaths) > 0 {
					seg.PreferredStockGroup = "drive_stock"
				} else {
					seg.PreferredStockGroup = ""
				}
			} else if len(artlistFiltered) > 0 {
				seg.StockMatches = nil
				seg.ArtlistMatches = artlistFiltered
				seg.PreferredStockGroup = "artlist_folder"
				seg.PreferredStockPaths = association.PreferredPathsFromMatches(artlistFiltered)
				seg.PreferredStockReason = "artlist subject match"
			} else {
				seg.StockMatches = nil
				seg.PreferredStockGroup = ""
				seg.PreferredStockPaths = nil
				seg.PreferredStockReason = ""
			}
		}
		if err := storeSegmentInCache(ctx, cache, cacheKey, req, seg, narrative); err != nil {
			zap.L().Warn("timeline segment cache write failed",
				zap.Error(err),
				zap.String("topic", req.Topic),
				zap.String("timestamp", seg.Timestamp),
			)
		}
		zap.L().Info("timeline segment processed",
			zap.Int("index", seg.Index),
			zap.String("timestamp", seg.Timestamp),
			zap.String("subject", seg.Subject),
			zap.Int("stock_matches", len(seg.StockMatches)),
			zap.Int("artlist_matches", len(seg.ArtlistMatches)),
			zap.Int("drive_matches", len(seg.DriveMatches)),
			zap.Int("search_suggestions", len(seg.SearchSuggestions)),
			zap.Duration("elapsed", time.Since(segStarted)),
		)

		plan.Segments = append(plan.Segments, seg)
	}

	zap.L().Info("timeline plan completed",
		zap.String("topic", req.Topic),
		zap.Int("segments", len(plan.Segments)),
		zap.Duration("elapsed", time.Since(startedAt)),
	)

	return plan, nil
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

// Deprecated: kept only for timeline_render.go compatibility. Use artlist_query_generator.go instead.
func phraseToArtlistQuery(phrase string) string {
	tokens := textutil.TokenizeWithStopWords(phrase)
	cleaned := make([]string, 0, len(tokens))

	banned := map[string]bool{
		"then": true, "they": true, "something": true, "remarkably": true,
		"similar": true, "thought": true, "evidence": true, "the": true,
		"a": true, "an": true, "is": true, "was": true, "were": true,
		"are": true, "been": true, "have": true, "has": true,
		"had": true, "but": true, "and": true, "or": true,
	}

	for _, t := range tokens {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || banned[t] {
			continue
		}
		cleaned = append(cleaned, t)
		if len(cleaned) >= 4 {
			break
		}
	}

	if len(cleaned) == 0 {
		return "cinematic documentary"
	}
	return strings.Join(cleaned, " ")
}
