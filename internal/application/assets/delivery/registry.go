package delivery

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
)

// PathBuilder computes the folder path segments for a given PublishRequest.
// Each segment becomes a nested Drive folder under the destination's root.
// The function MUST sanitise every segment via pathutil.SafeFolderName.
// Returning an empty slice is valid only when RequireSubpath is false.
type PathBuilder func(req PublishRequest) ([]string, error)

// DestinationPolicy defines how a DestinationKey resolves to a Drive path.
// The DestinationRegistry holds exactly one policy per DestinationKey.
type DestinationPolicy struct {
	// RootFolderID returns the Drive folder ID that serves as the root
	// for this destination. Derived from config at construction time.
	RootFolderID string

	// PathBuilder computes nested folder segments under RootFolderID.
	PathBuilder PathBuilder

	// RequireSubpath, when true, rejects uploads that would land directly
	// in the root folder (i.e. when PathBuilder returns an empty slice).
	// This prevents accidental pollution of top-level Drive folders.
	RequireSubpath bool
}

// DestinationRegistry is the single authority that maps a DestinationKey
// to a root folder and a path structure. Adding a new capability means
// adding one policy entry here — no endpoint-level Drive logic is permitted.
type DestinationRegistry struct {
	policies map[DestinationKey]DestinationPolicy
}

// NewDestinationRegistry builds the registry from application config.
// Every DestinationKey has exactly one policy. The root folder IDs are
// captured eagerly (at construction time) so the registry is immutable
// after creation.
func NewDestinationRegistry(cfg *config.Config) *DestinationRegistry {
	return &DestinationRegistry{
		policies: map[DestinationKey]DestinationPolicy{
			DestinationYouTubeClip: {
				RootFolderID:   cfg.Drive.ClipsFolder(),
				PathBuilder:    YouTubeClipPath,
				RequireSubpath: true,
			},
			DestinationArtlist: {
				RootFolderID:   cfg.Drive.ArtlistFolder(),
				PathBuilder:    ArtlistPath,
				RequireSubpath: true,
			},
			DestinationStock: {
				RootFolderID:   cfg.Drive.StockFolder(),
				PathBuilder:    StockPath,
				RequireSubpath: true,
			},
			DestinationImage: {
				RootFolderID:   cfg.Drive.ImagesFolder(),
				PathBuilder:    ImagePath,
				RequireSubpath: true,
			},
			DestinationVoiceover: {
				RootFolderID:   cfg.Drive.VoiceoverFolder(),
				PathBuilder:    VoiceoverPath,
				RequireSubpath: true,
			},
			DestinationBook: {
				RootFolderID:   cfg.Drive.BooksFolder(),
				PathBuilder:    BookPath,
				RequireSubpath: true,
			},
			DestinationScript: {
				RootFolderID:   cfg.Drive.ScriptsFolder(),
				PathBuilder:    ScriptPath,
				RequireSubpath: true,
			},
			DestinationSoundEffect: {
				RootFolderID:   cfg.Drive.SoundEffectsFolder(),
				PathBuilder:    SoundEffectPath,
				RequireSubpath: true,
			},
		},
	}
}

// Has reports whether the registry contains a policy for the given key.
func (r *DestinationRegistry) Has(key DestinationKey) bool {
	_, ok := r.policies[key]
	return ok
}

// Resolve returns the policy for the given key, or an error if the key
// is not registered. Callers MUST check Has() first when iterating over
// a known set of keys (e.g. in tests).
func (r *DestinationRegistry) Resolve(key DestinationKey) (DestinationPolicy, error) {
	p, ok := r.policies[key]
	if !ok {
		return DestinationPolicy{}, fmt.Errorf("delivery: unknown destination key %q", key)
	}
	return p, nil
}

// Keys returns all registered destination keys. Useful for diagnostics
// and completeness tests.
func (r *DestinationRegistry) Keys() []DestinationKey {
	keys := make([]DestinationKey, 0, len(r.policies))
	for k := range r.policies {
		keys = append(keys, k)
	}
	return keys
}

// ── Path builders ──────────────────────────────────────────────────────
//
// Each builder returns []string segments that the Publisher will pass
// to FolderManager.EnsurePath(rootID, segments...). Every segment is
// sanitised via pathutil.SafeFolderName to prevent path traversal,
// OS-unsafe characters, and empty folder names.

// YouTubeClipPath builds the path for YouTube clips:
//
//	clips/{group}/{video_id}
func YouTubeClipPath(req PublishRequest) ([]string, error) {
	group := strings.TrimSpace(req.Group)
	subject := strings.TrimSpace(req.Subject)
	if group == "" {
		return nil, fmt.Errorf("delivery: YouTubeClipPath: group is required")
	}
	if subject == "" {
		return nil, fmt.Errorf("delivery: YouTubeClipPath: subject (video ID) is required")
	}
	return []string{
		pathutil.SafeFolderName(group),
		pathutil.SafeFolderName(subject),
	}, nil
}

// ArtlistPath builds the path for Artlist assets:
//
//	artlist/{term}/{asset_id}
func ArtlistPath(req PublishRequest) ([]string, error) {
	group := strings.TrimSpace(req.Group)
	subject := strings.TrimSpace(req.Subject)
	if group == "" {
		return nil, fmt.Errorf("delivery: ArtlistPath: group (search term) is required")
	}
	if subject == "" {
		return nil, fmt.Errorf("delivery: ArtlistPath: subject (asset ID) is required")
	}
	return []string{
		pathutil.SafeFolderName(group),
		pathutil.SafeFolderName(subject),
	}, nil
}

// StockPath builds the path for stock footage:
//
//	stock/{category}/{provider}
func StockPath(req PublishRequest) ([]string, error) {
	group := strings.TrimSpace(req.Group)
	subject := strings.TrimSpace(req.Subject)
	if group == "" {
		return nil, fmt.Errorf("delivery: StockPath: group (category) is required")
	}
	seg := []string{pathutil.SafeFolderName(group)}
	if subject != "" {
		seg = append(seg, pathutil.SafeFolderName(subject))
	}
	return seg, nil
}

// ImagePath builds the path for generated images:
//
//	images/{style}/{subject}
func ImagePath(req PublishRequest) ([]string, error) {
	style := strings.TrimSpace(req.Style)
	subject := strings.TrimSpace(req.Subject)
	if style == "" {
		return nil, fmt.Errorf("delivery: ImagePath: style is required")
	}
	if subject == "" {
		return nil, fmt.Errorf("delivery: ImagePath: subject is required")
	}
	return []string{
		pathutil.SafeFolderName(style),
		pathutil.SafeFolderName(subject),
	}, nil
}

// VoiceoverPath builds the path for voiceover audio:
//
//	voiceovers/{project}/{language}
func VoiceoverPath(req PublishRequest) ([]string, error) {
	project := strings.TrimSpace(req.ProjectID)
	language := strings.TrimSpace(req.Language)
	if project == "" {
		return nil, fmt.Errorf("delivery: VoiceoverPath: project_id is required")
	}
	if language == "" {
		return nil, fmt.Errorf("delivery: VoiceoverPath: language is required")
	}
	return []string{
		pathutil.SafeFolderName(project),
		pathutil.SafeFolderName(language),
	}, nil
}

// BookPath builds the path for book processing outputs:
//
//	books/{project}
func BookPath(req PublishRequest) ([]string, error) {
	project := strings.TrimSpace(req.ProjectID)
	if project == "" {
		return nil, fmt.Errorf("delivery: BookPath: project_id is required")
	}
	return []string{
		pathutil.SafeFolderName(project),
	}, nil
}

// ScriptPath builds the path for generated scripts/documents:
//
//	scripts/{project}/{language}
func ScriptPath(req PublishRequest) ([]string, error) {
	project := strings.TrimSpace(req.ProjectID)
	language := strings.TrimSpace(req.Language)
	if project == "" {
		return nil, fmt.Errorf("delivery: ScriptPath: project_id is required")
	}
	if language == "" {
		return nil, fmt.Errorf("delivery: ScriptPath: language is required")
	}
	return []string{
		pathutil.SafeFolderName(project),
		pathutil.SafeFolderName(language),
	}, nil
}

// SoundEffectPath builds the path for generated sound effects:
//
//	sound-effects/{category}
func SoundEffectPath(req PublishRequest) ([]string, error) {
	group := strings.TrimSpace(req.Group)
	if group == "" {
		return nil, fmt.Errorf("delivery: SoundEffectPath: group (category) is required")
	}
	return []string{
		pathutil.SafeFolderName(group),
	}, nil
}
