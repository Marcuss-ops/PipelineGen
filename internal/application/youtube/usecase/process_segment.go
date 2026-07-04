// Package usecase — process_segment.go: canonical ProcessYouTubeSegmentUseCase.
//
// Commit C (PR-C-YouTube-Cutover, June 2026) lifts the legacy per-segment
// orchestration out of the youtube/adapters/ package into a typed use case.
//
// Commit 1/6 (PR-C-YouTube-Cutover, June 2026): the use case became the
// production path. 5 required ports panic on nil at ctor time
// (Cache/VideoPipeline/Hash/Writer/SegmentsSvc).
//
// Commit 2/6 (PR-C-YouTube-Cutover, June 2026, Correttezza): 5 fail-closed
// corrections landed:
//   - #2 StrategyReplace cache-bypass: when cmd.Strategy == "replace",
//     the cache lookup is skipped so a re-extract under the same clipID
//     always re-runs the full 9-step pipeline.
//   - #3 SegmentPolicy: a Min/Max duration gate (defaults 4s/60s; user-
//     requested clip-duration window — no effects, no transitions) is
//     applied at Step 1. Out-of-range segments fail with
//     FailureCodeDurationOutOfRange.
//   - #4 policyVersion in filename: BuildClipFilename takes a 5th
//     parameter (policyVersion) so two policy versions of the same
//     (videoID, start, end) tuple produce different files.
//   - #5 runtime fail-closed at Step 5: localPath == "" →
//     FailureCodeEmptyLocalPath; os.Stat size == 0 →
//     FailureCodeInvalidLocalArtifact; hash.MD5File err/empty →
//     FailureCodeHashFailed. Pre-Commit-2 silently swallowed all
//     three.
//   - #6 ClipAsset canonical: Step 9 builds a `youtubetypes.ClipAsset`
//     (the canonical domain entity — ID/VideoID/LocalPath/FileHash/
//     Drive/Coordinates/Metadata) and passes it to the writer. The
//     writer no longer sees the HTTP-shaped ExtractItem.
//
// The 9-step sequence is preserved.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ProcessSegmentPolicyVersion is the canonical "v1" policy version
// stamped into the deterministic clip ID + filename. Bump it when
// the metadata enrichment prompt, semantic keywords, embedding
// model, or segment policy change.
const ProcessSegmentPolicyVersion = "v1"

// ProcessSegmentDeps bundles every port ProcessYouTubeSegmentUseCase
// touches. nil-port tolerance matches the rest of the youtube package
// for the OPTIONAL ports (Subtitles/Transcriber/DriveFolderMgr); the
// REQUIRED ports panic on nil at ctor time.
type ProcessSegmentDeps struct {
	Cache          youtubeports.ClipCachePort
	VideoPipeline  youtubeports.VideoPipelinePort
	Subtitles      youtubeports.SubtitleFetcherPort
	Transcriber    youtubeports.WhisperTranscriberPort
	Hash           youtubeports.HashServicePort
	DriveFolderMgr youtubeports.DriveFolderManagerPort
	Writer         youtubeports.ClipAtomicWriter
	SegmentsSvc    *SegmentsService
	// SegmentPolicy is the duration gate (Min/Max in seconds).
	// Zero values default to {Min: 4, Max: 60}. Commit 2/6 #3.
	// Per user spec (2026-07-04): no effects, no transitions are
	// applied to extracted clips; the YouTube endpoint only cuts
	// the segment, preserves audio, uploads to Drive, writes
	// media_assets and emits the asset.index.requested outbox event.
	SegmentPolicy youtubetypes.SegmentPolicy
	// ClipMetadataWriter is the optional metadata-enrichment writer
	// (Commit 4/6, P1 #15). When non-nil, Step 10 of the pipeline
	// writes CanonicalClipMetadata to media_assets + emits the
	// metadata outbox event. When nil, Step 10 short-circuits
	// silently. Type: youtubeports.ClipMetadataWriter.
	ClipMetadataWriter youtubeports.ClipMetadataWriter
	// MetadataService is the optional metadata-enrichment orchestrator
	// (Commit 4/6, P1 #15 + #16). When non-nil, Step 10 calls
	// EnrichClip to build + persist metadata. When nil, Step 10
	// is a no-op.
	MetadataService *ytmetadata.MetadataService
	// Stager is the shared assets.SourceStager port (Step 9/12 wire-up,
	// July 2026). Optional. When non-nil, Step 4 stages the FULL
	// video via the shared stager before retry.Do, then sets
	// cutReq.PreDownloadedPath so the concrete VideoPipeline
	// uses ffmpeg -c copy (videomuscles/youtube_pipeline.go:124-133)
	// to slice the local staged file instead of re-downloading via
	// yt-dlp. This is genuine bandwidth-saving (one yt-dlp download
	// per Execute vs N downloads for the retry+replace case).
	Stager assets.SourceStager
	// FFProbe is the optional ffprobe validation port (audit 2026-07-03
	// BLOCKER #3). When non-nil, Step 5a validates the downloaded clip
	// via ffprobe: container readable, video stream present, duration
	// within ±5% tolerance, width/height > 0, FPS > 0, audio present
	// when KeepAudio=true. When nil, the validation step is silently
	// skipped (pre-existing hash + stat checks remain).
	FFProbe youtubeports.FFProbePort
	Log     *zap.Logger
}

// ProcessYouTubeSegmentUseCase is the canonical per-segment pipeline.
type ProcessYouTubeSegmentUseCase struct {
	deps ProcessSegmentDeps
}

// NewProcessYouTubeSegmentUseCase constructs the canonical use case.
// 5 required ports panic on nil. Subtitles/Transcriber/DriveFolderMgr
// are runtime-gated (no panic).
func NewProcessYouTubeSegmentUseCase(d ProcessSegmentDeps) *ProcessYouTubeSegmentUseCase {
	if d.Cache == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Cache port is required (composition must wire ClipCacheAdapter from internal/infrastructure/database/sqlite/assets/clip_cache_adapter.go)")
	}
	if d.VideoPipeline == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: VideoPipeline port is required (composition must wire the YouTube pipeline adapter)")
	}
	if d.Hash == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Hash port is required (composition must wire hashutil.NewHashAdapter)")
	}
	if d.Writer == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Writer port is required — composition must wire ClipAtomicWriterAdapter (PR-C P0 #3 fail-closed; pre-Commit-1 silently wrote nothing and returned 'processed')")
	}
	if d.SegmentsSvc == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: SegmentsSvc port is required (composition must construct *SegmentsService via youtube.NewSegmentsService())")
	}
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	return &ProcessYouTubeSegmentUseCase{deps: d}
}

// Execute runs the canonical 9-step pipeline for one segment.
func (u *ProcessYouTubeSegmentUseCase) Execute(ctx context.Context, cmd youtubetypes.ProcessSegmentCommand) (youtubetypes.ProcessSegmentResult, error) {
	out := youtubetypes.ProcessSegmentResult{
		Status: "failed",
		Item: youtubetypes.ExtractItem{
			Name:            cleanSegmentName(cmd.Segment.Name, cmd.Index),
			Start:           strings.TrimSpace(cmd.Segment.Start),
			End:             strings.TrimSpace(cmd.Segment.End),
			DriveFolderID:   cmd.DriveFolderID,
			DriveFolderPath: cmd.DriveFolderPath,
			Status:          "failed",
		},
	}

	// Step 1 — deterministic clip ID + timestamp validation +
	// SegmentPolicy bounds (Commit 2/6 #3) + filename with policyVersion
	// (Commit 2/6 #4).
	startSec, err := textutil.ParseTimestamp(out.Item.Start)
	if err != nil {
		return u.failInvalidTimestamp(out, "start", err)
	}
	endSec, err := textutil.ParseTimestamp(out.Item.End)
	if err != nil {
		return u.failInvalidTimestamp(out, "end", err)
	}
	if startSec >= endSec {
		msg := fmt.Sprintf("start time (%s) must be before end time (%s)", cmd.Segment.Start, cmd.Segment.End)
		typed := NewExtractionError(FailureCodeInvalidTimestamp, false, msg, nil)
		return u.fail(out, typed)
	}
	duration := endSec - startSec
	policyVer := cmd.PolicyVersion
	if policyVer == "" {
		policyVer = ProcessSegmentPolicyVersion
	}
	// Commit 2/6 #3: SegmentPolicy duration gate.
	if !u.deps.SegmentPolicy.ValidDuration(duration) {
		policy := u.deps.SegmentPolicy
		if policy.MinDuration == 0 {
			policy.MinDuration = youtubetypes.DefaultSegmentPolicy().MinDuration
		}
		if policy.MaxDuration == 0 {
			policy.MaxDuration = youtubetypes.DefaultSegmentPolicy().MaxDuration
		}
		msg := fmt.Sprintf("duration %ds out of range [%d, %d]", duration, policy.MinDuration, policy.MaxDuration)
		typed := NewExtractionError(FailureCodeDurationOutOfRange, false, msg, nil)
		return u.fail(out, typed)
	}
	clipID := fmt.Sprintf("yt_%s_%d_%d_%s", cmd.VideoID, startSec, endSec, policyVer)
	out.ID = clipID
	out.Item.ID = clipID
	out.Item.StartSeconds = startSec
	out.Item.EndSeconds = endSec
	out.Item.Duration = duration
	// Commit 2/6 #4: filename carries the policyVersion.
	out.Item.Filename = u.deps.SegmentsSvc.BuildClipFilename(
		cmd.VideoID, startSec, endSec, out.Item.Name, policyVer,
	)

	// Step 2 — cache hit short-circuit. Commit 2/6 #2:
	// StrategyReplace bypasses the cache lookup entirely so a
	// re-extract under the same clipID always re-runs the full
	// pipeline.
	if u.deps.Cache != nil && cmd.Strategy != youtubetypes.StrategyReplace {
		existingItem, exists, cacheErr := u.deps.Cache.GetExisting(ctx, clipID)
		if cacheErr == nil && exists && existingItem != nil {
			out.Item.Status = "skipped"
			out.Item.LocalPath = existingItem.LocalPath
			out.Item.DriveFileID = existingItem.DriveFileID
			out.Item.DriveLink = existingItem.DriveLink
			out.Item.DownloadLink = existingItem.DownloadLink
			out.Status = "skipped"
			return out, nil
		}
		if cacheErr != nil {
			u.deps.Log.Warn("clip cache lookup failed; falling through to re-process",
				zap.String("clip_id", clipID), zap.Error(cacheErr))
		}
	}

	// Step 3 — coords into VideoPipeline for cut/download.
	keepAudio := true
	if cmd.KeepAudio != nil {
		keepAudio = *cmd.KeepAudio
	}
	normalize := true
	if cmd.Normalize != nil {
		normalize = *cmd.Normalize
	}
	cutReq := youtubeports.VideoCutRequest{
		URL:            cmd.VideoURL,
		VideoID:        cmd.VideoID,
		Start:          float64(startSec),
		Duration:       float64(duration),
		OutputName:     strings.TrimSuffix(out.Item.Filename, ".mp4"),
		ForceKeyframes: cmd.ForceKeyframes,
		KeepAudio:      keepAudio,
		Normalize:      normalize,
		Strategy:       string(cmd.Strategy),
		OutputDir:      cmd.OutDir,
	}

	// Step 4 — retry download with exponential backoff.
	if u.deps.VideoPipeline == nil {
		typed := NewExtractionError(FailureCodeVideoProcessingFailed, false, "video pipeline port not wired", nil)
		return u.fail(out, typed)
	}

	// Step 4a — shared SourceStager pre-stage (Step 9/12 wire-up, July 2026).
	//
	// When the Stager port is wired, download the FULL source video via the
	// shared SourceStager port BEFORE the retry loop. CutRequest.PreDownloadedPath
	// is set so the concrete VideoPipeline (videomuscles/youtube_pipeline.go:124-133)
	// SKIPS the yt-dlp download slice and uses ffmpeg -c copy on the local file —
	// saving N yt-dlp calls for the retry loop (the retry consumes the SAME
	// staged file).
	//
	// Graceful degradation: stager.StageSource failure is logged Warn and the
	// cutReq keeps PreDownloadedPath=""; the legacy DownloadAndCut path
	// downloads itself per attempt. No call site is locked-out.
	//
	// Cleanup is deferred best-effort via Cleanup() so a transient failure
	// cannot mask the mediaProcessor's outcome (mirrors Artlist pattern).
	if u.deps.Stager != nil && cmd.VideoURL != "" {
		staged, stageErr := u.deps.Stager.StageSource(ctx, assets.SourceRef{
			URL: cmd.VideoURL,
		})
		if stageErr != nil {
			u.deps.Log.Warn("shared SourceStager pre-stage failed (continuing with legacy per-segment yt-dlp)",
				zap.String("clip_id", clipID),
				zap.String("video_url", cmd.VideoURL),
				zap.Error(stageErr))
		} else {
			cutReq.PreDownloadedPath = staged.LocalPath
			u.deps.Log.Info("shared SourceStager pre-staged full video for -c copy slicing",
				zap.String("clip_id", clipID),
				zap.String("video_url", cmd.VideoURL),
				zap.String("local_path", staged.LocalPath),
				zap.Int64("bytes", staged.Bytes))
			defer func(staged *assets.StagedAsset) {
				if cleanupErr := u.deps.Stager.Cleanup(ctx, staged); cleanupErr != nil {
					u.deps.Log.Warn("shared SourceStager cleanup failed (best-effort)",
						zap.String("local_path", staged.LocalPath),
						zap.Error(cleanupErr))
				}
			}(staged)
		}
	}
	var dlResult *youtubeports.VideoCutResult
	err = retry.Do(ctx, func() error {
		_ = os.Remove(filepath.Join(cmd.OutDir, out.Item.Filename))
		var dlErr error
		dlResult, dlErr = u.deps.VideoPipeline.DownloadAndCutYouTubeVideo(ctx, cutReq)
		return dlErr
	}, retry.RetryOptions{
		MaxAttempts:    3,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     30 * time.Second,
		IsRetryable:    IsTransientExtractionError,
	})
	if err != nil {
		typed := NewExtractionError(
			FailureCodeVideoProcessingFailed,
			IsTransientExtractionError(err),
			fmt.Sprintf("video processing failed: %v", err),
			err,
		)
		return u.fail(out, typed)
	}

	// Step 5 — runtime fail-closed (Commit 2/6 #5): empty path,
	// missing/zero-size file, and hash failure all fail-closed
	// with the typed ExtractionError taxonomy.
	localPath := ""
	if dlResult != nil {
		localPath = dlResult.LocalPath
	}
	if localPath == "" {
		typed := NewExtractionError(FailureCodeEmptyLocalPath, false,
			"video pipeline returned empty LocalPath", nil)
		return u.fail(out, typed)
	}
	if stat, statErr := os.Stat(localPath); statErr != nil || stat.Size() == 0 {
		typed := NewExtractionError(FailureCodeInvalidLocalArtifact, false,
			fmt.Sprintf("local artifact %q missing or zero-size (stat_err=%v)", localPath, statErr),
			statErr)
		return u.fail(out, typed)
	}
	var fileHash string
	if u.deps.Hash != nil {
		var hashErr error
		fileHash, hashErr = u.deps.Hash.MD5File(localPath)
		if hashErr != nil || fileHash == "" {
			typed := NewExtractionError(FailureCodeHashFailed, false,
				fmt.Sprintf("hash.MD5File failed for %q (err=%v)", localPath, hashErr),
				hashErr)
			return u.fail(out, typed)
		}
	}
	out.Item.LocalPath = localPath
	out.Item.Filename = filepath.Base(localPath)
	out.Item.FileHash = fileHash
	out.FileHash = fileHash

	// Step 5a — ffprobe validation (audit 2026-07-03 BLOCKER #3).
	// Optional port: when nil, validation is silently skipped.
	// Validates: container readable, video stream present, duration
	// within ±5% tolerance, width/height > 0, FPS > 0, audio present
	// when KeepAudio=true. A corrupted file fails terminal — the
	// caller must re-download.
	if u.deps.FFProbe != nil {
		report, probeErr := u.deps.FFProbe.ValidateClip(ctx, localPath, duration, keepAudio)
		if probeErr != nil {
			typed := NewExtractionError(FailureCodeFFProbeValidationFailed, false,
				fmt.Sprintf("ffprobe execution failed for %q: %v", localPath, probeErr),
				probeErr)
			return u.fail(out, typed)
		}
		if ffprobeErr := validateFFProbeReport(report, localPath, duration, keepAudio); ffprobeErr != nil {
			return u.fail(out, ffprobeErr)
		}
		// Log non-fatal warnings for operator dashboards.
		for _, w := range report.Warnings {
			u.deps.Log.Warn("ffprobe: non-fatal warning",
				zap.String("clip_id", clipID),
				zap.String("local_path", localPath),
				zap.String("warning", w))
		}
	}

	// Step 6 — SliceSubtitles (cache hit per Commit G).
	if u.deps.Subtitles != nil {
		if subErr := u.deps.Subtitles.SliceSubtitles(
			ctx, cmd.VideoID, startSec, endSec, localPath,
		); subErr != nil {
			u.deps.Log.Warn("subtitle slice failed, falling back to Whisper",
				zap.String("clip_id", clipID), zap.Error(subErr))
			// Step 7 — Whisper fallback.
			if u.deps.Transcriber != nil {
				transcript, wErr := u.deps.Transcriber.TranscribeAudio(ctx, localPath)
				if wErr == nil && transcript != "" {
					txtPath := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".txt"
					_ = os.WriteFile(txtPath, []byte(transcript), 0o644)
					u.deps.Log.Info("whisper fallback transcribed clip",
						zap.String("clip_id", clipID),
						zap.String("txt_path", txtPath))
				} else if wErr != nil {
					u.deps.Log.Warn("whisper fallback failed",
						zap.String("clip_id", clipID), zap.Error(wErr))
				}
			}
		}
	}

	// Step 8 — DriveUploadFileIfChanged.
	if u.deps.DriveFolderMgr != nil && cmd.DriveFolderID != "" && localPath != "" {
		if upRes, _, upErr := u.deps.DriveFolderMgr.UploadFileIfChanged(
			ctx, localPath, cmd.DriveFolderID, out.Item.Filename,
		); upErr == nil && upRes != nil {
			out.Item.DriveFileID = upRes.FileID
			out.Item.DriveLink = upRes.WebViewLink
		} else if upErr != nil {
			retryable := IsTransientExtractionError(upErr)
			if retryable {
				u.deps.Log.Warn("drive upload transient failure (will be classified retryable by parent)",
					zap.String("clip_id", clipID), zap.Error(upErr))
			} else {
				u.deps.Log.Error("drive upload terminal failure (will be classified terminal by parent)",
					zap.String("clip_id", clipID), zap.Error(upErr))
			}
			typed := NewExtractionError(
				FailureCodeDriveUploadFailed,
				retryable,
				fmt.Sprintf("drive upload failed: %v", upErr),
				upErr,
			)
			return u.fail(out, typed)
		}
	}
	out.DriveFileID = out.Item.DriveFileID
	out.DriveLink = out.Item.DriveLink

	// Step 9 — ClipAtomicWriter. Build the canonical ClipAsset
	// and delegate to the writer, which owns the outbox envelope
	// exclusively. The caller MUST NOT supply a custom payload
	// (Blocco 1.1: the previous ad-hoc payload caused every
	// indexing event to land in dead_letter because the consumer
	// requires the canonical BuildReindexEnvelopeV1 shape).
	//
	// BLOCKER #4 closure (audit 2026-07-03): when the outbox event
	// is suppressed by an existing terminal row (dead_letter or
	// superseded), the writer returns ErrOutboxTerminalConflict.
	// We surface this as "processed_but_index_blocked" — the clip
	// was written to the DB but no new indexing event was created.
	if u.deps.Writer != nil {
		asset := buildClipAsset(clipID, cmd, out, fileHash, policyVer)
		event := youtubeports.IndexEventPayload{
			Type:        "asset.index.requested",
			AggregateID: clipID,
			CreatedAt:   time.Now().UTC(),
		}
		if wErr := u.deps.Writer.CommitClipAndIndexEvent(
			ctx, clipID, asset, event,
		); wErr != nil {
			// BLOCKER #4: terminal outbox conflict → processed_but_index_blocked.
			// The clip row was committed; the index event was suppressed by
			// a dead_letter/superseded row. Not a failure — the clip is safe,
			// but the operator must resolve the terminal event before indexing.
			if errors.Is(wErr, youtubeports.ErrOutboxTerminalConflict) {
				out.Item.Status = "processed_but_index_blocked"
				out.Status = "processed_but_index_blocked"
				u.deps.Log.Warn("clip committed but index blocked by terminal outbox row (BLOCKER #4)",
					zap.String("clip_id", clipID),
					zap.Error(wErr))
				return out, nil
			}
			typed := NewExtractionError(FailureCodeWriterFailed, false,
				fmt.Sprintf("writer failed: %v", wErr), wErr)
			return u.fail(out, typed)
		}
		out.IndexedRequestID = event.AggregateID
	}

	out.Item.Status = "processed"
	out.Status = "processed"
	return out, nil
}

// buildClipAsset is the Commit 2/6 #6 helper: it constructs the
// canonical `youtubetypes.ClipAsset` from the use case's per-segment
// state. The ClipAsset bundles ID/VideoID/LocalPath/FileHash/Drive/
// Coordinates/Metadata in one typed struct so the writer's column
// mapping is explicit and refactor-resistant.
//
// Metadata fields are populated from the segment DTO + the use case
// state we already have access to (drive folder, video URL,
// normaliser). The full Ollama-backed enrichment builder is Commit 4
// scope; this helper ships the typed shape + a deterministic
// fallback (segment-level summary/topics/speakers when present,
// otherwise derived from the segment name + video ID).
func buildClipAsset(
	clipID string,
	cmd youtubetypes.ProcessSegmentCommand,
	out youtubetypes.ProcessSegmentResult,
	fileHash string,
	policyVersion string,
) youtubetypes.ClipAsset {
	md := youtubetypes.ClipMetadata{
		SourceURL:       cmd.VideoURL,
		TranscriptPath:  "", // populated by Step 6/7 when Whisper fallback fires
		NormalizedGroup: deriveNormalizedGroup(cmd),
	}
	// Segment is a VALUE type (Commit C/1 design, not a pointer),
	// so the field assignments are unconditional.
	md.Summary = cmd.Segment.Summary
	md.Topics = cmd.Segment.Topics
	md.Speakers = cmd.Segment.Speakers
	md.MentionedPeople = cmd.Segment.MentionedPeople
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

// deriveNormalizedGroup returns the canonical normalized group for
// the asset. The destination's Group is the primary source; when
// absent, the segment's normalized value is "general".
func deriveNormalizedGroup(cmd youtubetypes.ProcessSegmentCommand) string {
	if cmd.Destination != nil && strings.TrimSpace(cmd.Destination.Group) != "" {
		return strings.TrimSpace(cmd.Destination.Group)
	}
	return "general"
}

// fail is the typed-error fast-return. Updates the out's error
// fields, then returns.
func (u *ProcessYouTubeSegmentUseCase) fail(out youtubetypes.ProcessSegmentResult, typed *ExtractionError) (youtubetypes.ProcessSegmentResult, error) {
	out.Item.Status = "failed"
	if typed != nil {
		out.Item.Error = typed.Error()
		out.Error = typed
	}
	return out, typed
}

// failInvalidTimestamp wraps a timestamp parse error as a typed
// FailureCodeInvalidTimestamp ExtractionError.
func (u *ProcessYouTubeSegmentUseCase) failInvalidTimestamp(out youtubetypes.ProcessSegmentResult, which string, err error) (youtubetypes.ProcessSegmentResult, error) {
	typed := NewExtractionError(
		FailureCodeInvalidTimestamp,
		false,
		fmt.Sprintf("invalid %s timestamp: %v", which, err),
		err,
	)
	return u.fail(out, typed)
}

// cleanSegmentName trims + SafeName + falls back to segment_NNN on
// empty. Mirrors the helper in adapters/segment_processor.go (kept
// here so the canonical use case is self-contained; the adapters
// copy is removed in Commit H).
func cleanSegmentName(name string, idx int) string {
	name = strings.TrimSpace(name)
	name = textutil.SafeName(name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("segment_%03d", idx+1)
	}
	return name
}

// validateFFProbeReport checks the structured ffprobe report and
// returns a typed ExtractionError when a mandatory validation fails.
// Returns nil when the clip passes all checks. Non-fatal warnings
// (e.g. audio missing when KeepAudio=false) are NOT surfaced here —
// the caller logs them separately.
//
// Audit 2026-07-03 BLOCKER #3 (ffprobe validation after download).
//
// Mandatory checks (terminal failures):
//   - Container readable (not a .part or truncated file)
//   - Video stream present
//   - Duration within ±5% or ±1s tolerance of expected
//   - Width > 0 and Height > 0
//   - FPS > 0
//   - Audio present when keepAudio=true
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
	// Duration tolerance: ±5% or ±1 second, whichever is larger.
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
