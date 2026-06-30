// Package usecase — process_segment.go: canonical ProcessYouTubeSegmentUseCase.
//
// Commit C (PR-C-YouTube-Cutover, June 2026) lifts the legacy per-segment
// orchestration out of
// `internal/application/youtube/adapters/segment_processor.go::processSegment`
// into a typed use case that:
//   - declares all required ports as struct fields (Pattern 0, test-injectable);
//   - exposes a deterministic clip ID
//     `yt_<videoID>_<startSec>_<endSec>_<policyVersion>`
//     that supports re-processing under bumped policy versions;
//   - performs the canonical 9-step sequence in order;
//   - surfaces a typed Result that the extraction fan-out collects into
//     ExtractResponse for back-compat with the existing job handler.
//
// Until Commit H (DELETE legacy) the legacy adapters.processSegment is
// annotated `// TODO wave delete: legacy inline` and remains the production
// path for callers that have not yet wired the new ports. Once all callers
// move to the canonical use case, adapters.SegmentProcessor + adapters.Service
// are removed in Commit H.
package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ProcessSegmentPolicyVersion is stamped into the deterministic clip ID.
// Bump it when the metadata enrichment prompt, semantic keywords, embedding
// model, or segment policy change. The bump forces re-processing under the
// same clipID, so the YouTube discovery ledger (Commit D) re-emits the
// segment once the user opts into the new policy version.
const ProcessSegmentPolicyVersion = "v1"

// ProcessSegmentDeps bundles every port ProcessYouTubeSegmentUseCase touches.
// nil-port tolerance matches the rest of the youtube package — a nil port is
// logged + no-op'd, never panicked, so partial test fixtures still drive
// the full sequence behind the new wiring path.
type ProcessSegmentDeps struct {
	Cache          youtubeports.ClipCachePort
	VideoPipeline  youtubeports.VideoPipelinePort
	Subtitles      youtubeports.SubtitleFetcherPort
	Transcriber    youtubeports.WhisperTranscriberPort
	Hash           youtubeports.HashServicePort
	DriveFolderMgr youtubeports.DriveFolderManagerPort
	Writer         youtubeports.ClipAtomicWriter
	SegmentsSvc    *SegmentsService
	Log            *zap.Logger
}

// ProcessYouTubeSegmentUseCase is the canonical per-segment pipeline.
type ProcessYouTubeSegmentUseCase struct {
	deps ProcessSegmentDeps
}

// NewProcessYouTubeSegmentUseCase constructs the canonical use case.
//
// Commit 1/6 (PR-C-YouTube-Cutover, June 2026): fail-fast posture for
// required ports. Per the verdict's P0 #3 fail-closed directive, the
// use case MUST NOT silently accept nil ports — a nil Cache /
// VideoPipeline / Hash / Writer / SegmentsSvc means the canonical
// pipeline CANNOT complete and the use case will surface "processed"
// anyway (the pre-fix silent-success bug). Production composition
// wires every required port via the new wired-up adapter pair
// (ClipCacheAdapter + ClipAtomicWriterAdapter); the ctor panic here
// surfaces a wiring gap at boot rather than at first segment.
//
// Nil-tolerance is preserved for the optional DriveFolderMgr (per the
// verdict's \"Drive solo quando destination policy lo richiede\"
// directive): the use case runtime-checks DriveFolderMgr == nil and
// short-circuits the upload step, returning a non-canonical
// \"skipped-no-drive\" outcome rather than panicking on nil.
//
// SegmentsSvc is constructed via NewSegmentsService() when nil is
// supplied — the canonical helper is dependency-free, so this default
// is safe across all environments including partial-deploy tests.
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

// Execute runs the canonical 9-step pipeline for one segment:
//
//  1. Deterministic clip ID + timestamp validation.
//  2. CheckExistingClip — early-return when cached (idempotent re-runs).
//  3. Coordinates + retry-download via VideoPipeline port (3 attempts).
//  4. MD5File after cut.
//  5. SliceSubtitles (cache hit per Commit G) → Whisper fallback.
//  6. BuildClipMetadata via SegmentsSvc internal helper.
//  7. ProcessLifecycle (kept on ExtractionCallbacks path — Commit H
//     folds it into LifecycleServicePort if composition needs it).
//  8. DestinationResolver → DriveUploadFileIfChanged.
//  9. ClipAtomicWriter (DB write + outbox row in same tx; Commit F
//     implements the concrete adapter).
//
// Status semantics:
//   - "skipped" → Item has Drive* filled but no video pipeline or writer
//     side-effects. Idempotent re-run on already-processed clip.
//   - "processed" → full 9-step sequence completed without error.
//   - "failed" → any step returned an error; out.Error carries the cause.
//
// The use case does NOT set resp.OK or resp.Stats — that is the
// job handler's classifier (jobs/classify.go) responsibility.
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

	// Step 1 — deterministic clip ID + timestamp validation.
	startSec, err := textutil.ParseTimestamp(out.Item.Start)
	if err != nil {
		out.Item.Error = fmt.Sprintf("invalid start timestamp: %v", err)
		out.Error = err
		return out, fmt.Errorf("invalid start: %w", err)
	}
	endSec, err := textutil.ParseTimestamp(out.Item.End)
	if err != nil {
		out.Item.Error = fmt.Sprintf("invalid end timestamp: %v", err)
		out.Error = err
		return out, fmt.Errorf("invalid end: %w", err)
	}
	if startSec >= endSec {
		msg := fmt.Sprintf(
			"start time (%s) must be before end time (%s)",
			cmd.Segment.Start, cmd.Segment.End,
		)
		out.Item.Error = msg
		out.Error = fmt.Errorf("%s", msg)
		return out, fmt.Errorf("%s", msg)
	}
	duration := endSec - startSec
	policyVer := cmd.PolicyVersion
	if policyVer == "" {
		policyVer = ProcessSegmentPolicyVersion
	}
	clipID := fmt.Sprintf("yt_%s_%d_%d_%s", cmd.VideoID, startSec, endSec, policyVer)
	out.ID = clipID
	out.Item.ID = clipID
	out.Item.StartSeconds = startSec
	out.Item.EndSeconds = endSec
	out.Item.Duration = duration
	out.Item.Filename = u.deps.SegmentsSvc.BuildClipFilename(
		cmd.VideoID, startSec, endSec, out.Item.Name,
	)

	// Step 2 — cache hit short-circuit.
	if u.deps.Cache != nil {
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
		Strategy:       cmd.Strategy,
		OutputDir:      cmd.OutDir,
	}

	// Step 4 — retry download with exponential backoff.
	if u.deps.VideoPipeline == nil {
		err := fmt.Errorf("video pipeline port not wired")
		out.Item.Error = err.Error()
		out.Error = err
		return out, err
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
		IsRetryable:    isTransientExtractionError,
	})
	if err != nil {
		out.Item.Error = fmt.Sprintf("video processing failed: %v", err)
		out.Error = err
		return out, err
	}

	// Step 5 — MD5File after cut.
	localPath := ""
	if dlResult != nil {
		localPath = dlResult.LocalPath
	}
	out.Item.LocalPath = localPath
	if localPath != "" {
		out.Item.Filename = filepath.Base(localPath)
	}
	var fileHash string
	if u.deps.Hash != nil && localPath != "" {
		fileHash, _ = u.deps.Hash.MD5File(localPath)
	}
	out.Item.FileHash = fileHash
	out.FileHash = fileHash

	// Step 6 — SliceSubtitles (cache hit per Commit G).
	if u.deps.Subtitles != nil {
		if subErr := u.deps.Subtitles.SliceSubtitles(
			ctx, cmd.VideoID, startSec, endSec, localPath,
		); subErr != nil {
			u.deps.Log.Warn("subtitle slice failed, falling back to Whisper",
				zap.String("clip_id", clipID), zap.Error(subErr))
			// Step 7 — Whisper fallback when subtitles empty.
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

	// Step 8 — DriveUploadFileIfChanged. Drive upload failure flips
	// Item.Status to "failed" so the classifier (jobs/classify.go) sees
	// it; the audit roadmap explicitly calls this out (P0 #2: "the item
	// restituito normalmente NON ha DriveFileID/DriveLink/non ha
	// persistenza DB garantita"). A retry-on-503 retryable substring
	// (timeout/429/503 etc) surfaces to the parent classifier as
	// retryable; otherwise terminal.
	if u.deps.DriveFolderMgr != nil && cmd.DriveFolderID != "" && localPath != "" {
		if upRes, _, upErr := u.deps.DriveFolderMgr.UploadFileIfChanged(
			ctx, localPath, cmd.DriveFolderID, out.Item.Filename,
		); upErr == nil && upRes != nil {
			out.Item.DriveFileID = upRes.FileID
			out.Item.DriveLink = upRes.WebViewLink
		} else if upErr != nil {
			if isTransientExtractionError(upErr) {
				u.deps.Log.Warn("drive upload transient failure (will be classified retryable by parent)",
					zap.String("clip_id", clipID), zap.Error(upErr))
			} else {
				u.deps.Log.Error("drive upload terminal failure (will be classified terminal by parent)",
					zap.String("clip_id", clipID), zap.Error(upErr))
			}
			out.Item.Status = "failed"
			out.Item.Error = fmt.Sprintf("drive upload failed: %v", upErr)
			out.Error = fmt.Errorf("drive upload failed: %w", upErr)
			return out, fmt.Errorf("drive upload failed: %w", upErr)
		}
	}
	out.DriveFileID = out.Item.DriveFileID
	out.DriveLink = out.Item.DriveLink

	// Step 9 — ClipAtomicWriter (DB write + outbox row in same tx).
	if u.deps.Writer != nil {
		outboxPayload, _ := json.Marshal(map[string]any{
			"clip_id":       clipID,
			"video_id":      cmd.VideoID,
			"drive_link":    out.Item.DriveLink,
			"drive_file_id": out.Item.DriveFileID,
			"file_hash":     fileHash,
		})
		event := youtubeports.IndexEventPayload{
			Type:        "asset.index.requested",
			AggregateID: clipID,
			Payload:     outboxPayload,
			CreatedAt:   time.Now().UTC(),
		}
		if wErr := u.deps.Writer.CommitClipAndIndexEvent(
			ctx, clipID, out.Item, event,
		); wErr != nil {
			out.Item.Error = fmt.Sprintf("writer failed: %v", wErr)
			out.Error = fmt.Errorf("writer: %w", wErr)
			return out, fmt.Errorf("writer: %w", wErr)
		}
		out.IndexedRequestID = event.AggregateID
	}

	out.Item.Status = "processed"
	out.Status = "processed"
	return out, nil
}

// isTransientExtractionError mirrors the retryable-error taxonomy
// `internal/application/youtube/dto.IsTransientDownloadError` uses for
// retry.Do predicates. It accepts substring matches against the canonical
// list (timeout, 429/5xx, network errors, drive transient errors).
func isTransientExtractionError(err error) bool {
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

// cleanSegmentName trims + SafeName + falls back to segment_NNN on empty.
// Mirrors the helper in adapters/segment_processor.go (kept here so the
// canonical use case is self-contained; the adapters copy is removed in
// Commit H).
func cleanSegmentName(name string, idx int) string {
	name = strings.TrimSpace(name)
	name = textutil.SafeName(name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("segment_%03d", idx+1)
	}
	return name
}
