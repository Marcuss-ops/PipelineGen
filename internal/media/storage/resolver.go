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
//	clip + youtube  -> youtube/<group>/<gen_id>/<hash><ext>
//	clip + artlist  -> artlist/<group>/<gen_id>/<hash><ext>
//	clip + stock    -> stock/<provider>/<group>/<gen_id>/<hash><ext>
//	image + any     -> images/<style>/<sub_style>/<gen_id>/<hash><ext>
//	image_video     -> image_videos/<style>/<sub_style>/<gen_id>/<hash><ext>
//	voiceover       -> voiceovers/<provider>/<voice>/<hash><ext>
//	export_video    -> exports/videos/<project>/<date>/<hash><ext>
func (r *Resolver) resolveRelative(req AssetDestinationRequest) string {
	ext := ensureDot(req.Ext)
	genID := nonEmpty(req.GenerationID, req.Hash)

	switch req.MediaType {
	case MediaTypeClip:
		return r.clipRelative(req, ext, genID)
	case MediaTypeImage:
		return r.imageRelative(req, ext, genID)
	case MediaTypeImageVideo:
		return r.imageVideoRelative(req, ext, genID)
	case MediaTypeVoiceover:
		return r.voiceoverRelative(req, ext)
	case MediaTypeExportVideo:
		return r.exportRelative(req, ext)
	default:
		// Fallback: source/type based
		style := nonEmpty(req.Style, "general")
		return filepath.Join(req.Source, style, genID, req.Hash+ext)
	}
}

func (r *Resolver) clipRelative(req AssetDestinationRequest, ext, genID string) string {
	group := slugify(nonEmpty(req.Group, "general"))
	switch req.Source {
	case SourceYouTube:
		return filepath.Join("youtube", group, genID, req.Hash+ext)
	case SourceArtlist:
		return filepath.Join("artlist", group, genID, req.Hash+ext)
	case SourceStock:
		provider := slugify(nonEmpty(req.Provider, "unknown"))
		return filepath.Join("stock", provider, group, genID, req.Hash+ext)
	default:
		return filepath.Join(req.Source, group, genID, req.Hash+ext)
	}
}

func (r *Resolver) imageRelative(req AssetDestinationRequest, ext, genID string) string {
	style := slugify(nonEmpty(req.Style, "general"))
	subStyle := slugify(nonEmpty(req.SubStyle, "standard"))
	if req.Source == "" || req.Source == SourceImage {
		return filepath.Join("images", "generated", style, subStyle, genID, req.Hash+ext)
	}
	return filepath.Join("images", "downloaded", req.Source, style, subStyle, genID, req.Hash+ext)
}

func (r *Resolver) imageVideoRelative(req AssetDestinationRequest, ext, genID string) string {
	style := slugify(nonEmpty(req.Style, "general"))
	subStyle := slugify(nonEmpty(req.SubStyle, "standard"))
	return filepath.Join("image_videos", style, subStyle, genID, req.Hash+ext)
}

func (r *Resolver) voiceoverRelative(req AssetDestinationRequest, ext string) string {
	provider := slugify(nonEmpty(req.Provider, "default"))
	voice := slugify(nonEmpty(req.Voice, "default"))
	return filepath.Join("voiceovers", provider, voice, req.Hash+ext)
}

func (r *Resolver) exportRelative(req AssetDestinationRequest, ext string) string {
	project := slugify(nonEmpty(req.Project, "unknown"))
	date := nonEmpty(req.Date, "unknown")
	return filepath.Join("exports", "videos", project, date, req.Hash+ext)
}

// resolveDriveFolder builds the Drive folder path (relative to media_root_folder).
func (r *Resolver) resolveDriveFolder(req AssetDestinationRequest) string {
	genID := nonEmpty(req.GenerationID, req.Hash)

	switch req.MediaType {
	case MediaTypeClip:
		return r.clipDriveFolder(req, genID)
	case MediaTypeImage:
		return r.imageDriveFolder(req, genID)
	case MediaTypeImageVideo:
		return r.videoDriveFolder(req, genID)
	case MediaTypeVoiceover:
		provider := slugify(nonEmpty(req.Provider, "default"))
		voice := slugify(nonEmpty(req.Voice, "default"))
		return filepath.Join("voiceovers", provider, voice)
	case MediaTypeExportVideo:
		project := slugify(nonEmpty(req.Project, "unknown"))
		date := nonEmpty(req.Date, "unknown")
		return filepath.Join("exports", "videos", project, date)
	default:
		return filepath.Join("other", req.MediaType, req.Source, genID)
	}
}

func (r *Resolver) clipDriveFolder(req AssetDestinationRequest, genID string) string {
	group := slugify(nonEmpty(req.Group, "general"))
	switch req.Source {
	case SourceYouTube:
		return filepath.Join("youtube", group, genID)
	case SourceArtlist:
		return filepath.Join("artlist", group, genID)
	case SourceStock:
		provider := slugify(nonEmpty(req.Provider, "unknown"))
		return filepath.Join("stock", provider, group, genID)
	default:
		return filepath.Join(req.Source, group, genID)
	}
}

func (r *Resolver) imageDriveFolder(req AssetDestinationRequest, genID string) string {
	style := slugify(nonEmpty(req.Style, "general"))
	if req.SubStyle != "" && req.SubStyle != "standard" {
		return filepath.Join("images", style, slugify(req.SubStyle), genID)
	}
	return filepath.Join("images", style, genID)
}

// videoDriveFolder: Drive path = <style>/<subject> (no image_videos prefix).
func (r *Resolver) videoDriveFolder(req AssetDestinationRequest, genID string) string {
	style := slugify(nonEmpty(req.Style, "general"))
	subject := slugify(nonEmpty(req.Subject, genID))
	return filepath.Join(style, subject)
}

// resolveDriveFilename builds the filename for Drive.
func (r *Resolver) resolveDriveFilename(req AssetDestinationRequest) string {
	ext := ensureDot(req.Ext)
	switch req.MediaType {
	case MediaTypeImage:
		if ext == ".json" {
			return "metadata.json"
		}
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
