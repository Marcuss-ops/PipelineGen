// Package usecase — clip_source_text.go owns the text/display domain
// of ClipSourceBuilder (PR-REFACTOR-P1-CYCLOMATIC extraction):
//
//   - truncateExcerpt — rune-safe excerpt truncation (canonical
//     truncation layer, pinned by pathological_inputs_p2c_test.go).
//   - clipDisplayName — the per-clip display-name projection.
//   - chronologicalSortKey / clipTimeline — the ordering projections
//     (start_ms/end_ms + chunk_index/chunk_duration_sec).
//   - parseMetadataMs / dedupTrimmedClipIDs — parsing + ID dedup.
//   - optsRequireDriveLink / optsResolveLanguage — option projections.
//   - clipParallelism — the bounded-parallelism projection.
//
// These are pure helpers consumed by BuildClipContext
// (clip_source_builder.go) and the evidence builder
// (clip_source_evidence.go); this file owns no orchestration.
package usecase

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

const excerptMaxRunes = 500
const defaultClipParallelism = 4

// truncateExcerpt returns s if its rune count is at most maxRunes;
// otherwise it returns s truncated to exactly maxRunes runes followed by
// the U+2026 HORIZONTAL ELLIPSIS.
func truncateExcerpt(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := make([]rune, 0, maxRunes+1)
	for _, r := range s {
		if len(runes) == maxRunes {
			break
		}
		runes = append(runes, r)
	}
	return string(runes) + "\u2026"
}

func clipDisplayName(clip *asset.Asset, id string) string {
	if name := strings.TrimSpace(clip.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(clip.Filename); name != "" {
		return name
	}
	return id
}

func chronologicalSortKey(clip *asset.Asset, id string) int64 {
	if clip != nil {
		startMs, _ := clipTimeline(clip)
		if startMs >= 0 {
			return startMs
		}
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			continue
		}
		j := i + 1
		for j < len(id) && id[j] >= '0' && id[j] <= '9' {
			j++
		}
		if n, err := strconv.ParseInt(id[i:j], 10, 64); err == nil {
			return n
		}
		i = j - 1
	}
	return int64(^uint64(0) >> 1)
}

// clipTimeline is the single timestamp projection for indexed clips. Ingested
// boxing chunks commonly carry chunk_index + chunk_duration_sec instead of
// explicit millisecond offsets; both representations must produce the same
// binding contract downstream.
func clipTimeline(clip *asset.Asset) (int64, int64) {
	if clip == nil {
		return -1, -1
	}
	startMs := int64(clip.GetMetadataInt("start_ms"))
	endMs := int64(clip.GetMetadataInt("end_ms"))
	if endMs > startMs {
		return startMs, endMs
	}
	chunkIndex := int64(clip.GetMetadataInt("chunk_index"))
	chunkDurationSec := int64(clip.GetMetadataInt("chunk_duration_sec"))
	if chunkIndex >= 0 && chunkDurationSec > 0 {
		startMs = chunkIndex * chunkDurationSec * 1000
		return startMs, startMs + chunkDurationSec*1000
	}
	return -1, -1
}

func parseMetadataMs(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return ms
}

func dedupTrimmedClipIDs(clipIDs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(clipIDs))
	uniqueIDs := make([]string, 0, len(clipIDs))
	for _, id := range clipIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return nil, fmt.Errorf("clip source builder: no valid clip IDs provided")
	}
	return uniqueIDs, nil
}

func optsRequireDriveLink(opts *ClipGenerationOptions) bool {
	if opts == nil {
		return true
	}
	return opts.RequireDriveLink
}

func optsResolveLanguage(opts *ClipGenerationOptions) string {
	if opts == nil {
		return ""
	}
	return strings.TrimSpace(opts.Language)
}

func clipParallelism(count int) int {
	if count <= 0 {
		return 1
	}
	if count < defaultClipParallelism {
		return count
	}
	return defaultClipParallelism
}
