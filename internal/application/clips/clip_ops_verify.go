package clips

import (
	"context"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Verify reports DB/local/Drive coherence for a single clip.
func (s *ClipOpsService) Verify(ctx context.Context, source, clipID string) *VerifyReport {
	report := &VerifyReport{
		OK:     true,
		Source: source,
		ClipID: clipID,
		Issues: []string{},
		DB:     true,
		Extra:  map[string]any{},
	}

	if clipID == "" {
		report.OK = false
		return report
	}

	// Handle Voiceover source.
	if strings.ToLower(source) == "voiceover" && s.voiceoverRepo != nil {
		rec, err := s.voiceoverRepo.GetByID(ctx, clipID)
		if err != nil {
			report.OK = false
			return report
		}
		if rec == nil {
			report.OK = false
			return report
		}
		clip := voiceoverDTOToClip(rec)
		return s.verifyClip(ctx, source, nil, clip)
	}

	repo := s.resolveRepo(source)
	if repo == nil {
		report.OK = false
		report.Issues = append(report.Issues, "invalid_source")
		return report
	}

	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		report.OK = false
		return report
	}
	return s.verifyClip(ctx, source, repo, clip)
}

// verifyClip is the private verifier. Mirrors the legacy verifyClip
// in api/clip_ops.go; takes a repo (might be nil for voiceover
// source) and a clip.
//
// S1c (June 2026) — verifyClip is now strictly read-only.
// When the local clip row has no LegacyFileMD5 but Drive supplies a
// matching MD5, the report exposes the candidate via
// (HashRecoverable, HashRecoverableValue) but DOES NOT mutate
// the row.
func (s *ClipOpsService) verifyClip(ctx context.Context, source string, repo ClipRepositoryPort, clip *asset.Asset) *VerifyReport {
	report := &VerifyReport{
		OK:     true,
		Source: source,
		ClipID: clip.ID,
		Issues: []string{},
		DB:     true,
		Extra:  map[string]any{},
	}

	// Check local file
	hasLocalFile := false
	if clip.LocalPath() != "" {
		if _, statErr := os.Stat(clip.LocalPath()); statErr == nil {
			hasLocalFile = true
			report.LocalFile = true
			report.LocalPath = clip.LocalPath()
		} else {
			report.LocalFile = false
			report.LocalPath = clip.LocalPath()
			report.LocalError = "file not found: " + statErr.Error()
			report.Issues = append(report.Issues, "local_file_missing")
		}
	} else {
		report.LocalFile = false
		report.Issues = append(report.Issues, "local_path_empty")
	}

	// Check Drive link
	driveLink := clip.DriveLink()
	if driveLink == "" {
		driveLink = clip.DownloadLink()
	}
	var fileID string
	if driveLink != "" {
		report.HasDriveLink = true
		report.DriveLink = driveLink
		fileID, err := urlutil.FileIDFromDriveLink(driveLink)
		if err == nil && fileID != "" {
			report.DriveFileID = fileID
			report.DriveLinkValid = true
		} else {
			report.DriveLinkValid = false
			report.Issues = append(report.Issues, "drive_link_invalid")
		}
	} else {
		report.HasDriveLink = false
		report.Issues = append(report.Issues, "drive_link_missing")
	}

	// Check hash (read-only S1c path)
	if clip.LegacyFileMD5() != "" {
		report.Hash = clip.LegacyFileMD5()
		report.HasHash = true
		if hasLocalFile {
			report.HashVerified = false
		}
	} else {
		if fileID != "" && s.driveUploader != nil {
			md5, err := s.driveUploader.GetFileMD5(ctx, fileID)
			if err == nil && md5 != "" {
				report.Hash = md5
				report.HasHash = true
				report.HashRecovered = false
				report.HashRecoverable = true
				report.HashRecoverableValue = md5
				report.HashInfo = HashInfo{
					Recoverable:   true,
					CandidateHash: md5,
				}
			} else {
				report.HasHash = false
				report.Issues = append(report.Issues, "hash_missing")
			}
		} else {
			report.HasHash = false
			report.Issues = append(report.Issues, "hash_missing")
		}
	}

	if clip.FolderID() != "" {
		report.FolderID = clip.FolderID()
	}
	if clip.FolderPath() != "" {
		report.FolderPath = clip.FolderPath()
	}

	status := "unknown"
	if clip.DriveLink() != "" || clip.DownloadLink() != "" {
		status = "processed"
	} else if clip.LocalPath() != "" {
		status = "downloaded"
	} else {
		status = "pending"
	}
	report.Status = status

	if len(report.Issues) == 0 {
		report.Coherent = true
	} else {
		report.Coherent = false
		report.IssueCount = len(report.Issues)
	}

	return report
}
