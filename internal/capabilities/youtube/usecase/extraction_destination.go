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

// validateDriveDestination fails closed when the caller expressed a Drive
// destination intent that the resolution step could not turn into a root
// folder id.
//
// godlike/07 no-fake-availability. Observed live (2026-09-16): a request
// carrying `destination.group` + `create_subfolder:true` but no `folder_id`
// resolved to FolderID == "". The subfolder block in ExtractService.Extract is
// gated on FolderID != "", so no folder was materialised; Step 8's own gate
// (`cmd.DriveFolderID != ""`) then skipped the upload; and the job still
// returned ok / processed with an item carrying no drive_file_id. The caller
// asked for Drive delivery and got a silent local-only extraction.
//
// The rule: a Drive destination is a CONTRACT, not a hint. If any part of the
// destination expresses Drive intent (a folder path, a subfolder name, or a
// group that the transport would have turned into a root), the resolved root
// MUST exist.
//
// A nil destination, or a Destination whose fields are all empty, is NOT
// intent: a local-only extraction stays legitimate.
//
// Pure and side-effect free so the rule is testable without a wired Drive
// resolver (see the package doc of this file).
func validateDriveDestination(dest Destination, req *youtubetypes.ExtractRequest) error {
	if req == nil || req.Destination == nil {
		return nil
	}
	if dest.FolderID != "" {
		return nil
	}
	subfolder := strings.TrimSpace(req.Destination.SubfolderName)
	group := strings.TrimSpace(req.Destination.Group)
	if strings.TrimSpace(dest.FolderPath) == "" && subfolder == "" && group == "" {
		// No Drive intent at all: nothing to honour, nothing to refuse.
		return nil
	}
	return fmt.Errorf(
		"youtube extraction: Drive destination requested but the root folder did not resolve to a folder id (group=%q folder_path=%q subfolder=%q) — refusing to report success for an extraction that would upload nothing",
		group, strings.TrimSpace(dest.FolderPath), subfolder)
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
