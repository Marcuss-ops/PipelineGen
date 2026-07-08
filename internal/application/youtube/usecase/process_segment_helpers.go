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

// buildClipAsset constructs the canonical youtubetypes.ClipAsset
// from the use case's per-segment state.
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
	md.Topics = cmd.Segment.Topics
	md.Speakers = cmd.Segment.Speakers
	md.MentionedPeople = cmd.Segment.MentionedPeople
	// Title: use the segment name if present, otherwise derive from Summary.
	if cmd.Segment.Name != "" {
		md.Title = cmd.Segment.Name
	} else if cmd.Segment.Summary != "" {
		md.Title = cmd.Segment.Summary
	}
	return youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       cmd.VideoID,
		LocalPath:     out.Item.LocalPath,
		FileHash:      fileHash,
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
	}
}

func deriveNormalizedGroup(cmd youtubetypes.ProcessSegmentCommand) string {
	if cmd.Destination != nil && strings.TrimSpace(cmd.Destination.Group) != "" {
		return strings.TrimSpace(cmd.Destination.Group)
	}
	return "general"
}

func (u *ProcessYouTubeSegmentUseCase) fail(out youtubetypes.ProcessSegmentResult, typed *ExtractionError) (youtubetypes.ProcessSegmentResult, error) {
	out.Item.Status = "failed"
	if typed != nil {
		out.Item.Error = typed.Error()
		out.Error = typed
	}
	return out, typed
}

func (u *ProcessYouTubeSegmentUseCase) failInvalidTimestamp(out youtubetypes.ProcessSegmentResult, which string, err error) (youtubetypes.ProcessSegmentResult, error) {
	typed := NewExtractionError(
		FailureCodeInvalidTimestamp,
		false,
		fmt.Sprintf("invalid %s timestamp: %v", which, err),
		err,
	)
	return u.fail(out, typed)
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
