// Package segments provides segment-level helpers for YouTube clip extraction.
// During PR5 Phase 4 (June 2026), standalone functions were consolidated into a
// cohesive Service struct to enable dependency injection and smaller call
// signatures.
package usecase

import (
	"fmt"
	"os"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Service provides segment-level helpers for youtube clip extraction.
// Zero dependencies — all state comes from method parameters.
type SegmentsService struct{}

// NewService is the canonical constructor.
func NewSegmentsService() *SegmentsService {
	return &SegmentsService{}
}

// ── Filename and validation helpers ────────────────────────────────────────

// FileSizeFromPath returns the file size in bytes, or 0 if the file cannot be stat'd.
func (s *SegmentsService) FileSizeFromPath(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// BuildClipFilename constructs a canonical YouTube clip filename from video ID,
// timestamps, a human-readable name, AND the policy version.
//
// Commit 2/6 (PR-C-YouTube-Cutover, June 2026, Correttezza #4): the
// policyVersion is stamped into the filename so two policy versions
// of the same (videoID, start, end) tuple produce different files
// (different local paths + different Drive names). Without the stamp,
// re-extraction under a bumped policy version would silently overwrite
// the previous clip file in Drive. Format:
//
//	yt_<videoID>_<startSec>_<endSec>_<policyVersion>_<slug>.mp4
//
// The clipID (yt_<videoID>_<start>_<end>_<policyVer>) is the canonical
// primary key; the filename adds the slug so operators can locate
// clips by name in Drive.
func (s *SegmentsService) BuildClipFilename(videoID string, startSec, endSec int, name, policyVersion string) string {
	slug := textutil.SlugifyWithMax(name, 40)
	if slug == "" {
		slug = "clip"
	}
	if len(slug) > 0 && slug[0] >= '0' && slug[0] <= '9' {
		slug = "c_" + slug
	}
	if policyVersion == "" {
		policyVersion = ProcessSegmentPolicyVersion
	}
	return fmt.Sprintf("yt_%s_%d_%d_%s_%s.mp4", videoID, startSec, endSec, policyVersion, slug)
}

// SanitizeTimestamp validates a timestamp string format (SS, MM:SS, or HH:MM:SS).
func (s *SegmentsService) SanitizeTimestamp(ts string) error {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return fmt.Errorf("timestamp is required")
	}
	parts := strings.Split(ts, ":")
	if len(parts) > 3 {
		return fmt.Errorf("invalid timestamp format: %s", ts)
	}
	for _, p := range parts {
		for _, c := range p {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid timestamp: %s", ts)
			}
		}
	}
	return nil
}

// BuildClipMetadataInput removed (CLIPS-META A6+A7, July 2026).
// The type was dead code — zero production callers. The canonical
// ClipMetadataInput (dto/metadata_types.go) is the single builder
// input type for clip metadata enrichment. The lifecycle.FinalizeInput
// construction that BuildClipMetadata performed is now owned by
// process_segment.go::ProcessYouTubeSegmentUseCase directly.
