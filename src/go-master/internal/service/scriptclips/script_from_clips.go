// Package scriptclips provides script generation from existing clips.
package scriptclips

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/translation"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// ScriptFromClipsService generates scripts based on existing clips in Drive/Artlist
type ScriptFromClipsService struct {
	scriptGen      *ollama.Generator
	entityService  *entities.EntityService
	indexer        *clip.Indexer
	artlistSrc     *clip.ArtlistSource
	clipTranslator *translation.ClipSearchTranslator
}

// ScriptFromClipsRequest represents the request for script generation from clips
type ScriptFromClipsRequest struct {
	Topic                string `json:"topic" binding:"required"`
	Language             string `json:"language"`
	Tone                 string `json:"tone"`
	Model                string `json:"model"`
	TargetDuration       int    `json:"target_duration"`
	ClipsPerSegment      int    `json:"clips_per_segment"`
	UseArtlist           bool   `json:"use_artlist"`
	UseDriveClips        bool   `json:"use_drive_clips"`
}

// ScriptFromClipsResponse represents the response with script and clip mappings
type ScriptFromClipsResponse struct {
	OK              bool                   `json:"ok"`
	Topic           string                 `json:"topic"`
	Script          string                 `json:"script"`
	WordCount       int                    `json:"word_count"`
	EstDuration     int                    `json:"est_duration"`
	Model           string                 `json:"model"`
	Segments        []SegmentWithClips     `json:"segments"`
	TotalArtlistClips int                  `json:"total_artlist_clips"`
	TotalDriveClips  int                   `json:"total_drive_clips"`
	ProcessingTime  float64                `json:"processing_time_seconds"`
}

// SegmentWithClips represents a script segment with associated clips
type SegmentWithClips struct {
	SegmentIndex int               `json:"segment_index"`
	Text         string            `json:"text"`
	StartTime    string            `json:"start_time"`
	EndTime      string            `json:"end_time"`
	Entities     EntityResult      `json:"entities"`
	ArtlistClips []clip.IndexedClip `json:"artlist_clips"`
	DriveClips   []clip.IndexedClip `json:"drive_clips"`
	TotalClips   int               `json:"total_clips"`
}

// NewScriptFromClipsService creates a new service
func NewScriptFromClipsService(
	scriptGen *ollama.Generator,
	entityService *entities.EntityService,
	indexer *clip.Indexer,
	artlistSrc *clip.ArtlistSource,
) *ScriptFromClipsService {
	return &ScriptFromClipsService{
		scriptGen:      scriptGen,
		entityService:  entityService,
		indexer:        indexer,
		artlistSrc:     artlistSrc,
		clipTranslator: translation.NewClipSearchTranslator(),
	}
}

// GenerateScriptFromClips generates a script based on available clips
func (s *ScriptFromClipsService) GenerateScriptFromClips(ctx context.Context, req *ScriptFromClipsRequest) (*ScriptFromClipsResponse, error) {
	startTime := time.Now()
	logger.Info("Starting script generation from clips",
		zap.String("topic", req.Topic),
		zap.String("language", req.Language),
		zap.Int("target_duration", req.TargetDuration),
		zap.Int("clips_per_segment", req.ClipsPerSegment),
	)

	// Step 1: Collect available clips from Drive and/or Artlist
	var availableTopics []string
	var driveClips []clip.IndexedClip

	if req.UseDriveClips && s.indexer != nil {
		index := s.indexer.GetIndex()
		if index != nil {
			driveClips = index.Clips

			// Extract unique topics from folder paths
			seenTopics := make(map[string]bool)
			for _, clip := range driveClips {
				// Extract topic from folder path (e.g., "Stock Clips/Tesla" -> "Tesla")
				parts := splitFolderPath(clip.FolderPath)
				if len(parts) > 0 {
					topic := parts[len(parts)-1]
					if !seenTopics[topic] {
						seenTopics[topic] = true
						availableTopics = append(availableTopics, topic)
					}
				}
			}
		}
	}

	if req.UseArtlist && s.artlistSrc != nil {
		_, err := s.artlistSrc.GetAllCategories()
		if err == nil {
			// Categories available, will search during segment processing
		}
	}

	logger.Info("Available clip topics",
		zap.Int("drive_clips", len(driveClips)),
		zap.Strings("topics", availableTopics),
	)

	// Step 2: Build source text from available clips
	sourceText := s.buildSourceText(req.Topic, availableTopics, driveClips)

	// Step 3: Generate script from source text
	scriptResult, err := s.scriptGen.GenerateFromText(ctx, &ollama.TextGenerationRequest{
		SourceText: sourceText,
		Title:      req.Topic,
		Language:   req.Language,
		Duration:   req.TargetDuration,
		Tone:       req.Tone,
		Model:      req.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	logger.Info("Script generated, extracting entities",
		zap.Int("word_count", scriptResult.WordCount),
	)

	// Step 4: Segment script and extract entities
	segmentConfig := entities.SegmentConfig{
		TargetWordsPerSegment: 50,
		MinSegments:         1,
		MaxSegments:         20,
		OverlapWords:        5,
	}

	analysis, err := s.entityService.AnalyzeScript(ctx, scriptResult.Script, 12, segmentConfig)
	if err != nil {
		return nil, fmt.Errorf("entity analysis failed: %w", err)
	}

	logger.Info("Entity extraction completed",
		zap.Int("total_segments", analysis.TotalSegments),
		zap.Int("total_entities", analysis.TotalEntities),
	)

	// Step 5: Calculate timestamps
	segments := s.calculateTimestamps(analysis, scriptResult.EstDuration)

	// Step 6: For each segment, find 3 Artlist clips + available Drive clips
	totalArtlistClips := 0
	totalDriveClips := 0

	for i := range segments {
		segment := &segments[i]

		// Collect entity names for searching
		entityNames := s.collectEntityNames(segment.Entities)

		// Find Artlist clips (3 per segment)
		for _, entityName := range entityNames {
			searchQueryEN := s.clipTranslator.TranslateQuery(entityName)

			if req.UseArtlist && s.artlistSrc != nil {
				artClips, err := s.artlistSrc.SearchClips(searchQueryEN, req.ClipsPerSegment)
				if err == nil {
					segment.ArtlistClips = append(segment.ArtlistClips, artClips...)
					totalArtlistClips += len(artClips)
				}
			}
		}

		// Find Drive clips by matching entity names
		if req.UseDriveClips {
			for _, entityName := range entityNames {
				searchQueryEN := s.clipTranslator.TranslateQuery(entityName)
				matchedClips := s.findDriveClipsByEntity(searchQueryEN, driveClips)
				segment.DriveClips = append(segment.DriveClips, matchedClips...)
				totalDriveClips += len(matchedClips)
			}
		}

		segment.TotalClips = len(segment.ArtlistClips) + len(segment.DriveClips)
	}

	processingTime := time.Since(startTime).Seconds()

	logger.Info("Script generation from clips completed",
		zap.Int("segments", len(segments)),
		zap.Int("artlist_clips", totalArtlistClips),
		zap.Int("drive_clips", totalDriveClips),
		zap.Float64("processing_time", processingTime),
	)

	return &ScriptFromClipsResponse{
		OK:              true,
		Topic:           req.Topic,
		Script:          scriptResult.Script,
		WordCount:       scriptResult.WordCount,
		EstDuration:     scriptResult.EstDuration,
		Model:           scriptResult.Model,
		Segments:        segments,
		TotalArtlistClips: totalArtlistClips,
		TotalDriveClips:  totalDriveClips,
		ProcessingTime:  processingTime,
	}, nil
}

// buildSourceText creates source text from available clips
func (s *ScriptFromClipsService) buildSourceText(topic string, topics []string, clips []clip.IndexedClip) string {
	// Start with the main topic
	sourceText := fmt.Sprintf("Crea uno script video su: %s.\n\n", topic)

	// Add context from available topics
	if len(topics) > 0 {
		sourceText += "Clip disponibili per i seguenti argomenti:\n"
		for _, t := range topics {
			sourceText += fmt.Sprintf("- %s\n", t)
		}
		sourceText += "\n"
	}

	// Add some clip examples
	if len(clips) > 0 {
		sourceText += fmt.Sprintf("Sono disponibili %d clip video.\n", len(clips))
		sourceText += "Integra queste risorse nel racconto.\n"
	}

	return sourceText
}

// findDriveClipsByEntity finds Drive clips matching an entity
func (s *ScriptFromClipsService) findDriveClipsByEntity(entityName string, clips []clip.IndexedClip) []clip.IndexedClip {
	var matched []clip.IndexedClip
	searchTerms := extractSearchTerms(entityName)

	for _, clip := range clips {
		// Match against clip tags, name, or folder path
		if clipMatches(clip, searchTerms) {
			matched = append(matched, clip)
		}
	}

	// Limit to first 3 matches
	if len(matched) > 3 {
		matched = matched[:3]
	}

	return matched
}

// clipMatches checks if a clip matches search terms
func clipMatches(clip clip.IndexedClip, terms []string) bool {
	// Check tags
	for _, tag := range clip.Tags {
		for _, term := range terms {
			if containsIgnoreCase(tag, term) {
				return true
			}
		}
	}

	// Check name
	for _, term := range terms {
		if containsIgnoreCase(clip.Name, term) {
			return true
		}
	}

	// Check folder path
	for _, term := range terms {
		if containsIgnoreCase(clip.FolderPath, term) {
			return true
		}
	}

	return false
}

// extractSearchTerms extracts search terms from entity name
func extractSearchTerms(entity string) []string {
	var terms []string
	for _, word := range strings.Fields(entity) {
		if len(word) > 2 {
			terms = append(terms, word)
		}
	}
	return terms
}

// Helper functions

func splitFolderPath(path string) []string {
	_, folder := filepath.Split(path)
	if folder == "" {
		return nil
	}
	return strings.Split(filepath.Clean(path), string(filepath.Separator))
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func (s *ScriptFromClipsService) calculateTimestamps(analysis *entities.ScriptEntityAnalysis, totalDuration int) []SegmentWithClips {
	if analysis == nil || len(analysis.SegmentEntities) == 0 {
		return []SegmentWithClips{}
	}

	segments := make([]SegmentWithClips, len(analysis.SegmentEntities))
	totalSegments := len(analysis.SegmentEntities)
	secondsPerSegment := totalDuration / totalSegments

	for i, seg := range analysis.SegmentEntities {
		startSec := i * secondsPerSegment
		endSec := startSec + secondsPerSegment

		if i == totalSegments-1 {
			endSec = totalDuration
		}

		segments[i] = SegmentWithClips{
			SegmentIndex: seg.SegmentIndex,
			Text:         seg.SegmentText,
			StartTime:    formatTime(startSec),
			EndTime:      formatTime(endSec),
			Entities: EntityResult{
				FrasiImportanti:  seg.FrasiImportanti,
				NomiSpeciali:     seg.NomiSpeciali,
				ParoleImportanti: seg.ParoleImportanti,
				EntitaSenzaTesto: seg.EntitaSenzaTesto,
			},
			ArtlistClips: []clip.IndexedClip{},
			DriveClips:   []clip.IndexedClip{},
		}
	}

	return segments
}

func (s *ScriptFromClipsService) collectEntityNames(entity EntityResult) []string {
	seen := make(map[string]bool)
	names := []string{}

	for _, name := range entity.NomiSpeciali {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	for _, phrase := range entity.FrasiImportanti {
		if !seen[phrase] {
			seen[phrase] = true
			names = append(names, phrase)
		}
	}

	for _, word := range entity.ParoleImportanti {
		if !seen[word] {
			seen[word] = true
			names = append(names, word)
		}
	}

	return names
}

