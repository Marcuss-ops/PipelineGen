// Package usecase — extraction_destination.go: pure path + Drive
// resolvers for an extraction request.
//
// PR-GODOBJ-1 (July 2026): pure-function helpers split out of the
// legacy extraction_service.go god service per godlike/06 SSOT
// (one canonical owner per fact: outDir + Drive destination resolution
// lives ONLY here). All helpers are side-effect free so they can be
// unit-tested without a wired DriveFolderManagerPort.
package usecase

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// Destination is the canonical typed envelope for an extraction's
// Drive upload target. The prior god-service inline triple-return
// shape (folderID + folderPath + flag-string) is collapsed here into
// a single typed struct per AGENTS.md Pattern 0 (one canonical owner
// per fact).
type Destination struct {
	FolderID   string
	FolderPath string
}

// resolveOutDir derives the canonical local output directory for a
// given video + group. Format: <DataDir>/media/clips/<group>/yt_<videoID>.
// Mirrors the prior extraction_service.go:208 derivation exactly so on-disk
// path compatibility is preserved (PR-GODOBJ-1 must NOT change wheel
// behaviour; only split).
func resolveOutDir(dataDir, videoID, group string) string {
	if group == "" {
		group = "general"
	}
	folderSlug := "yt_" + videoID
	return filepath.Join(dataDir, "media", "clips", group, folderSlug)
}

// resolveDestination extracts the canonical Drive destination from
// the inbound request. nil- and empty-string tolerant: a nil
// Destination returns the zero-value Destination; an empty Destination
// returns the same (whitespace-only trimmed to "").
func resolveDestination(req *youtubetypes.ExtractRequest) Destination {
	if req == nil || req.Destination == nil {
		return Destination{}
	}
	return Destination{
		FolderID:   strings.TrimSpace(req.Destination.FolderID),
		FolderPath: strings.TrimSpace(req.Destination.FolderPath),
	}
}

// resolveSubtitleDestination resolves the Drive folder that subtitle
// sidecars are uploaded into — ONCE per extraction (Sept 2026 N→1
// contract). Previously every segment called GetOrCreateFolder(videoID)
// inside Step 6-9, issuing N Drive round-trips for the same folder.
//
// Policy:
//   - no SubtitleDestination or empty FolderID → "" (Step 6-9 skips upload)
//   - PerClipSubfolders=true → the per-video child folder is get-or-created
//     ONCE here and every segment uploads straight into it
//   - PerClipSubfolders=false → the configured root IS the upload target
//
// Fail-closed (godlike/07): a resolution failure aborts the whole
// extraction with a typed error instead of silently uploading subtitles
// into the wrong folder.
func (s *ExtractionService) resolveSubtitleDestination(ctx context.Context, req *youtubetypes.ExtractRequest, videoID string) (string, error) {
	if s == nil || req == nil || req.SubtitleDestination == nil {
		return "", nil
	}
	root := strings.TrimSpace(req.SubtitleDestination.FolderID)
	if root == "" {
		return "", nil
	}
	if !req.SubtitleDestination.PerClipSubfolders {
		return root, nil
	}
	if s.processSeg == nil {
		return "", fmt.Errorf("youtube extraction: subtitle subfolder requested but segment pipeline not wired")
	}
	resolved, err := s.processSeg.ResolveSubtitleFolder(ctx, videoID, root)
	if err != nil {
		return "", fmt.Errorf("youtube extraction: resolve subtitle folder: %w", err)
	}
	if resolved == "" {
		return "", fmt.Errorf("youtube extraction: subtitle folder resolution returned an empty folder id")
	}
	return resolved, nil
}
