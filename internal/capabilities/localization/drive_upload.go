package localization

// drive_upload.go owns the Drive upload step of the localization fan-out: it
// takes a rendered LocalizedClipArtifact (status RENDERED, local bytes
// verified) and uploads it to Drive, returning the SAME artifact certified
// with DriveFileID + DriveLink and status UPLOADED.
//
// godlike/06 SSOT (one canonical owner per fact): the upload returns a
// certified LocalizedClipArtifact — never a bare path or a reconstructed
// Drive link. The concrete upload mechanics live behind a narrow port (wired
// to the canonical delivery.Publisher at the composition root); this package
// never imports the Drive/SQLite stack.
//
// godlike/07 fail-closed: a not-yet-rendered artifact, a missing local path,
// or a missing content hash is rejected BEFORE any upload — Drive never
// receives an empty or uncertified file.

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// DriveUploadInput carries the certified local facts + destination for one
// upload. ContentHash is the SHA-256 of the local bytes (the renderer's
// verified digest); the concrete adapter derives the Drive idempotency key
// from it so a re-run reuses the same Drive file.
type DriveUploadInput struct {
	LocalPath   string
	Filename    string
	FolderID    string
	ContentHash string
	Language    string
	SizeBytes   int64
}

type SubtitleUploadInput struct {
	LocalPath   string
	Filename    string
	FolderID    string
	ContentHash string
	Language    string
	SizeBytes   int64
}

// DriveUploadResult is the certified upload outcome.
type DriveUploadResult struct {
	FileID string
	Link   string
}

type SubtitleDriveUploader interface {
	UploadSubtitle(ctx context.Context, in SubtitleUploadInput) (*DriveUploadResult, error)
}

// DriveUploader uploads a rendered localized clip to Drive. The concrete
// adapter wraps the canonical delivery.Publisher (DestinationClipMetadata,
// ConflictSkip, content-hash idempotency); the capability never chooses the
// destination key or the conflict policy itself.
type DriveUploader interface {
	Upload(ctx context.Context, in DriveUploadInput) (*DriveUploadResult, error)
}

// DrivePublisher is the canonical Drive upload step. It is immutable after
// construction and safe for concurrent Publish calls.
type DrivePublisher struct {
	uploader DriveUploader
}

// NewDrivePublisher builds the upload step. Fail-closed: a nil uploader is
// rejected at construction (an uploader that cannot reach Drive can never
// certify an artifact as UPLOADED).
func NewDrivePublisher(uploader DriveUploader) (*DrivePublisher, error) {
	if uploader == nil {
		return nil, fmt.Errorf("localization.NewDrivePublisher: drive uploader is required")
	}
	return &DrivePublisher{uploader: uploader}, nil
}

// Publish uploads a rendered localized clip and returns a certified artifact:
// the input's facts are preserved and DriveFileID / DriveLink are added, with
// Status advanced to LocalizedClipUploaded. The input is never mutated — the
// certified artifact is a copy.
func (p *DrivePublisher) Publish(ctx context.Context, artifact LocalizedClipArtifact, folderID string) (LocalizedClipArtifact, error) {
	if p == nil || p.uploader == nil {
		return artifact, fmt.Errorf("localization: drive publisher is not initialized")
	}
	if artifact.Status != LocalizedClipRendered {
		return artifact, fmt.Errorf("localization: drive upload: artifact status %q is not RENDERED", artifact.Status)
	}
	if strings.TrimSpace(artifact.LocalPath) == "" {
		return artifact, fmt.Errorf("localization: drive upload: artifact local_path is required")
	}
	if strings.TrimSpace(artifact.SHA256) == "" {
		return artifact, fmt.Errorf("localization: drive upload: artifact sha256 is required")
	}
	if strings.TrimSpace(folderID) == "" {
		return artifact, fmt.Errorf("localization: drive upload: drive folder id is required")
	}

	result, err := p.uploader.Upload(ctx, DriveUploadInput{
		LocalPath:   artifact.LocalPath,
		Filename:    localizedClipFilename(artifact),
		FolderID:    folderID,
		ContentHash: artifact.SHA256,
		Language:    artifact.Language,
		SizeBytes:   artifact.SizeBytes,
	})
	if err != nil {
		return artifact, fmt.Errorf("localization: drive upload: %w", err)
	}
	if result == nil || result.FileID == "" {
		return artifact, fmt.Errorf("localization: drive upload: uploader returned an empty Drive result")
	}

	out := artifact
	out.DriveFileID = result.FileID
	out.DriveLink = result.Link
	out.Status = LocalizedClipUploaded
	subtitleFolderID := artifact.SubtitleFolderID
	if strings.TrimSpace(artifact.SubtitlePath) != "" && strings.TrimSpace(subtitleFolderID) != "" {
		if su, ok := p.uploader.(SubtitleDriveUploader); ok {
			var subtitleSize int64
			if info, statErr := os.Stat(artifact.SubtitlePath); statErr == nil {
				subtitleSize = info.Size()
			}
			subtitleHash := artifact.SubtitleSHA256
			if subtitleHash == "" {
				subtitleHash = artifact.SHA256
			}
			sub, err := su.UploadSubtitle(ctx, SubtitleUploadInput{
				LocalPath:   artifact.SubtitlePath,
				Filename:    artifact.ClipID + "." + artifact.Language + ".ass",
				FolderID:    subtitleFolderID,
				ContentHash: subtitleHash,
				Language:    artifact.Language,
				SizeBytes:   subtitleSize,
			})
			if err != nil {
				return artifact, fmt.Errorf("localization: drive upload subtitle: %w", err)
			}
			if sub == nil || sub.FileID == "" {
				return artifact, fmt.Errorf("localization: drive upload subtitle: empty result")
			}
		}
	}
	return out, nil
}

// localizedClipFilename derives the deterministic Drive filename from the
// artifact identity + language: "<clip_id>.<language>.mp4".
func localizedClipFilename(artifact LocalizedClipArtifact) string {
	// The rendered bytes, not the source clip name, identify a regeneration.
	// Keeping the digest in the filename prevents ConflictSkip from returning
	// an older render while preserving idempotency for identical bytes.
	digest := strings.TrimSpace(artifact.SHA256)
	if len(digest) > 12 {
		digest = digest[:12]
	}
	name := artifact.ClipID + "." + artifact.Language
	if digest != "" {
		name += "." + digest
	}
	name += ".mp4"
	return name
}
