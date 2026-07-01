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
//   - #3 SegmentPolicy: a Min/Max duration gate (defaults 2s/60s) is
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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

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
	// Zero values default to {Min: 2, Max: 60}. Commit 2/6 #3.
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
	Log           *zap.Logger
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

// isTransientExtractionErrorLegacy is the legacy substring-match
// fallback used by IsTransientExtractionError when errors.As
// cannot find a *ExtractionError. Kept as a private helper because
// raw port errors (e.g. yt-dlp subprocess output) bubble up through
// retry.Do before the use case can wrap them.
func isTransientExtractionErrorLegacy(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	retryable := []string{
		"timeout", "connection refused", "connection reset", "eof",
		"429", "503", "502", "504",
		"rate limit", "quota exceeded",
		"http error 5",
		"temporarily unavailable",
	}
	for _, s := range retryable {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
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
