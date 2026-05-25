package storage

import (
	"path/filepath"
	"strings"
)

// MediaRoot is the base for ALL local media paths.
// Typically "data/media" or an absolute path.
type MediaRoot string

func (m MediaRoot) String() string { return string(m) }

// DriveRoot is the ID of the single Drive root folder for ALL media.
type DriveRoot string

func (d DriveRoot) String() string { return string(d) }

// Resolver resolves asset destination paths deterministically.
// Call Resolve(AssetDestinationRequest) to get where an asset should be stored.
type Resolver struct {
	mediaRoot MediaRoot
	driveRoot DriveRoot
}

// NewResolver creates a resolver with the given roots.
func NewResolver(mediaRoot MediaRoot, driveRoot DriveRoot) *Resolver {
	return &Resolver{mediaRoot: mediaRoot, driveRoot: driveRoot}
}

// MediaRootPath returns the absolute local media root path.
func (r *Resolver) MediaRootPath() string {
	return r.mediaRoot.String()
}

// DriveRootID returns the Drive root folder ID.
func (r *Resolver) DriveRootID() string {
	return r.driveRoot.String()
}

// Resolve computes the deterministic local and Drive paths for an asset.
func (r *Resolver) Resolve(req AssetDestinationRequest) (*AssetDestination, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	rel := r.resolveRelative(req)
	dest := &AssetDestination{
		RelativePath:    rel,
		LocalPath:       filepath.Join(r.mediaRoot.String(), rel),
		DriveFolderPath: r.resolveDriveFolder(req),
		DriveFileName:   r.resolveDriveFilename(req),
	}

	return dest, nil
}

// resolveRelative builds the relative path under data/media/.
//
// Rules:
//
//	clip + youtube  -> clips/youtube/<group>/<hash><ext>
//	clip + artlist  -> clips/artlist/<group>/<hash><ext>
//	clip + stock    -> clips/stock/<provider>/<group>/<hash><ext>
//	image + any     -> images/<style>/<subject>/<hash><ext>
//	image_video     -> image_videos/fullscreen/<style>/<subject>/<hash><ext>
//	voiceover       -> voiceovers/<provider>/<voice>/<hash><ext>
//	export_video    -> exports/videos/<project>/<date>/<hash><ext>
func (r *Resolver) resolveRelative(req AssetDestinationRequest) string {
	ext := ensureDot(req.Ext)

	switch req.MediaType {
	case MediaTypeClip:
		return r.clipRelative(req, ext)
	case MediaTypeImage:
		return r.imageRelative(req, ext)
	case MediaTypeImageVideo:
		return r.imageVideoRelative(req, ext)
	case MediaTypeVoiceover:
		return r.voiceoverRelative(req, ext)
	case MediaTypeExportVideo:
		return r.exportRelative(req, ext)
	default:
		// Fallback: source/type based
		slug := slugify(req.Subject)
		if slug == "" {
			slug = req.Hash
		}
		return filepath.Join(req.Source, slug, req.Hash+ext)
	}
}

func (r *Resolver) clipRelative(req AssetDestinationRequest, ext string) string {
	group := nonEmpty(req.Group, "general")
	switch req.Source {
	case SourceYouTube:
		return filepath.Join("clips", "youtube", group, req.Hash+ext)
	case SourceArtlist:
		return filepath.Join("clips", "artlist", group, req.Hash+ext)
	case SourceStock:
		provider := nonEmpty(req.Provider, "unknown")
		return filepath.Join("clips", "stock", provider, group, req.Hash+ext)
	default:
		return filepath.Join("clips", req.Source, group, req.Hash+ext)
	}
}

func (r *Resolver) imageRelative(req AssetDestinationRequest, ext string) string {
	style := nonEmpty(req.Style, "general")
	subject := slugify(nonEmpty(req.Subject, req.Hash))
	if req.Source == "" || req.Source == SourceImage {
		return filepath.Join("images", "generated", style, subject, req.Hash+ext)
	}
	return filepath.Join("images", "downloaded", req.Source, subject, req.Hash+ext)
}

func (r *Resolver) imageVideoRelative(req AssetDestinationRequest, ext string) string {
	style := nonEmpty(req.Style, "general")
	subject := slugify(nonEmpty(req.Subject, req.Hash))
	return filepath.Join("image_videos", "fullscreen", style, subject, req.Hash+ext)
}

func (r *Resolver) voiceoverRelative(req AssetDestinationRequest, ext string) string {
	provider := nonEmpty(req.Provider, "default")
	voice := nonEmpty(req.Voice, "default")
	return filepath.Join("voiceovers", provider, voice, req.Hash+ext)
}

func (r *Resolver) exportRelative(req AssetDestinationRequest, ext string) string {
	project := slugify(nonEmpty(req.Project, "unknown"))
	date := nonEmpty(req.Date, "unknown")
	return filepath.Join("exports", "videos", project, date, req.Hash+ext)
}

// resolveDriveFolder builds the Drive folder path (relative to media_root_folder).
func (r *Resolver) resolveDriveFolder(req AssetDestinationRequest) string {
	switch req.MediaType {
	case MediaTypeClip:
		group := nonEmpty(req.Group, "general")
		switch req.Source {
		case SourceYouTube:
			return filepath.Join("clips", "youtube", group)
		case SourceArtlist:
			return filepath.Join("clips", "artlist", group)
		case SourceStock:
			provider := nonEmpty(req.Provider, "unknown")
			return filepath.Join("clips", "stock", provider, group)
		default:
			return filepath.Join("clips", req.Source, group)
		}
	case MediaTypeImage:
		style := nonEmpty(req.Style, "general")
		subject := slugify(nonEmpty(req.Subject, req.Hash))
		return filepath.Join("images", style, subject)
	case MediaTypeImageVideo:
		style := nonEmpty(req.Style, "general")
		subject := slugify(nonEmpty(req.Subject, req.Hash))
		return filepath.Join("image_videos", "fullscreen", style, subject)
	case MediaTypeVoiceover:
		provider := nonEmpty(req.Provider, "default")
		voice := nonEmpty(req.Voice, "default")
		return filepath.Join("voiceovers", provider, voice)
	case MediaTypeExportVideo:
		project := slugify(nonEmpty(req.Project, "unknown"))
		date := nonEmpty(req.Date, "unknown")
		return filepath.Join("exports", "videos", project, date)
	default:
		return filepath.Join("other", req.MediaType, req.Source)
	}
}

// resolveDriveFilename builds the filename for Drive.
func (r *Resolver) resolveDriveFilename(req AssetDestinationRequest) string {
	ext := ensureDot(req.Ext)
	switch req.MediaType {
	case MediaTypeImage:
		return req.Hash + ext
	case MediaTypeImageVideo:
		return req.Hash + ext
	default:
		if req.Subject != "" {
			return slugify(req.Subject) + ext
		}
		return req.Hash + ext
	}
}

// --- helpers ---

func ensureDot(ext string) string {
	ext = strings.TrimSpace(ext)
	if ext == "" {
		return ".bin"
	}
	if !strings.HasPrefix(ext, ".") {
		return "." + ext
	}
	return ext
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}

func slugify(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "untitled"
	}
	s = strings.ToLower(s)
	parts := strings.Fields(s)
	return strings.Join(parts, "-")
}

// EnsureDriveFolderPath creates the full Drive folder hierarchy under root.
// Returns the final folder ID.
// This is a placeholder signature — the actual Drive SDK call is injected.
type DriveFolderCreator func(ctx interface{}, parentID string, pathSegments ...string) (string, error)

// DefaultResolver returns a resolver with conventional defaults.
// mediaRoot = "<dataDir>/media"
// driveRoot = <configured root or first available legacy root>
func DefaultResolver(dataDir, driveRootID string) *Resolver {
	return NewResolver(
		MediaRoot(filepath.Join(dataDir, "media")),
		DriveRoot(driveRootID),
	)
}
