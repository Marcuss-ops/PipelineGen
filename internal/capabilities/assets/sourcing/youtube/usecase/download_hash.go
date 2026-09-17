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
// owner of YouTube-clip download + hash for the sourcing/youtube registration
// pipeline. It is NOT the owner of the clip identity format: the asset-id
// format `yt_<videoID>_<startSec>_<endSec>_<policyVersion>` is owned by
// kernel/asset/detail.YouTubeClipAssetID, the same builder the extraction
// pipeline uses (2026-09-17 identity unification). The MD5 file hash stays the
// CONTENT identity (media_assets.legacy_file_md5 + supersede gate), never the
// logical clip identity.
package usecase

import (
	"context"
	"fmt"
	"time"

	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
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
	// PolicyVersion is the clip-identity policy component. Empty falls back to
	// detail.DefaultYouTubeClipPolicyVersion ("v1"), which is what the
	// extraction pipeline uses when the caller declares no policy — so the two
	// ingest paths mint the SAME asset id for the same window.
	PolicyVersion string
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
	ClipID        string            // canonical yt_<videoID>_<startSec>_<endSec>_<policyVersion> identifier
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
// canonical clipID from the REQUEST WINDOW (videoID + start + end + policy),
// not from the downloaded bytes. It is a thin orchestration function:
//
//  1. nil-fetcher guard → returns typed error (fail-closed)
//  2. Delegate to fetcher.Fetch with the command fields
//  3. On fetch failure → wraps error with usecase prefix
//  4. Compute MD5 hash via hasher (nil-safe: empty hash when hasher is nil)
//  5. Derive the canonical identity via detail.YouTubeClipAssetID
//
// The hash-is-empty case is still NOT an error: the hash is the CONTENT
// identity and the caller (Register()) logs a warning and proceeds. It can no
// longer degrade the clip IDENTITY — before the 2026-09-17 unification an
// unusable hash produced the degenerate id `yt_<videoID>_`, which collided
// across every window of the same video (one segment silently overwriting
// another). The window-derived identity is well-defined even when the bytes
// cannot be hashed.
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

	clipID, err := deriveClipID(cmd.VideoID, cmd.SegmentStart, cmd.SegmentEnd, cmd.PolicyVersion)
	if err != nil {
		return nil, fmt.Errorf("usecase.DownloadAndHashClip: derive clip id: %w", err)
	}

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

// deriveClipID builds the canonical clip identifier from the videoID, the
// requested window and the policy version. It delegates the FORMAT to
// detail.YouTubeClipAssetID so an identical window cannot mint two different
// asset ids depending on which ingest route handled it.
//
// Seconds are truncated to whole seconds (int) because the extraction
// pipeline parses "HH:MM:SS" into integer seconds: both paths must land on the
// same integer window to converge on the same primary key.
func deriveClipID(videoID string, segmentStart, segmentEnd time.Duration, policyVersion string) (string, error) {
	startSec := int(segmentStart / time.Second)
	endSec := int(segmentEnd / time.Second)
	return detail.YouTubeClipAssetID(videoID, startSec, endSec, policyVersion)
}
