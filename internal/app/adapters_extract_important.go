// Package app - adapters_extract_important.go: PR-GEMMA-EXTRACT-IMPORTANT Step 3.
//
// Concrete adapter wiring for the 6 inline ports declared in
// internal/application/youtube/usecase/extract_important_clips.go
// (5 ports [TranscriptFetcher / Analyzer / SectionDownloader /
// DriveFolderCreator / DriveUploader] + 1 new HasherPort added in
// Step 3 to retire the prior direct internal/infrastructure/files.MD5File
// import — godlike/06 SSOT drift fix).
//
// Per godlike/06 one-canonical-owner-per-fact, EACH adapter is the SOLE owner
// of its port implementation. Compile-time `var _` pins lock signature
// drift to build-failure (not runtime panic).
//
// Per godlike/07 fail-closed at composition, BuildExtractImportantClipsAdapters
// panics on nil critical deps. Analyzer is intentionally NOT exposed
// here — it's a nil-tolerant forward-pointer at the use case ctor (per spec).
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/application/transcripts"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtubeusecase "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// ── 6 compile-time pins (godlike/06 lock signature drift to build-failure) ─

var (
	_ youtubeusecase.TranscriptFetcherPort  = (*transcriptFetcherAdapter)(nil)
	_ youtubeusecase.AnalyzerPort           = (*failClosedAnalyzerAdapter)(nil)
	_ youtubeusecase.SectionDownloaderPort  = (*sectionDownloaderAdapter)(nil)
	_ youtubeusecase.DriveFolderCreatorPort = (*driveFolderCreatorAdapter)(nil)
	_ youtubeusecase.DriveUploaderPort      = (*driveUploaderAdapter)(nil)
	_ youtubeusecase.HasherPort             = (*md5HasherAdapter)(nil)
)

// ── 1. TranscriptFetcherAdapter ─────────────────────────────────────

// transcriptFetcherAdapter wraps the canonical application-layer subtitle
// adapter (transcripts.YTDLPSubtitleAdapter). Maps an in-process yt-dlp
// subtitle fetch (TranscriptDocument with Entries[].TimedEntry) to the
// use case's Transcript shape.
type transcriptFetcherAdapter struct {
	sub *transcripts.YTDLPSubtitleAdapter
}

func (a *transcriptFetcherAdapter) FetchTranscript(ctx context.Context, videoID, language string) (*youtubeusecase.Transcript, error) {
	if a.sub == nil {
		return nil, fmt.Errorf("transcriptFetcherAdapter: subtitle adapter unwired")
	}
	videoURL := "https://www.youtube.com/watch?v=" + videoID
	doc, err := a.sub.Fetch(ctx, videoURL)
	if err != nil {
		return nil, fmt.Errorf("transcriptFetcherAdapter: fetch %s: %w", videoURL, err)
	}
	out := &youtubeusecase.Transcript{
		VideoID:  doc.VideoID,
		Language: doc.Language,
	}
	for _, e := range doc.Entries {
		out.Entries = append(out.Entries, youtubeusecase.TranscriptEntry{
			Text:     e.Text,
			StartSec: e.Start,
			EndSec:   e.End,
		})
	}
	return out, nil
}

// ── 2. FailClosedAnalyzerAdapter ─────────────────────────────────

// failClosedAnalyzerAdapter: until the LLM analyzer backend lands
// (PR-GEMMA forward-pointer), this adapter blocks the use case Execute
// path with a typed ErrAnalyzerUnavailable sentinel. godlike/07 fail-closed
// means the use case returns the typed error to the broker; no silent
// 0-segments success.
type failClosedAnalyzerAdapter struct{}

func (a *failClosedAnalyzerAdapter) AnalyzeImportantSegments(ctx context.Context, transcript *youtubeusecase.Transcript, max int) ([]youtubeusecase.Segment, error) {
	return nil, fmt.Errorf("%w: analyzer backend not yet wired (PR-GEMMA forward-pointer)", youtubeusecase.ErrAnalyzerUnavailable)
}

// ── 3. SectionDownloaderAdapter ──────────────────────────────────

// sectionDownloaderAdapter wraps downloader.YTDLPDownloader.DownloadSections
// (single-section case from a [startSec, endSec] pair). Writes to a
// per-call temp dir (defer-cleaned).
type sectionDownloaderAdapter struct {
	dl *downloader.YTDLPDownloader
}

func (a *sectionDownloaderAdapter) DownloadSection(ctx context.Context, videoURL string, startSec, endSec float64) (string, error) {
	if a.dl == nil {
		return "", fmt.Errorf("sectionDownloaderAdapter: downloader unwired")
	}
	tmpDir, err := os.MkdirTemp("", "gemma_clip_*")
	if err != nil {
		return "", fmt.Errorf("sectionDownloaderAdapter: mkdir temp: %w", err)
	}
	// DO NOT defer os.RemoveAll here — the caller needs the file after
	// this function returns (stat, upload to Drive, hash). Cleanup is
	// deferred to the per-job workspace eviction (or OS-level /tmp cleanup).
	outPath := filepath.Join(tmpDir, fmt.Sprintf("clip_%.0f_%.0f.mp4", startSec, endSec))
	sectionSpec := fmt.Sprintf("*%.0f-%.0f", startSec, endSec)
	segs, err := a.dl.DownloadSections(ctx, &downloader.DownloadRequest{
		URL:              videoURL,
		OutputPath:       outPath,
		DownloadSections: []string{sectionSpec},
	})
	if err != nil {
		return "", fmt.Errorf("sectionDownloaderAdapter: download %s [%s]: %w", videoURL, sectionSpec, err)
	}
	if len(segs) == 0 {
		return "", fmt.Errorf("sectionDownloaderAdapter: no segments returned for %s [%s]", videoURL, sectionSpec)
	}
	return segs[0].Path, nil
}

// ── 4. DriveFolderCreatorAdapter ─────────────────────────────────

// driveFolderCreatorAdapter wraps drive.FolderManagerPort (the canonical
// narrow port the delivery.Publisher consumes). The F3.14 narrow-port
// commitment keeps the adapter to just EnsureFolder.
type driveFolderCreatorAdapter struct {
	fm drive.FolderManagerPort
}

func (a *driveFolderCreatorAdapter) EnsureFolder(ctx context.Context, parentFolderID, folderName string) (string, error) {
	if a.fm == nil {
		return "", fmt.Errorf("driveFolderCreatorAdapter: folder manager unwired")
	}
	return a.fm.EnsureFolder(ctx, parentFolderID, folderName)
}

// ── 5. DriveUploaderAdapter ──────────────────────────────────────

// drivePutFn is the canonical PutFile signature. Declared as a func type so
// the adapter does NOT depend on the concrete drive.PutFileRequest /
// drive.PutFileResult types (those types live in publisher_types.go and
// are kept loose-typed here for forward-pointer flexibility). The wire
// site in build_bundles_youtube.go provides a concrete closure at
// composition time.
type drivePutFn func(ctx context.Context, req drivePutFnRequest) (*drivePutFnResult, error)

type drivePutFnRequest struct {
	LocalPath string
	FolderID  string
	Filename  string
}

type drivePutFnResult struct {
	FileID      string
	WebViewLink string
}

// DriveUploaderAdapter implements the use case's DriveUploaderPort by
// invoking the injected drivePutFn (which the composition root bridges
// to the canonical drive.FileUploaderPort.PutFile closure).
type driveUploaderAdapter struct {
	putFn drivePutFn
}

func (a *driveUploaderAdapter) UploadFile(ctx context.Context, localPath, folderID, fileName string) (*youtubeports.UploadResultDTO, error) {
	if a.putFn == nil {
		return nil, fmt.Errorf("driveUploaderAdapter: putFn unwired")
	}
	res, err := a.putFn(ctx, drivePutFnRequest{LocalPath: localPath, FolderID: folderID, Filename: fileName})
	if err != nil {
		return nil, fmt.Errorf("driveUploaderAdapter: put %s: %w", fileName, err)
	}
	return &youtubeports.UploadResultDTO{
		FileID:      res.FileID,
		WebViewLink: res.WebViewLink,
	}, nil
}

// ── 6. MD5HasherAdapter ──────────────────────────────────────────

// md5HasherAdapter IS the canonical SOLE owner of the application-layer
// MD5 wrapper. Per godlike/06 SSOT, this is the SINGLE point in the use case
// dependency chain that imports internal/infrastructure/files.MD5File.
// The use case itself remains pure application-layer (no infra imports)
// per godlike/06 layered architecture.
type md5HasherAdapter struct{}

func (a *md5HasherAdapter) HashFile(ctx context.Context, path string) (string, error) {
	return files.MD5File(path)
}

// ── 7. AdminFolderManagerAdapter ─────────────────────────────────

// adminFolderManagerAdapter bridges drive.Admin (the canonical Pattern 0
// port) to drive.FolderManagerPort (the narrow port the gemma use case
// consumes). The Admin interface already exposes GetOrCreateFolder directly;
// this adapter is a typed-shape conversion (godlike/06 Pattern 0: bridge
// admin.GetOrCreateFolder(name, parent) → FolderManagerPort.EnsureFolder(parent, segments...)).
//
// wiring.ComposeRoot-only construction: this adapter is the SOLE caller of
// drive.Admin for the extract-important pipeline; the canonical YouTube
// pipeline uses a different adapter (NewYouTubePublisherDriveAdapter).
type adminFolderManagerAdapter struct {
	admin drive.Admin
}

// EnsureFolder accepts a variadic segments slice per drive.FolderManagerPort (FASE 9
// signature). The gemma use case passes exactly 1 segment (the per-clip subfolder
// name) — multi-segment paths would mean tree-of-folders creation which is a
// YouTube-clip-only concern. We delegate the canonical 1-segment case to
// admin.GetOrCreateFolder and fail-closed on multi-segment misuse.
func (a *adminFolderManagerAdapter) EnsureFolder(ctx context.Context, parent string, segments ...string) (string, error) {
	if a.admin == nil {
		return "", fmt.Errorf("adminFolderManagerAdapter: drive.Admin unwired")
	}
	if len(segments) != 1 {
		return "", fmt.Errorf("adminFolderManagerAdapter: requires exactly 1 segment (got %d); shallow subfolder only", len(segments))
	}
	return a.admin.GetOrCreateFolder(ctx, segments[0], parent)
}

// ProbeFolderAccess delegates to admin.GetFolderName (the canonical
// Drive-side access probe — returns the folder name or ErrFolderInaccessible).
func (a *adminFolderManagerAdapter) ProbeFolderAccess(ctx context.Context, rootID string) error {
	if a.admin == nil {
		return fmt.Errorf("adminFolderManagerAdapter: drive.Admin unwired")
	}
	_, err := a.admin.GetFolderName(ctx, rootID)
	return err
}

// Compile-time pin: adminFolderManagerAdapter must satisfy drive.FolderManagerPort
// (godlike/06 SSOT — signature drift surfaces as build failure, not runtime panic).
var _ drive.FolderManagerPort = (*adminFolderManagerAdapter)(nil)

// ── Factory + deps ───────────────────────────────────────────────────

// ExtractImportantClipsAdapterDeps is the canonical dep contract for
// the factory. Subtitles / Downloader / Folder / Files are required;
// Analyzer / AtomicClipWriter are wired separately at the use-case
// ctor (Analyzer is nil-tolerant forward-pointer; AtomicClipWriter is
// owned by the composition root independently).
type ExtractImportantClipsAdapterDeps struct {
	Subtitles  *transcripts.YTDLPSubtitleAdapter
	Downloader *downloader.YTDLPDownloader
	Folder     drive.FolderManagerPort
	Files      drivePutFn
}

// ExtractImportantClipsAdapters is the assembled port set the use case
// ctor consumes. The 5 ports are concrete adapter instances; the 6th
// (Analyzer) is intentionally omitted — the use case accepts analyzer
// as a separate nil-tolerant parameter.
type ExtractImportantClipsAdapters struct {
	TranscriptFetcher youtubeusecase.TranscriptFetcherPort
	SectionDownloader youtubeusecase.SectionDownloaderPort
	DriveFolder       youtubeusecase.DriveFolderCreatorPort
	DriveUploader     youtubeusecase.DriveUploaderPort
	Hasher            youtubeusecase.HasherPort
}

// BuildExtractImportantClipsAdapters: factory with godlike/07 fail-closed
// (panics on nil critical deps). The use case is later constructed at
// the composition root by passing individual ports from this struct
// (plus the separate analyzer forward-pointer and the ClipAtomicWriter)
// into youtubeusecase.NewExtractImportantClipsUseCase.
func BuildExtractImportantClipsAdapters(deps ExtractImportantClipsAdapterDeps) *ExtractImportantClipsAdapters {
	if deps.Subtitles == nil || deps.Downloader == nil ||
		deps.Folder == nil || deps.Files == nil {
		panic("BuildExtractImportantClipsAdapters: required dep nil (Subtitles, Downloader, Folder, Files)")
	}
	return &ExtractImportantClipsAdapters{
		TranscriptFetcher: &transcriptFetcherAdapter{sub: deps.Subtitles},
		SectionDownloader: &sectionDownloaderAdapter{dl: deps.Downloader},
		DriveFolder:       &driveFolderCreatorAdapter{fm: deps.Folder},
		DriveUploader:     &driveUploaderAdapter{putFn: deps.Files},
		Hasher:            &md5HasherAdapter{},
	}
}
