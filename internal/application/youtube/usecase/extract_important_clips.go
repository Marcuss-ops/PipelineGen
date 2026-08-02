// Package usecase - extract_important_clips.go: PR-GEMMA-EXTRACT-IMPORTANT (2026-07-08).
//
// ExtractImportantClipsUseCase is the canonical orchestrator for
// POST /api/clips/extract-important.
//  1. Fetches the YouTube timed transcript (TranscriptFetcher inline port).
//  2. Invokes an LLM Analyzer (AnalyzerPort, NIL-TOLERANT) to identify important segments.
//  3. For each LLM-discovered segment: download via yt-dlp SectionDownloader,
//     upload to a per-video Drive subfolder, write media_assets via the
//     canonical ClipAtomicWriter (single-tx media_assets INSERT +
//     outbox_events asset.index.requested INSERT).
//  4. Fail-closed on 0 segments (ErrNoSegments).
//
// Typed sentinels (godlike/07 typed-error contract):
//   - ErrSubtitleUnavailable - subtitle fetch error.
//   - ErrAnalyzerUnavailable - analyzer nil or analyzer error.
//   - ErrNoSegments          - LLM returned 0 segments.
//   - ErrClipPublishFailed   - full-batch folder creation / commit failure.
//   - ErrInvalidInput        - empty VideoID/URL/MaxSegments<=0/DriveRootFolder.
//   - ErrHashFailed          - file hash computation failed.
//
// Step 3 retirement of godlike/06 drift: the use case no longer imports
// internal/infrastructure/files directly. The HasherPort inline interface
// is injected via NewExtractImportantClipsUseCase as the 8th constructor
// argument; the canonical MD5 implementation lives in the application-layer
// adapter (internal/app/adapters_extract_important.go::md5HasherAdapter).
//
// Per godlike/06: inline ports (FASE-X forward-pointer; future one-time
// mechanical port-move consolidates them into internal/application/youtube/ports/ports.go).
package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"go.uber.org/zap"
)

// ── Typed sentinels (godlike/07) ──────────────────────────────────────

var (
	ErrSubtitleUnavailable = errors.New("extract_important_clips: subtitle fetcher unavailable")
	ErrAnalyzerUnavailable = errors.New("extract_important_clips: analyzer unavailable")
	ErrNoSegments          = errors.New("extract_important_clips: no segments identified")
	ErrClipPublishFailed   = errors.New("extract_important_clips: clip publish failed")
	ErrInvalidInput        = errors.New("extract_important_clips: invalid input")
	ErrHashFailed          = errors.New("extract_important_clips: file hash failed")
)

// ── Inline ports (FASE-X forward-pointer, godlike/06 minor drift ack) ─────

type TranscriptFetcherPort interface {
	FetchTranscript(ctx context.Context, videoID, language string) (*Transcript, error)
}

type AnalyzerPort interface {
	AnalyzeImportantSegments(ctx context.Context, transcript *Transcript, max int) ([]Segment, error)
}

type SectionDownloaderPort interface {
	DownloadSection(ctx context.Context, videoURL string, startSec, endSec float64) (string, error)
}

type DriveFolderCreatorPort interface {
	EnsureFolder(ctx context.Context, parentFolderID, folderName string) (string, error)
}

type DriveUploaderPort interface {
	// TODO(P0.4): migrate to delivery.Publisher.Publish (DRIVE-CUTOVER-P0-1)
	UploadFile(ctx context.Context, localPath, folderID, fileName string) (*youtubeports.UploadResultDTO, error)
}

// HasherPort (Step 3, retires godlike/06 drift from internal/infrastructure/files direct import).
type HasherPort interface {
	HashFile(ctx context.Context, path string) (string, error)
}

// ── Domain DTOs (godlike/06 one canonical shape per type) ──────────────────

type Transcript struct {
	VideoID  string
	Language string
	Entries  []TranscriptEntry
}

type TranscriptEntry struct {
	Text     string
	StartSec float64
	EndSec   float64
}

type Segment struct {
	StartSec    float64
	EndSec      float64
	Description string
}

type ExtractImportantClipsCommand struct {
	VideoID         string `json:"video_id"`
	URL             string `json:"url"`
	Language        string `json:"language"`
	Category        string `json:"category,omitempty"`
	MaxSegments     int    `json:"max_segments"`
	PolicyVersion   string `json:"policy_version,omitempty"`
	DriveRootFolder string `json:"drive_root_folder,omitempty"`
}

type ExtractImportantClipsResult struct {
	VideoID        string     `json:"video_id"`
	Language       string     `json:"language"`
	SegmentsTotal  int        `json:"segments_total"`
	ClipsProcessed int        `json:"clips_processed"`
	ClipsFailed    int        `json:"clips_failed"`
	Clips          []ClipItem `json:"clips"`
}

type ClipItem struct {
	ClipID       string  `json:"clip_id"`
	StartSec     float64 `json:"start_sec"`
	EndSec       float64 `json:"end_sec"`
	Status       string  `json:"status"`
	DriveFileID  string  `json:"drive_file_id,omitempty"`
	WebViewLink  string  `json:"web_view_link,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
}

const (
	ClipStatusProcessed = "PROCESSED"
	ClipStatusFailed    = "FAILED"
)

// ── Use case struct (godlike/06 SSOT: ONLY ONE per package) ─────────────────

type ExtractImportantClipsUseCase struct {
	log        *zap.Logger
	subtitles  TranscriptFetcherPort
	analyzer   AnalyzerPort // NIL-TOLERANT per spec (AnalyzerPort is forward-pointer).
	downloader SectionDownloaderPort
	folder     DriveFolderCreatorPort
	uploader   DriveUploaderPort
	writer     youtubeports.ClipAtomicWriter
	hasher     HasherPort // Step 3: replaces prior direct files.MD5File import (godlike/06 drift fix).
}

// ExtractImportantClipsDeps bundles the ports required by
// ExtractImportantClipsUseCase so the constructor stays under the
// archcheck 8-parameter cap.
type ExtractImportantClipsDeps struct {
	Log        *zap.Logger
	Subtitles  TranscriptFetcherPort
	Analyzer   AnalyzerPort // nil-tolerant
	Downloader SectionDownloaderPort
	Folder     DriveFolderCreatorPort
	Uploader   DriveUploaderPort
	Writer     youtubeports.ClipAtomicWriter
	Hasher     HasherPort
}

func NewExtractImportantClipsUseCase(deps ExtractImportantClipsDeps) *ExtractImportantClipsUseCase {
	if deps.Log == nil || deps.Subtitles == nil || deps.Downloader == nil ||
		deps.Folder == nil || deps.Uploader == nil || deps.Writer == nil ||
		deps.Hasher == nil {
		panic("ExtractImportantClipsUseCase.New: required port nil (analyzer is nil-tolerant)")
	}
	return &ExtractImportantClipsUseCase{
		log: deps.Log, subtitles: deps.Subtitles, analyzer: deps.Analyzer,
		downloader: deps.Downloader, folder: deps.Folder, uploader: deps.Uploader,
		writer: deps.Writer, hasher: deps.Hasher,
	}
}

// ── Execute (canonical) ─────────────────────────────────────────────

func (uc *ExtractImportantClipsUseCase) Execute(ctx context.Context, cmd ExtractImportantClipsCommand) (*ExtractImportantClipsResult, error) {
	if cmd.VideoID == "" || cmd.URL == "" || cmd.MaxSegments <= 0 {
		return nil, fmt.Errorf("%w: video_id/url required and max_segments > 0", ErrInvalidInput)
	}
	if cmd.DriveRootFolder == "" {
		return nil, fmt.Errorf("%w: drive_root_folder required for clip publishing", ErrInvalidInput)
	}

	lang := cmd.Language
	if lang == "" {
		lang = "und"
	}
	transcript, err := uc.subtitles.FetchTranscript(ctx, cmd.VideoID, lang)
	if err != nil || transcript == nil {
		return nil, fmt.Errorf("%w: video_id=%s lang=%s: %v", ErrSubtitleUnavailable, cmd.VideoID, lang, err)
	}

	if uc.analyzer == nil {
		return nil, fmt.Errorf("%w: no LLM analyzer wired (analyzer is forward-pointer)", ErrAnalyzerUnavailable)
	}
	segments, err := uc.analyzer.AnalyzeImportantSegments(ctx, transcript, cmd.MaxSegments)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnalyzerUnavailable, err)
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("%w: video_id=%s max=%d", ErrNoSegments, cmd.VideoID, cmd.MaxSegments)
	}

	folderName := sanitizeFolderToken(cmd.VideoID)
	folderID, err := uc.folder.EnsureFolder(ctx, cmd.DriveRootFolder, folderName)
	if err != nil {
		return nil, fmt.Errorf("%w: ensure folder %s: %v", ErrClipPublishFailed, folderName, err)
	}

	clips := publishSegmentsParallel(ctx, uc, cmd, transcript.Language, folderID, segments)

	res := &ExtractImportantClipsResult{
		VideoID:       cmd.VideoID,
		Language:      transcript.Language,
		SegmentsTotal: len(segments),
	}
	for _, c := range clips {
		if c.Status == ClipStatusProcessed {
			res.ClipsProcessed++
		} else {
			res.ClipsFailed++
		}
	}
	res.Clips = clips
	return res, nil
}

// ── Per-clip parallel fan-out (godlike/07 no-fake-availability) ─────────────

func publishSegmentsParallel(
	ctx context.Context,
	uc *ExtractImportantClipsUseCase,
	cmd ExtractImportantClipsCommand,
	language string,
	folderID string,
	segments []Segment,
) []ClipItem {
	var (
		mu    sync.Mutex
		items = make([]ClipItem, 0, len(segments))
		wg    sync.WaitGroup
	)
	for _, seg := range segments {
		wg.Add(1)
		go func(s Segment) {
			defer wg.Done()
			item := uc.publishOneSegment(ctx, cmd, language, folderID, s)
			mu.Lock()
			items = append(items, item)
			mu.Unlock()
		}(seg)
	}
	wg.Wait()
	return items
}

// publishOneSegment: per-clip pipeline (download -> upload -> hash -> commit).
//
// Step 3 change: the prior `files.MD5File(localPath)` direct call became
// `uc.hasher.HashFile(ctx, localPath)` — godlike/06 application-layer purity
// restored (no infra import in the use case).
func (uc *ExtractImportantClipsUseCase) publishOneSegment(
	ctx context.Context,
	cmd ExtractImportantClipsCommand,
	language string,
	folderID string,
	seg Segment,
) ClipItem {
	clipID := buildClipID(cmd.VideoID, seg, cmd.PolicyVersion)
	item := ClipItem{
		ClipID:   clipID,
		StartSec: seg.StartSec,
		EndSec:   seg.EndSec,
		Status:   ClipStatusFailed,
	}

	// (a) download via yt-dlp SectionDownloader
	localPath, err := uc.downloader.DownloadSection(ctx, cmd.URL, seg.StartSec, seg.EndSec)
	if err != nil || localPath == "" {
		item.ErrorMessage = fmt.Sprintf("download section: %v", err)
		uc.log.Warn("clip.download.failed", zap.String("clip_id", clipID), zap.Error(err))
		return item
	}
	if fi, statErr := os.Stat(localPath); statErr != nil || (fi != nil && fi.Size() == 0) {
		item.ErrorMessage = fmt.Sprintf("stat local path: %v", statErr)
		uc.log.Warn("clip.stat.failed", zap.String("clip_id", clipID), zap.String("path", localPath))
		return item
	}

	// (b) upload to per-video Drive subfolder
	// TODO(P0.4): migrate to delivery.Publisher.Publish (DRIVE-CUTOVER-P0-1)
	upload, err := uc.uploader.UploadFile(ctx, localPath, folderID, clipID+".mp4")
	if err != nil || upload == nil || upload.FileID == "" {
		item.ErrorMessage = fmt.Sprintf("drive upload: %v", err)
		uc.log.Warn("clip.upload.failed", zap.String("clip_id", clipID), zap.String("folder", folderID), zap.Error(err))
		return item
	}
	item.DriveFileID = upload.FileID
	item.WebViewLink = upload.WebViewLink

	// (c) Compute FileHash via HasherPort (Step 3 — NO direct infra import).
	//
	// godlike/07 typed-error contract: the error message string must remain
	// `errors.Is(err, ErrHashFailed)`-probeable at higher layers. `fmt.Sprintf`
	// does NOT support `%w`, so we wrap with `fmt.Errorf` (which DOES support
	// `%w`) and stringify the result for the on-wire `ClipItem.ErrorMessage`.
	fileHash, hashErr := uc.hasher.HashFile(ctx, localPath)
	if hashErr != nil {
		wrapErr := fmt.Errorf("%w: %v", ErrHashFailed, hashErr)
		item.ErrorMessage = wrapErr.Error()
		uc.log.Warn("clip.hash.failed", zap.String("clip_id", clipID), zap.Error(wrapErr))
		return item
	}

	// (d) Canonical ClipAtomicWriter commit (single-tx media_assets INSERT + outbox emit).
	asset := youtubetypes.ClipAsset{
		ID:            clipID,
		VideoID:       cmd.VideoID,
		LocalPath:     localPath,
		FileHash:      fileHash,
		PolicyVersion: cmd.PolicyVersion,
		Drive: youtubetypes.ClipAssetDrive{
			FolderID:    folderID,
			FileID:      upload.FileID,
			WebViewLink: upload.WebViewLink,
		},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: int(seg.StartSec),
			EndSec:   int(seg.EndSec),
			Duration: int(seg.EndSec - seg.StartSec),
		},
		Metadata: youtubetypes.CanonicalClipMetadata{
			ClipID:  clipID,
			AssetID: upload.FileID,
			Summary: seg.Description,
		},
	}
	event := youtubeports.IndexEventPayload{
		AggregateID: clipID,
		CreatedAt:   time.Now().UTC(),
	}
	if err := uc.writer.CommitClipAndIndexEvent(ctx, clipID, asset, event); err != nil {
		item.ErrorMessage = fmt.Sprintf("clip atomic commit: %v", err)
		uc.log.Warn("clip.commit.failed", zap.String("clip_id", clipID), zap.Error(err))
		return item
	}

	item.Status = ClipStatusProcessed
	uc.log.Info("clip.processed",
		zap.String("clip_id", clipID),
		zap.String("video_id", cmd.VideoID),
		zap.String("drive_file_id", upload.FileID),
	)
	return item
}

// ── helpers (pure) ─────────────────────────────────────────────────────

func buildClipID(videoID string, seg Segment, policyVersion string) string {
	pv := policyVersion
	if pv == "" {
		pv = "v1"
	}
	return fmt.Sprintf("yt_%s_%.0f_%.0f_%s", videoID, seg.StartSec, seg.EndSec, pv)
}

func sanitizeFolderToken(videoID string) string {
	out := make([]byte, 0, len(videoID))
	for i := 0; i < len(videoID); i++ {
		c := videoID[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-' || c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "yt_video"
	}
	return "yt_" + string(out)
}
