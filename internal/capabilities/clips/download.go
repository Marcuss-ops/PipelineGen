package clips

import (
	"context"
	"fmt"
	"strings"

	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// DownloadUseCase resolves where a clip's video file lives and returns
// the location for the handler to stream.
type DownloadUseCase struct {
	assetRepo     asset.Repository
	voiceoverRepo *assets.VoiceoversRepository
}

// NewDownloadUseCase constructs the use case.
func NewDownloadUseCase(repo asset.Repository, voiceoverRepo *assets.VoiceoversRepository) *DownloadUseCase {
	return &DownloadUseCase{assetRepo: repo, voiceoverRepo: voiceoverRepo}
}

// DownloadSource indicates where the video content is located.
type DownloadSource int

const (
	DownloadSourceLocal DownloadSource = iota
	DownloadSourceDrive
	DownloadSourceNone
)

// DownloadResult contains the resolved location for streaming.
type DownloadResult struct {
	Clip      *asset.Asset
	Source    DownloadSource
	LocalPath string // filled when Source == DownloadSourceLocal
	DriveID   string // filled when Source == DownloadSourceDrive
}

// Non-media extensions that should not be proxied.
var blockedExtensions = map[string]bool{
	".txt":  true,
	".json": true,
	".md":   true,
}

// Resolve finds where the clip's video content is stored.
func (uc *DownloadUseCase) Resolve(ctx context.Context, source, clipID string) (*DownloadResult, error) {
	var clip *asset.Asset

	// Handle Voiceover source
	if strings.ToLower(source) == "voiceover" && uc.voiceoverRepo != nil {
		rec, err := uc.voiceoverRepo.GetByID(ctx, clipID)
		if err != nil {
			return nil, fmt.Errorf("voiceover not found: %s", clipID)
		}
		clip = assets.VoiceoverRecordToAsset(rec)
	} else {
		if uc.assetRepo == nil {
			return nil, fmt.Errorf("asset repository not available")
		}
		var err error
		clip, err = uc.assetRepo.Get(ctx, clipID)
		if err != nil {
			return nil, fmt.Errorf("clip not found: %s", clipID)
		}
		if clip == nil {
			return nil, fmt.Errorf("clip not found: %s", clipID)
		}
	}

	// 1. Try local file
	if clip.LocalPath() != "" {
		ext := strings.ToLower(clip.LocalPath()[strings.LastIndex(clip.LocalPath(), "."):])
		if !blockedExtensions[ext] {
			return &DownloadResult{
				Clip:      clip,
				Source:    DownloadSourceLocal,
				LocalPath: clip.LocalPath(),
			}, nil
		}
	}

	// 2. Try Drive
	driveID := clip.DriveFileID()
	if driveID == "" {
		driveID = driveutil.FileIDFromLink(clip.DriveLink())
	}
	if driveID == "" {
		driveID = driveutil.FileIDFromLink(clip.DownloadLink())
	}

	if driveID != "" {
		return &DownloadResult{
			Clip:    clip,
			Source:  DownloadSourceDrive,
			DriveID: driveID,
		}, nil
	}

	return &DownloadResult{
		Clip:   clip,
		Source: DownloadSourceNone,
	}, nil
}
