// Package usecase — DownloadAndHashClip is a narrow use case extracted from
// youtube/service.go::Register() (PR-CLIP-DECOM-2, July 2026).
//
// It owns the fetch + hash + clipID-derivation steps (steps 4 and 7) of the
// legacy 14-step Register pipeline: download the YouTube video via yt-dlp,
// compute the MD5 file hash, and derive the canonical clipID.
//
// Per AGENTS.md Pattern 0 + Pattern 5: the use case depends on two narrow
// ports — Fetcher (single Fetch method) and FileHasher (single MD5File
// method) — rather than importing sourcing.FetchProviderPort or
// pkg/hashutil directly. Adapters live in the composition root.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the canonical
// owner of YouTube-clip download + hash + clipID derivation for the
// sourcing/youtube registration pipeline. The clipID format
// yt_<videoID>_<hash8> is owned here.
package usecase

import (
	"context"
	"fmt"
	"time"
)

// DownloadAndHashCommand carries every input needed to download a YouTube
// clip and derive its identity. It mirrors the fields that Register()
// passes to the fetcher.
type DownloadAndHashCommand struct {
	VideoID      string        // extracted from URL by ResolveClipMetadata
	FetchAssetID string        // unique per segment; defaults to VideoID when empty
	SourceRef    string        // canonical YouTube URL
	SegmentStart time.Duration // start offset in seconds
	SegmentEnd   time.Duration // end offset in seconds
	NoAudio      bool          // when true, strip audio from the fetched clip
}

// DownloadAndHashResult is the canonical output of the fetch + hash step.
// Every downstream step in Register() (metadata, drive, db) reads from
// these fields.
type DownloadAndHashResult struct {
	LocalPath     string            // path to the downloaded .mp4 on disk
	AssetID       string            // provider-side asset identifier
	Name          string            // fetched video title
	Duration      time.Duration     // fetched video duration
	Bytes         int64             // file size in bytes
	Metadata      map[string]string // provider metadata (description, uploader, etc.)
	LegacyFileMD5 string            // MD5 hex digest (empty when hasher is nil or fails)
	ClipID        string            // canonical yt_<videoID>_<hash8> identifier
}

// Fetcher is the narrow port for downloading a video from an external
// provider (YouTube via yt-dlp). There is exactly ONE method: Fetch.
//
// The concrete adapter (composition root) wraps sourcing.FetchProviderPort
// and translates the local FetchRequest / FetchedAsset shapes.
type Fetcher interface {
	Fetch(ctx context.Context, req FetchRequest) (*FetchedAsset, error)
}

// FetchRequest is the use-case-owned wire shape for a video download.
type FetchRequest struct {
	AssetID      string
	SourceRef    string
	SegmentStart time.Duration
	SegmentEnd   time.Duration
	NoAudio      bool
}

// FetchedAsset is the use-case-owned wire shape for a completed download.
type FetchedAsset struct {
	LocalPath string
	AssetID   string
	Name      string
	Duration  time.Duration
	Bytes     int64
	Metadata  map[string]string
}

// FileHasher is the narrow port for computing a file checksum.
// There is exactly ONE method: MD5File.
type FileHasher interface {
	MD5File(path string) (string, error)
}

// DownloadAndHashClip downloads a YouTube video segment and derives its
// canonical clipID from the MD5 file hash. It is a thin orchestration
// function:
//
//  1. nil-fetcher guard → returns typed error (fail-closed)
//  2. Delegate to fetcher.Fetch with the command fields
//  3. On fetch failure → wraps error with usecase prefix
//  4. Compute MD5 hash via hasher (nil-safe: empty hash when hasher is nil)
//  5. Derive clipID as yt_<videoID>_<hash8> (truncated to 8 hex chars)
//
// The hash-is-empty case is intentionally NOT an error — the caller
// (Register()) logs a warning and proceeds with a best-effort clipID
// that carries an empty suffix. This mirrors the pre-extraction behavior
// and preserves backward compatibility for clips with corrupted or
// inaccessible local files.
func DownloadAndHashClip(ctx context.Context, fetcher Fetcher, hasher FileHasher, cmd DownloadAndHashCommand) (*DownloadAndHashResult, error) {
	if fetcher == nil {
		return nil, fmt.Errorf("usecase.DownloadAndHashClip: fetcher is nil")
	}

	fetchAssetID := cmd.FetchAssetID
	if fetchAssetID == "" {
		fetchAssetID = cmd.VideoID
	}
	fetched, err := fetcher.Fetch(ctx, FetchRequest{
		AssetID:      fetchAssetID,
		SourceRef:    cmd.SourceRef,
		SegmentStart: cmd.SegmentStart,
		SegmentEnd:   cmd.SegmentEnd,
		NoAudio:      cmd.NoAudio,
	})
	if err != nil {
		return nil, fmt.Errorf("usecase.DownloadAndHashClip: fetch: %w", err)
	}

	fileHash := ""
	if hasher != nil {
		if h, herr := hasher.MD5File(fetched.LocalPath); herr == nil {
			fileHash = h
		}
	}

	clipID := deriveClipID(cmd.VideoID, fileHash)

	return &DownloadAndHashResult{
		LocalPath:     fetched.LocalPath,
		AssetID:       fetched.AssetID,
		Name:          fetched.Name,
		Duration:      fetched.Duration,
		Bytes:         fetched.Bytes,
		Metadata:      fetched.Metadata,
		LegacyFileMD5: fileHash,
		ClipID:        clipID,
	}, nil
}

// deriveClipID builds the canonical clip identifier from the videoID and
// the first 8 hex characters of the MD5 file hash. When fileHash is empty
// the suffix is empty, producing yt_<videoID>_ — the caller is responsible
// for logging the warning.
func deriveClipID(videoID, fileHash string) string {
	suffix := fileHash
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return fmt.Sprintf("yt_%s_%s", videoID, suffix)
}
