package scriptclips

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/core/entities"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// calculateTimestamps assigns start/end times to each segment
func (s *ScriptClipsService) calculateTimestamps(analysis *entities.ScriptEntityAnalysis, totalDuration int) []SegmentClipMapping {
	if analysis == nil || len(analysis.SegmentEntities) == 0 {
		return []SegmentClipMapping{}
	}

	segments := make([]SegmentClipMapping, len(analysis.SegmentEntities))
	totalSegments := len(analysis.SegmentEntities)
	secondsPerSegment := totalDuration / totalSegments

	for i, seg := range analysis.SegmentEntities {
		startSec := i * secondsPerSegment
		endSec := startSec + secondsPerSegment

		if i == totalSegments-1 {
			endSec = totalDuration
		}

		segments[i] = SegmentClipMapping{
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
			ClipMappings: []ClipMapping{},
		}
	}

	return segments
}

// collectEntityNames gathers unique entity names from a segment
func (s *ScriptClipsService) collectEntityNames(entity EntityResult) []string {
	seen := make(map[string]bool)
	names := []string{}

	// Priority 1: NomiSpeciali
	for _, name := range entity.NomiSpeciali {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 50 {
			continue
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	// Priority 2: ParoleImportanti
	for _, word := range entity.ParoleImportanti {
		word = strings.TrimSpace(word)
		if word == "" || len(word) > 30 {
			continue
		}
		if !seen[word] {
			seen[word] = true
			names = append(names, word)
		}
	}

	// Priority 3: Short FrasiImportanti only
	for _, phrase := range entity.FrasiImportanti {
		phrase = strings.TrimSpace(phrase)
		if phrase == "" || len(phrase) > 40 {
			continue
		}
		wordCount := len(strings.Fields(phrase))
		if wordCount > 4 {
			continue
		}
		if !seen[phrase] {
			seen[phrase] = true
			names = append(names, phrase)
		}
	}

	if len(names) > 8 {
		names = names[:8]
	}

	return names
}

// entityTask represents a single entity to process
type entityTask struct {
	segmentIndex int
	entityName   string
}

// entityResult represents the result of processing an entity
type entityResult struct {
	segmentIndex int
	entityName   string
	mapping      ClipMapping
}

// processClipEntities processes all entities across all segments in parallel
func (s *ScriptClipsService) processClipEntities(ctx context.Context, segments []SegmentClipMapping, reportProgress func(string, int, string, string, int, int)) (int, int, error) {
	var tasks []entityTask
	for i := range segments {
		entityNames := s.collectEntityNames(segments[i].Entities)
		for _, entityName := range entityNames {
			tasks = append(tasks, entityTask{segmentIndex: i, entityName: entityName})
		}
	}

	totalEntities := len(tasks)
	logger.Info("Starting parallel clip processing", zap.Int("total_entities", totalEntities))

	if totalEntities == 0 {
		reportProgress("completed", 100, "No entities to process", "", 0, 0)
		return 0, 0, nil
	}

	reportProgress("clip_processing", 45, fmt.Sprintf("Starting clip processing for %d entities", totalEntities), "", 0, totalEntities)

	// Setup parallel workers
	maxWorkers := 5
	if len(tasks) < maxWorkers {
		maxWorkers = len(tasks)
	}
	if maxWorkers == 0 {
		maxWorkers = 1
	}

	resultCh := make(chan entityResult, len(tasks))
	taskCh := make(chan entityTask, len(tasks))

	for _, task := range tasks {
		taskCh <- task
	}
	close(taskCh)

	// Track progress
	var clipsProcessed int
	var mu sync.Mutex

	var wg sync.WaitGroup
	for w := 0; w < maxWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range taskCh {
				time.Sleep(time.Duration(workerID) * 500 * time.Millisecond)

				mu.Lock()
				currentProcessed := clipsProcessed
				mu.Unlock()

				reportProgress("clip_download", 45+int(float64(currentProcessed)/float64(totalEntities)*50),
					fmt.Sprintf("Processing clip for: %s", task.entityName), task.entityName, currentProcessed, totalEntities)

				mapping := s.findOrDownloadClip(ctx, task.entityName)

				mu.Lock()
				clipsProcessed++
				currentDone := clipsProcessed
				mu.Unlock()

				progressPct := 45 + int(float64(currentDone)/float64(totalEntities)*50)
				if mapping.ClipFound {
					reportProgress("clip_download", progressPct,
						fmt.Sprintf("✓ Clip found for '%s' (%d/%d)", task.entityName, currentDone, totalEntities),
						task.entityName, currentDone, totalEntities)
				} else {
					reportProgress("clip_download", progressPct,
						fmt.Sprintf("✗ Clip not found for '%s' (%d/%d)", task.entityName, currentDone, totalEntities),
						task.entityName, currentDone, totalEntities)
				}

				resultCh <- entityResult{
					segmentIndex: task.segmentIndex,
					entityName:   task.entityName,
					mapping:      mapping,
				}
			}
		}(w)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results
	totalClipsFound := 0
	totalClipsMissing := 0

	for result := range resultCh {
		segments[result.segmentIndex].ClipMappings = append(
			segments[result.segmentIndex].ClipMappings,
			result.mapping,
		)

		if result.mapping.ClipFound {
			totalClipsFound++
		} else {
			totalClipsMissing++
		}
	}

	reportProgress("completed", 100,
		fmt.Sprintf("Pipeline completed: %d clips found, %d missing", totalClipsFound, totalClipsMissing),
		"", totalClipsFound, totalClipsFound+totalClipsMissing)

	return totalClipsFound, totalClipsMissing, nil
}

// formatTime converts seconds to HH:MM:SS format
func formatTime(seconds int) string {
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
}

// sanitizeFilename removes special characters from filenames
func sanitizeFilename(name string) string {
	result := ""
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			result += string(c)
		} else {
			result += "_"
		}
	}
	return result
}
