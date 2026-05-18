package script

import (
	"context"
	"encoding/json"
	"strings"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/media/association"
	"velox/go-master/internal/pkg/textutil"
)

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

func storeSegmentInCache(ctx context.Context, c *Cache, cacheKey string, req ScriptDocsRequest, seg TimelineSegment, narrative string) error {
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
