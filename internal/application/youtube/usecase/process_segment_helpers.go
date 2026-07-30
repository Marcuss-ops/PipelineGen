package usecase

import (
	"fmt"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Helpers for ProcessYouTubeSegmentUseCase ─────────────────────────
//
// Extracted from process_segment.go per AGENTS.md Pattern 5
// (PR-SEGMENT-SPLIT, July 2026).
//
// Phase 2 honor (PR-SPLIT-PROCESS-SEGMENT, July 2026): `fail` and
// `failInvalidTimestamp` now take `*out` (pointer receiver) and return
// the typed `*ExtractionError` directly. The pre-split signature was
// `(out, typed) (out, err)` (value-snapshot) which was awkward for the
// step-method pattern in files step1..step10 that all mutate `&out`
// passed by the orchestrator at process_segment.go::Execute. The
// pointer-receiver form is byte-equivalent behaviorally: the SAME
// fields are set on `out` (Item.Status="failed" + Item.Error + Error)
// — the caller just propagates the typed error directly instead of
// unpacking a 2-tuple. Unexported helpers, no external API change
// (godlike/07 minimum-blast-radius holds).
func (u *ProcessYouTubeSegmentUseCase) fail(out *youtubetypes.ProcessSegmentResult, typed *ExtractionError) *ExtractionError {
	out.Item.Status = "failed"
	if typed != nil {
		out.Item.Error = typed.Error()
		out.Error = typed
	}
	return typed
}

func (u *ProcessYouTubeSegmentUseCase) failInvalidTimestamp(out *youtubetypes.ProcessSegmentResult, which string, err error) *ExtractionError {
	typed := NewExtractionError(
		FailureCodeInvalidTimestamp,
		false,
		fmt.Sprintf("invalid %s timestamp: %v", which, err),
		err,
	)
	return u.fail(out, typed)
}

// buildClipAsset constructs the canonical youtubetypes.ClipAsset
// from the use case's per-segment state.
//
// godlike/06 SSOT (one canonical owner per fact): buildClipAsset lives
// ONLY in process_segment_helpers.go. Step 9 (process_segment_step6to9.go)
// calls it; no other caller may re-derive the canonical shape.
func buildClipAsset(
	clipID string,
	cmd youtubetypes.ProcessSegmentCommand,
	out youtubetypes.ProcessSegmentResult,
	fileHash string,
	policyVersion string,
) youtubetypes.ClipAsset {
	md := youtubetypes.CanonicalClipMetadata{
		SourceURL:       cmd.VideoURL,
		TranscriptPath:  "",
		NormalizedGroup: deriveNormalizedGroup(cmd),
		SourceProvider:  "youtube",
		VideoID:         cmd.VideoID,
		ClipStartSec:    out.Item.StartSeconds,
		ClipEndSec:      out.Item.EndSeconds,
		ClipDurationSec: out.Item.Duration,
		PolicyVersion:   policyVersion,
		DrivePath:       out.Item.DriveLink,
		ContentHash:     fileHash,
	}
	md.Summary = cmd.Segment.Summary
	md.Category = cmd.Segment.Category
	md.SourceTitle = cmd.Segment.SourceTitle
	md.SourceChannel = cmd.Segment.SourceChannel
	md.Topics = cmd.Segment.Topics
	md.Speakers = cmd.Segment.Speakers
	md.MentionedPeople = cmd.Segment.MentionedPeople
	// Title: use the segment name if present, otherwise derive from Summary.
	if cmd.Segment.Name != "" {
		md.Title = cmd.Segment.Name
	} else if cmd.Segment.SourceTitle != "" {
		md.Title = cmd.Segment.SourceTitle
	} else if cmd.Segment.Summary != "" {
		md.Title = cmd.Segment.Summary
	}
	return youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       cmd.VideoID,
		LocalPath:     out.Item.LocalPath,
		FileHash:      fileHash,
		SearchText:    composeYouTubeClipSearchText(md, cmd.Segment.Hook),
		PolicyVersion: policyVersion,
		Drive: youtubetypes.ClipAssetDrive{
			FolderID:    cmd.DriveFolderID,
			FolderPath:  cmd.DriveFolderPath,
			FileID:      out.Item.DriveFileID,
			WebViewLink: out.Item.DriveLink,
		},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: out.Item.StartSeconds,
			EndSec:   out.Item.EndSeconds,
			Duration: out.Item.Duration,
		},
		Metadata: md,
		Texts:    cmd.Segment.Texts,
	}
}

// deriveNormalizedGroup returns the caller-supplied Group or an empty string.
//
// Prior to PR-YT-PATH-FALLBACK (July 2026), this function returned "general"
// as a hard-coded fallback. That shadowed the delivery layer's YouTubeClipPath
// fallback chain (Group → Category → "youtube_uncategorized") because the
// publisher always received req.Group="general" and never reached the
// "youtube_uncategorized" technical fallback.
//
// Returning "" delegates the fallback decision to the canonical SOLE owner
// (delivery.YouTubeClipPath) per godlike/06 SSOT one-canonical-owner-per-fact.
func deriveNormalizedGroup(cmd youtubetypes.ProcessSegmentCommand) string {
	if cmd.Destination != nil && strings.TrimSpace(cmd.Destination.Group) != "" {
		return strings.TrimSpace(cmd.Destination.Group)
	}
	return ""
}

// composeYouTubeClipSearchText builds the canonical search_text for a
// YouTube clip from the metadata available at Step 9 write time. The
// order of fields mirrors the DoD 10 priority:
//
//	title → summary → hook → topics → source_url → speakers → mentioned_people
//
// Fields with empty/zero values are silently skipped (godlike/07
// minimum-blast-radius: no dangling " ," or "Tags: " fragments).
//
// Transcript and channel are intentionally deferred to the
// youtube.rebuild_search_text job (Step 10 / post-enrichment) — they
// are NOT available at Step 9 write time.
//
// godlike/06 SSOT: this function is the SOLE canonical owner of
// the YouTube-clip search_text format at Step 9; other callers
// MUST NOT re-derive the same composition.
func composeYouTubeClipSearchText(md youtubetypes.CanonicalClipMetadata, hook string) string {
	parts := make([]string, 0, 8)

	// Title
	if v := strings.TrimSpace(md.Title); v != "" {
		parts = append(parts, v)
	}
	// Summary
	if v := strings.TrimSpace(md.Summary); v != "" {
		parts = append(parts, v)
	}
	// Hook
	if v := strings.TrimSpace(hook); v != "" {
		parts = append(parts, v)
	}
	// Topics (space-joined, each trimmed)
	if len(md.Topics) > 0 {
		kept := make([]string, 0, len(md.Topics))
		for _, t := range md.Topics {
			if t = strings.TrimSpace(t); t != "" {
				kept = append(kept, t)
			}
		}
		if len(kept) > 0 {
			parts = append(parts, strings.Join(kept, " "))
		}
	}
	// Source URL
	if v := strings.TrimSpace(md.SourceURL); v != "" {
		parts = append(parts, v)
	}
	// Speakers
	if len(md.Speakers) > 0 {
		kept := make([]string, 0, len(md.Speakers))
		for _, s := range md.Speakers {
			if s = strings.TrimSpace(s); s != "" {
				kept = append(kept, s)
			}
		}
		if len(kept) > 0 {
			parts = append(parts, strings.Join(kept, " "))
		}
	}
	// Mentioned people
	if len(md.MentionedPeople) > 0 {
		kept := make([]string, 0, len(md.MentionedPeople))
		for _, p := range md.MentionedPeople {
			if p = strings.TrimSpace(p); p != "" {
				kept = append(kept, p)
			}
		}
		if len(kept) > 0 {
			parts = append(parts, strings.Join(kept, " "))
		}
	}

	return strings.Join(parts, " ")
}

func cleanSegmentName(name string, idx int) string {
	name = strings.TrimSpace(name)
	name = textutil.SafeName(name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("segment_%03d", idx+1)
	}
	return name
}

func validateFFProbeReport(
	report *youtubeports.FFProbeReport,
	localPath string,
	expectedDurationSec int,
	keepAudio bool,
) *ExtractionError {
	if report == nil {
		return NewExtractionError(FailureCodeFFProbeValidationFailed, false,
			fmt.Sprintf("ffprobe: nil report for %q", localPath), nil)
	}
	if !report.ContainerReadable {
		return NewExtractionError(FailureCodeFFProbeValidationFailed, false,
			fmt.Sprintf("ffprobe: container not readable for %q (likely truncated or .part file)", localPath), nil)
	}
	if !report.VideoStreamPresent {
		return NewExtractionError(FailureCodeFFProbeValidationFailed, false,
			fmt.Sprintf("ffprobe: no video stream in %q", localPath), nil)
	}
	expected := float64(expectedDurationSec)
	diff := report.DurationSeconds - expected
	if diff < 0 {
		diff = -diff
	}
	tolerance := expected * 0.05
	if tolerance < 1.0 {
		tolerance = 1.0
	}
	if diff > tolerance {
		return NewExtractionError(FailureCodeFFProbeValidationFailed, false,
			fmt.Sprintf("ffprobe: duration mismatch for %q: expected %.1fs, got %.1fs (diff=%.1fs > tolerance=%.1fs)",
				localPath, expected, report.DurationSeconds, diff, tolerance), nil)
	}
	if report.Width <= 0 || report.Height <= 0 {
		return NewExtractionError(FailureCodeFFProbeValidationFailed, false,
			fmt.Sprintf("ffprobe: invalid dimensions %dx%d for %q", report.Width, report.Height, localPath), nil)
	}
	if report.FPS <= 0 {
		return NewExtractionError(FailureCodeFFProbeValidationFailed, false,
			fmt.Sprintf("ffprobe: invalid FPS %.1f for %q", report.FPS, localPath), nil)
	}
	if keepAudio && !report.AudioPresent {
		return NewExtractionError(FailureCodeFFProbeValidationFailed, false,
			fmt.Sprintf("ffprobe: audio stream missing in %q but KeepAudio=true", localPath), nil)
	}
	return nil
}
