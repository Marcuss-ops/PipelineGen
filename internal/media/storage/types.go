package storage

import "fmt"

// Source constants for asset origin.
const (
	SourceYouTube   = "youtube"
	SourceArtlist   = "artlist"
	SourceStock     = "stock"
	SourceImage     = "image"
	SourceVoiceover = "voiceover"
	SourceExport    = "export"
)

// MediaType constants.
const (
	MediaTypeClip        = "clip"
	MediaTypeImage       = "image"
	MediaTypeImageVideo  = "image_video"
	MediaTypeVoiceover   = "voiceover"
	MediaTypeExportVideo = "export_video"
)

// AssetDestinationRequest specifies WHAT to store.
type AssetDestinationRequest struct {
	Source       string // youtube, artlist, stock, image, voiceover
	MediaType    string // clip, image, image_video, voiceover, export_video
	Group        string // term/tag/query group
	Subject      string // subject name or slug
	Style        string // style qualifier (for images)
	SubStyle     string // sub-style qualifier (for images/videos)
	GenerationID string // unique generation ID (UUID or hash)
	Provider     string // sub-source (pexels, pixabay, flux-1-dev, etc.)
	Voice        string // voice name (for voiceovers)
	Hash         string // content hash
	Ext          string // file extension (with or without dot)
	Project      string // project slug (for exports)
	Date         string // date string (for exports)
}

// AssetDestination says WHERE to store it.
type AssetDestination struct {
	LocalPath       string // absolute filesystem path
	RelativePath    string // relative to data/media/
	DriveFolderPath string // path segments under media_root_folder (e.g. "clips/youtube/group")
	DriveFolderID   string // resolved Drive folder ID (set after Drive mkdir)
	DriveFileName   string // filename on Drive
}

// Validate checks that the request has enough info to resolve a path.
func (r AssetDestinationRequest) Validate() error {
	if r.Source == "" {
		return fmt.Errorf("source is required")
	}
	if r.MediaType == "" {
		return fmt.Errorf("media_type is required")
	}
	if r.Hash == "" && r.MediaType != MediaTypeExportVideo {
		return fmt.Errorf("hash is required for %s", r.MediaType)
	}
	return nil
}
