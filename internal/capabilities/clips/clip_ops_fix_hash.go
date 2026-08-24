package clips

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// FixHash orchestrates the fix-hash recovery flow:
//  1. resolve repo for `source`
//  2. reject voiceover source
//  3. read clip from repo
//  4. extract Drive fileID from clip.DriveLink/DownloadLink
//  5. fetch MD5 from Drive
//  6. set LegacyFileMD5 on the clip
//  7. delegate to dispatcher.EnqueueAndIndex (the canonical SSOT
//     writer + outbox event emitter).
//
// S1d (June 2026): PR-CLIP-RAW-MUTATIONS compliance. The dispatcher
// is the ONLY canonical writer route; restore / fix-hash go through it.
func (s *ClipOpsService) FixHash(ctx context.Context, source, clipID string) (*FixHashReport, error) {
	report := &FixHashReport{
		Source: source,
		ClipID: clipID,
	}

	// S1d: voiceover records live in voiceovers (separate table)
	// which AssetMutationDispatcher does not write to.
	if strings.EqualFold(source, "voiceover") {
		return nil, ErrFixHashVoiceoverUnsupported
	}

	repo := s.resolveRepo(source)
	if repo == nil {
		return nil, fmt.Errorf("fix-hash: invalid source: %q", source)
	}
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		return nil, fmt.Errorf("fix-hash: read clip %q: %w", clipID, err)
	}
	report.PreviousHash = clip.LegacyFileMD5()

	driveLink := clip.DriveLink()
	if driveLink == "" {
		driveLink = clip.DownloadLink()
	}
	if driveLink == "" {
		return nil, ErrFixHashMissingDriveLink
	}
	fileID, err := urlutil.FileIDFromDriveLink(driveLink)
	if err != nil || fileID == "" {
		return nil, fmt.Errorf("fix-hash: drive_link %q has no extractable file id", driveLink)
	}
	if s.driveUploader == nil {
		return nil, fmt.Errorf("fix-hash: drive_uploader not wired")
	}
	md5, err := s.driveUploader.GetFileMD5(ctx, fileID)
	if err != nil || md5 == "" {
		return nil, fmt.Errorf("fix-hash: drive GetFileMD5(%s): %w", fileID, err)
	}
	clip.SetLegacyFileMD5(md5)
	report.NewHash = md5

	if s.dispatcher == nil {
		report.OK = false
		report.Message = "dispatcher not wired; clip mutation NOT persisted"
		return report, ErrFixHashDispatcherUnavailable
	}
	if err := s.dispatcher.EnqueueAndIndex(ctx, clip, md5); err != nil {
		report.OK = false
		report.Message = fmt.Sprintf("dispatcher reject: %v", err)
		return report, fmt.Errorf("fix-hash: dispatcher.EnqueueAndIndex: %w", err)
	}
	report.OK = true
	report.Reindexed = true
	report.DispatcherOK = true
	report.Message = "fix-hash applied (outbox event emitted; clip sees re-index)"
	return report, nil
}
