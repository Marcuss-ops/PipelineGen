// Package timestamp gestisce il collegamento tra timestamp del testo e clip esistenti
package timestamp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"velox/go-master/internal/clip"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// Service implementa il Mapper interface
type Service struct {
	indexer     *clip.Indexer
	suggester   *clip.SemanticSuggester
	artlistSrc  *clip.ArtlistSource
}

// NewService crea un nuovo servizio di timestamp mapping
func NewService(indexer *clip.Indexer, artlistSrc *clip.ArtlistSource) *Service {
	suggester := clip.NewSemanticSuggester(indexer)
	
	return &Service{
		indexer:    indexer,
		suggester:  suggester,
		artlistSrc: artlistSrc,
	}
}

// MapSegmentsToClips mappa segmenti di testo a clip esistenti (Drive + Artlist)
func (s *Service) MapSegmentsToClips(ctx context.Context, req *MappingRequest) (*TimestampMapping, error) {
	logger.Info("Starting timestamp-to-clip mapping",
		zap.String("script_id", req.ScriptID),
		zap.Int("total_segments", len(req.Segments)),
	)

	if req.MaxClipsPerSegment == 0 {
		req.MaxClipsPerSegment = 3
	}
	if req.MinScore == 0 {
		req.MinScore = 20
	}

	mapping := &TimestampMapping{
		ScriptID: req.ScriptID,
		CreatedAt: time.Now(),
		Segments: make([]SegmentWithClips, 0, len(req.Segments)),
	}

	totalClips := 0
	totalScore := 0.0

	// Per ogni segmento, trova clip pertinenti
	for i, segment := range req.Segments {
		segmentWithClips := SegmentWithClips{
			Segment: segment,
		}

		// Cerca clip Drive
		if req.IncludeDrive {
			driveClips := s.findDriveClips(ctx, segment, req)
			segmentWithClips.AssignedClips = append(segmentWithClips.AssignedClips, driveClips...)
		}

		// Cerca clip Artlist
		if req.IncludeArtlist && s.artlistSrc != nil {
			artlistClips := s.findArtlistClips(ctx, segment, req)
			segmentWithClips.AssignedClips = append(segmentWithClips.AssignedClips, artlistClips...)
		}

		// Ordina per score decrescente e limita
		sort.Slice(segmentWithClips.AssignedClips, func(a, b int) bool {
			return segmentWithClips.AssignedClips[a].RelevanceScore > segmentWithClips.AssignedClips[b].RelevanceScore
		})

		if len(segmentWithClips.AssignedClips) > req.MaxClipsPerSegment {
			segmentWithClips.AssignedClips = segmentWithClips.AssignedClips[:req.MaxClipsPerSegment]
		}

		// Calcola best score
		if len(segmentWithClips.AssignedClips) > 0 {
			segmentWithClips.BestScore = segmentWithClips.AssignedClips[0].RelevanceScore
		}
		segmentWithClips.ClipCount = len(segmentWithClips.AssignedClips)

		totalClips += segmentWithClips.ClipCount
		totalScore += segmentWithClips.BestScore

		mapping.Segments = append(mapping.Segments, segmentWithClips)

		logger.Debug("Segment mapped",
			zap.Int("segment_index", i),
			zap.Int("clips_found", segmentWithClips.ClipCount),
			zap.Float64("best_score", segmentWithClips.BestScore),
		)
	}

	// Calcola durata totale
	if len(req.Segments) > 0 {
		lastSegment := req.Segments[len(req.Segments)-1]
		mapping.TotalDuration = lastSegment.EndTime
	}

	// Calcola score medio
	if len(mapping.Segments) > 0 {
		mapping.AverageScore = totalScore / float64(len(mapping.Segments))
	}

	logger.Info("Timestamp-to-clip mapping completed",
		zap.Int("total_segments", len(mapping.Segments)),
		zap.Int("total_clips", totalClips),
		zap.Float64("average_score", mapping.AverageScore),
	)

	return mapping, nil
}

// findDriveClips cerca clip Drive pertinenti per un segmento
func (s *Service) findDriveClips(ctx context.Context, segment TextSegment, req *MappingRequest) []ClipAssignment {
	// Costruisce query di ricerca dai keywords/entities del segmento
	query := buildSearchQuery(segment)
	
	if query == "" {
		return nil
	}

	// Usa semantic suggester
	suggestions := s.suggester.SuggestForSentence(
		ctx,
		query,
		req.MaxClipsPerSegment * 2, // Cerca di più per avere scelta
		req.MinScore,
		req.MediaType,
	)

	var assignments []ClipAssignment
	for _, suggestion := range suggestions {
		assignments = append(assignments, ClipAssignment{
			ClipID:         suggestion.Clip.ID,
			Source:         "drive",
			Name:           suggestion.Clip.Name,
			FolderPath:     suggestion.Clip.FolderPath,
			RelevanceScore: suggestion.Score,
			Duration:       suggestion.Clip.Duration,
			DriveLink:      suggestion.Clip.DriveLink,
			MatchReason:    suggestion.MatchReason,
		})
	}

	return assignments
}

// findArtlistClips cerca clip Artlist pertinenti per un segmento
func (s *Service) findArtlistClips(ctx context.Context, segment TextSegment, req *MappingRequest) []ClipAssignment {
	if s.artlistSrc == nil {
		return nil
	}

	query := buildSearchQuery(segment)
	if query == "" {
		return nil
	}

	// Cerca su Artlist
	artlistClips, err := s.artlistSrc.SearchClips(query, req.MaxClipsPerSegment * 2)
	if err != nil {
		logger.Warn("Artlist search failed",
			zap.Error(err),
			zap.String("query", query),
		)
		return nil
	}

	// Score delle clip Artlist con semantic suggester
	var assignments []ClipAssignment
	for _, clip := range artlistClips {
		score := calculateArtlistScore(clip, segment)
		
		if score >= req.MinScore {
			assignments = append(assignments, ClipAssignment{
				ClipID:         clip.ID,
				Source:         "artlist",
				Name:           clip.Name,
				FolderPath:     clip.FolderPath,
				RelevanceScore: score,
				Duration:       clip.Duration,
				DriveLink:      clip.DriveLink,
				MatchReason:    fmt.Sprintf("Artlist match: %s", clip.Name),
			})
		}
	}

	return assignments
}

// buildSearchQuery costruisce una query di ricerca da un segmento
func buildSearchQuery(segment TextSegment) string {
	// Combina keywords ed entities per la ricerca
	var parts []string
	
	// Aggiungi keywords
	parts = append(parts, segment.Keywords...)
	
	// Aggiungi entities (sono importanti per il matching)
	parts = append(parts, segment.Entities...)
	
	// Se non c'è nulla, usa il testo
	if len(parts) == 0 && segment.Text != "" {
		return segment.Text
	}

	if len(parts) == 0 {
		return ""
	}

	// Unisci le prime 5 parole chiave
	if len(parts) > 5 {
		parts = parts[:5]
	}

	return joinStrings(parts, " ")
}

// calculateArtlistScore calcola il score di pertinenza per una clip Artlist
func calculateArtlistScore(clip clip.IndexedClip, segment TextSegment) float64 {
	var score float64

	textLower := strings.ToLower(segment.Text)
	clipNameLower := strings.ToLower(clip.Name)

	// Keywords match
	for _, kw := range segment.Keywords {
		kwLower := strings.ToLower(kw)
		if strings.Contains(clipNameLower, kwLower) || strings.Contains(textLower, kwLower) {
			score += 30
		}
	}

	// Entities match (più importante)
	for _, entity := range segment.Entities {
		entityLower := strings.ToLower(entity)
		if strings.Contains(clipNameLower, entityLower) {
			score += 50
		}
	}

	// Tags match
	for _, tag := range clip.Tags {
		if strings.Contains(textLower, strings.ToLower(tag)) {
			score += 20
		}
	}

	// Normalize a 0-100
	if score > 100 {
		score = 100
	}

	return score
}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
