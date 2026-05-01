package script

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go.uber.org/zap"
	"strings"
	"time"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/service/artlist"
	segmentnorm "velox/go-master/internal/service/catalognormalizer"
)

const timelineCacheVersion = "v8"

// BuildTimelinePlan coordinates the LLM planning and asset matching.
func BuildTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, nodeScraperDir, sourceText, narrative string, stockRepo, artlistRepo, clipsRepo *clips.Repository, artlistService *artlistSvc.Service) (*TimelinePlan, error) {
	startedAt := time.Now()
	zap.L().Info("Building timeline plan", zap.String("topic", req.Topic))

	cacheKey := buildTimelineCacheKey(req, sourceText)
	if cached, err := loadCachedTimelinePlan(ctx, clipsRepo, cacheKey); err == nil && cached != nil {
		zap.L().Info("timeline plan cache hit",
			zap.String("topic", req.Topic),
			zap.String("cache_key", cacheKey),
			zap.Int("segments", len(cached.Segments)),
		)
		return cached, nil
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

	plan := &TimelinePlan{
		PrimaryFocus:  req.Topic,
		SegmentCount:  len(rawPlan.Segments),
		TotalDuration: req.Duration,
		Segments:      make([]TimelineSegment, 0, len(rawPlan.Segments)),
	}
	if clipsRepo != nil {
		if err := clipsRepo.DeleteSegmentEmbeddingsByScriptKey(ctx, cacheKey); err != nil {
			zap.L().Warn("failed to clear timeline cache key", zap.String("cache_key", cacheKey), zap.Error(err))
		}
	}

	// 2. ASSET MATCHING STRATEGY (MODULAR)
	normalizer := segmentnorm.NewService(stockRepo, clipsRepo, artlistRepo, zap.L())
	driveAssoc := NewDriveStockAssociation(dataDir)
	artlistFolderAssoc := NewArtlistFolderAssociation(artlistRepo, nodeScraperDir, req.Topic)
	artlistAssoc := NewArtlistStockAssociation(artlistService)
	clipAssoc := NewClipDriveAssociation(clipsRepo)
	dynamicAssoc := NewDynamicArtlistAssociation(artlistService, gen, req, narrative)

	for i, rawSeg := range rawPlan.Segments {
		seg := TimelineSegment{
			Index:           i + 1,
			StartTime:       rawSeg.StartTime,
			EndTime:         rawSeg.EndTime,
			Timestamp:       fmt.Sprintf("%.0f-%.0f", rawSeg.StartTime, rawSeg.EndTime),
			Subject:         strings.TrimSpace(rawSeg.Subject),
			NarrativeText:   rawSeg.NarrativeText,
			OpeningSentence: rawSeg.OpeningSentence,
			ClosingSentence: rawSeg.ClosingSentence,
			Keywords:        rawSeg.Keywords,
			Entities:        rawSeg.Entities,
		}
		seg.Subject = resolveTimelineSegmentSubject(ctx, req, seg, dataDir, stockRepo)

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
			seg.CanonicalKeywords = uniqueStrings(normalized.CanonicalKeywords)
			seg.CanonicalEntities = uniqueStrings(normalized.CanonicalEntities)
			seg.NormalizationSource = normalized.NormalizationSource
		}
		if strings.TrimSpace(seg.CanonicalSubject) == "" {
			seg.CanonicalSubject = seg.Subject
		}

		associationReq := AssociationCandidatesRequest{
			Topic:      req.Topic,
			SegmentKey: seg.Timestamp,
			Timestamp:  seg.Timestamp,
			Subject:    firstNonEmpty(seg.CanonicalSubject, seg.Subject),
			Narrative:  seg.NarrativeText,
			Keywords:   firstNonEmptySlice(seg.CanonicalKeywords, seg.Keywords),
			Entities:   firstNonEmptySlice(seg.CanonicalEntities, seg.Entities),
			TopK:       3,
		}

		if candidates, err := BuildAssociationCandidates(ctx, associationReq, dataDir, nodeScraperDir, stockRepo, artlistRepo, clipsRepo); err == nil {
			applyAssociationHints(&seg, candidates)
			injectPreferredAssociation(&seg)
		}

		// Eseguiamo l'associazione stratificata
		segStarted := time.Now()
		associateSegment(ctx, &seg, driveAssoc, artlistFolderAssoc, clipAssoc, artlistAssoc, dynamicAssoc)
		if err := storeTimelineSegmentCache(ctx, gen, clipsRepo, cacheKey, req, seg, narrative); err != nil {
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

func buildTimelineCacheKey(req ScriptDocsRequest, sourceText string) string {
	payload, _ := json.Marshal([]string{
		timelineCacheVersion,
		strings.ToLower(strings.TrimSpace(req.Topic)),
		strings.ToLower(strings.TrimSpace(req.Template)),
		fmt.Sprintf("%d", req.Duration),
		strings.ToLower(strings.TrimSpace(sourceText)),
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func hashSegmentPayload(req ScriptDocsRequest, seg TimelineSegment) string {
	payload, _ := json.Marshal([]any{
		timelineCacheVersion,
		strings.ToLower(strings.TrimSpace(req.Topic)),
		req.Duration,
		strings.ToLower(strings.TrimSpace(req.Template)),
		strings.TrimSpace(seg.NarrativeText),
		uniqueStrings(seg.Keywords),
		uniqueStrings(seg.Entities),
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func loadCachedTimelinePlan(ctx context.Context, repo *clips.Repository, cacheKey string) (*TimelinePlan, error) {
	if repo == nil || strings.TrimSpace(cacheKey) == "" {
		return nil, nil
	}
	if err := repo.EnsureSegmentEmbeddingsSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := repo.GetSegmentEmbeddingsByScriptKey(ctx, cacheKey)
	if err != nil || len(rows) == 0 {
		return nil, err
	}

	plan := &TimelinePlan{
		PrimaryFocus:  "",
		SegmentCount:  len(rows),
		TotalDuration: rows[0].Duration,
		Segments:      make([]TimelineSegment, 0, len(rows)),
	}
	if len(rows) > 0 {
		plan.PrimaryFocus = rows[0].Topic
	}
	for _, row := range rows {
		var seg TimelineSegment
		if err := json.Unmarshal([]byte(row.SegmentJSON), &seg); err != nil {
			seg = TimelineSegment{
				Index:            row.SegmentIndex,
				Subject:          row.RawSubject,
				CanonicalSubject: row.CanonicalSubject,
				Keywords:         mustUnmarshalStringSlice(row.RawKeywordsJSON),
				Entities:         mustUnmarshalStringSlice(row.RawEntitiesJSON),
			}
		}
		if seg.Index == 0 {
			seg.Index = row.SegmentIndex
		}
		if seg.CanonicalSubject == "" {
			seg.CanonicalSubject = row.CanonicalSubject
		}
		if len(seg.CanonicalKeywords) == 0 {
			seg.CanonicalKeywords = mustUnmarshalStringSlice(row.CanonicalKeywordsJSON)
		}
		if len(seg.CanonicalEntities) == 0 {
			seg.CanonicalEntities = mustUnmarshalStringSlice(row.CanonicalEntitiesJSON)
		}
		plan.Segments = append(plan.Segments, seg)
	}
	return plan, nil
}

func storeTimelineSegmentCache(ctx context.Context, gen *ollama.Generator, repo *clips.Repository, cacheKey string, req ScriptDocsRequest, seg TimelineSegment, narrative string) error {
	if repo == nil || strings.TrimSpace(cacheKey) == "" {
		return nil
	}
	if err := repo.EnsureSegmentEmbeddingsSchema(ctx); err != nil {
		return err
	}

	bestSource, bestPath, bestLink, bestScore := bestMatchFromSegment(seg)
	payload, err := json.Marshal(seg)
	if err != nil {
		return err
	}

	embeddingJSON := "[]"
	if gen != nil && gen.GetClient() != nil {
		embeddingText := strings.TrimSpace(strings.Join([]string{
			seg.CanonicalSubject,
			strings.Join(seg.CanonicalKeywords, " "),
			strings.Join(seg.CanonicalEntities, " "),
			seg.NarrativeText,
		}, " | "))
		if embeddingText == "" {
			embeddingText = strings.TrimSpace(narrative)
		}
		if embeddingText != "" {
			if embedding, err := gen.GetClient().Embed(ctx, embeddingText); err == nil && len(embedding) > 0 {
				if embeddingData, err := json.Marshal(embedding); err == nil {
					embeddingJSON = string(embeddingData)
				}
			}
		}
	}

	return repo.UpsertSegmentEmbedding(ctx, &clips.SegmentEmbeddingRecord{
		ScriptKey:             cacheKey,
		SourceHash:            hashSegmentPayload(req, seg),
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

	for _, matches := range [][]scoredMatch{seg.StockMatches, seg.ArtlistMatches, seg.DriveMatches} {
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

func mustUnmarshalStringSlice(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptySlice(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func resolveTimelineSegmentSubject(ctx context.Context, req ScriptDocsRequest, seg TimelineSegment, dataDir string, stockRepo *clips.Repository) string {
	topic := strings.TrimSpace(req.Topic)
	rawSubject := strings.TrimSpace(seg.Subject)

	if direct, ok, err := findDirectStockFolderCandidate(ctx, stockRepo, dataDir, topic, rawSubject); err == nil && ok && direct != nil {
		if topic != "" && looksLikePersonName(topic) {
			return topic
		}
		if name := strings.TrimSpace(direct.Name); name != "" {
			return name
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
	if concise := conciseSubject(seg.OpeningSentence); concise != "" {
		return concise
	}
	if concise := conciseSubject(seg.ClosingSentence); concise != "" {
		return concise
	}
	if topic != "" {
		return topic
	}
	return rawSubject
}

func injectPreferredAssociation(seg *TimelineSegment) {
	if seg == nil {
		return
	}
	if len(seg.StockMatches) > 0 || len(seg.ArtlistMatches) > 0 || len(seg.DriveMatches) > 0 {
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

	match := scoredMatch{
		Title:   title,
		Path:    path,
		Score:   80,
		Link:    link,
		Details: seg.PreferredStockReason,
	}

	switch strings.ToLower(strings.TrimSpace(seg.PreferredStockGroup)) {
	case "stock_folder":
		match.Source = "drive_stock"
		seg.StockMatches = append(seg.StockMatches, match)
	case "clip_folder":
		match.Source = "clip_drive"
		seg.DriveMatches = append(seg.DriveMatches, match)
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

func associateSegment(ctx context.Context, seg *TimelineSegment, driveAssoc *DriveStockAssociation, artlistFolderAssoc *ArtlistFolderAssociation, clipAssoc *ClipDriveAssociation, artlistAssoc *ArtlistStockAssociation, dynamicAssoc *DynamicArtlistAssociation) {
	// 1. Cerca in Drive Stock (Cartelle locali)
	if driveAssoc != nil {
		matches, _ := driveAssoc.Associate(ctx, seg)
		if len(matches) > 0 {
			seg.StockMatches = append(seg.StockMatches, matches...)
		}
	}

	// 2. Cerca in cartelle Artlist
	if artlistFolderAssoc != nil {
		matches, _ := artlistFolderAssoc.Associate(ctx, seg)
		if len(matches) > 0 {
			seg.ArtlistMatches = append(seg.ArtlistMatches, matches...)
		}
	}

	// 3. Cerca in Clip Drive (Clip già scaricate)
	if clipAssoc != nil {
		matches, _ := clipAssoc.Associate(ctx, seg)
		if len(matches) > 0 {
			seg.DriveMatches = matches
			seg.StockMatches = append(seg.StockMatches, matches...)
		}
	}

	// 4. Cerca in Artlist Stock (Database Artlist)
	if artlistAssoc != nil {
		matches, _ := artlistAssoc.Associate(ctx, seg)
		if len(matches) > 0 {
			seg.ArtlistMatches = append(seg.ArtlistMatches, matches...)
			seg.StockMatches = append(seg.StockMatches, matches...)
		}
	}

	// 5. Fallback Dinamico (Estrae keyword e tenta ricerca live)
	if dynamicAssoc != nil {
		matches, _ := dynamicAssoc.Associate(ctx, seg)
		if len(matches) > 0 {
			seg.ArtlistMatches = append(seg.ArtlistMatches, matches...)
			seg.StockMatches = append(seg.StockMatches, matches...)
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
				OpeningSentence: "Inizio del video su " + topic,
			},
		},
	}
}
