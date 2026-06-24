package clips

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// DownloadUseCase resolves where a clip's video file lives and returns
// the location for the handler to stream.
type DownloadUseCase struct {
	assetRepo     asset.Repository
	voiceoverRepo VoiceoverRepositoryPort
}

// NewDownloadUseCase constructs the use case.
// PG-005 (June 2026): voiceoverRepo parameter is now the typed
// VoiceoverRepositoryPort (defined in this package's ports.go)
// instead of *assets.VoiceoversRepository, so the use case has
// zero infrastructure-layer reach-through.
func NewDownloadUseCase(repo asset.Repository, voiceoverRepo VoiceoverRepositoryPort) *DownloadUseCase {
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

	// Handle Voiceover source via the typed port
	if strings.ToLower(source) == "voiceover" && uc.voiceoverRepo != nil {
		rec, err := uc.voiceoverRepo.GetByID(ctx, clipID)
		if err != nil {
			return nil, fmt.Errorf("voiceover not found: %w", err)
		}
		if rec == nil {
			return nil, fmt.Errorf("voiceover not found")
		}
		return &DownloadResult{
			Source:    DownloadSourceLocal,
			LocalPath: rec.LocalPath,
		}, nil
	}

	if uc.assetRepo == nil {
		return nil, fmt.Errorf("asset repository not configured")
	}

	assetRec, err := uc.assetRepo.Get(ctx, clipID)
	if err != nil {
		return nil, fmt.Errorf("clip not found: %w", err)
	}
	if assetRec == nil {
		return nil, fmt.Errorf("clip not found")
	}
	clip = assetRec

	// Default: try local path
	res := &DownloadResult{
		Clip:      clip,
		Source:    DownloadSourceLocal,
		LocalPath: clip.LocalPath(),
		DriveID:   clip.DriveFileID(),
	}

	// Resolve drive link → drive ID if needed
	if res.DriveID == "" && clip.DriveLink() != "" {
		if id, _ := urlutil.FileIDFromDriveLink(clip.DriveLink()); id != "" {
			res.DriveID = id
		}
	}

	// Validate that local file is not a text/json stub
	if res.LocalPath != "" {
		ext := strings.ToLower(strings.TrimSuffix(res.LocalPath, extractExt(res.LocalPath)))
		if blockedExtensions[ext] {
			res.LocalPath = ""
		}
	}

	// Prefer local if it exists
	if res.LocalPath == "" {
		res.Source = DownloadSourceDrive
	} else if res.DriveID == "" && clip.DriveLink() != "" {
		// Local exists, but also have a Drive link → Drive (cross-source fallback)
		res.Source = DownloadSourceDrive
	}

	if res.Source == DownloadSourceLocal && res.LocalPath == "" && res.DriveID == "" {
		res.Source = DownloadSourceNone
	}

	return res, nil
}

func extractExt(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			break
		}
		if path[i] == '.' {
			return path[i:]
		}
	}
	return ""
}
