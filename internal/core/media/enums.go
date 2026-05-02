package media

type SourceKind string

const (
	SourceKindArtlist SourceKind = "artlist"
	SourceKindStock   SourceKind = "stock"
	SourceKindYouTube SourceKind = "youtube"
	SourceKindDrive   SourceKind = "drive"
	SourceKindManual  SourceKind = "manual"
)

func (s SourceKind) Valid() bool {
	switch s {
	case SourceKindArtlist, SourceKindStock, SourceKindYouTube, SourceKindDrive, SourceKindManual:
		return true
	default:
		return false
	}
}

type MediaType string

const (
	MediaTypeVideo       MediaType = "video"
	MediaTypeAudio       MediaType = "audio"
	MediaTypeImage       MediaType = "image"
	MediaTypeYouTubeClip MediaType = "youtube_clip"
	MediaTypeStockClip   MediaType = "stock_clip"
	MediaTypeArtlistClip MediaType = "artlist_clip"
)

func (m MediaType) Valid() bool {
	switch m {
	case MediaTypeVideo, MediaTypeAudio, MediaTypeImage, MediaTypeYouTubeClip, MediaTypeStockClip, MediaTypeArtlistClip:
		return true
	default:
		return false
	}
}

type MediaStatus string

const (
	MediaStatusDiscovered MediaStatus = "discovered"
	MediaStatusDownloaded MediaStatus = "downloaded"
	MediaStatusNormalized MediaStatus = "normalized"
	MediaStatusUploaded   MediaStatus = "uploaded"
	MediaStatusReady      MediaStatus = "ready"
	MediaStatusFailed     MediaStatus = "failed"
	MediaStatusArchived   MediaStatus = "archived"
)

func (s MediaStatus) Valid() bool {
	switch s {
	case MediaStatusDiscovered, MediaStatusDownloaded, MediaStatusNormalized, MediaStatusUploaded, MediaStatusReady, MediaStatusFailed, MediaStatusArchived:
		return true
	default:
		return false
	}
}

type LocationKind string

const (
	LocationKindURL      LocationKind = "url"
	LocationKindLocal    LocationKind = "local"
	LocationKindDrive    LocationKind = "drive"
	LocationKindS3       LocationKind = "s3"
	LocationKindOther    LocationKind = "other"
)

func (l LocationKind) Valid() bool {
	switch l {
	case LocationKindURL, LocationKindLocal, LocationKindDrive, LocationKindS3, LocationKindOther:
		return true
	default:
		return false
	}
}

type UsageKind string

const (
	UsageKindScriptAsset  UsageKind = "script_asset"
	UsageKindTimelineClip UsageKind = "timeline_clip"
	UsageKindThumbnail    UsageKind = "thumbnail"
	UsageKindBackground   UsageKind = "background"
)

func (u UsageKind) Valid() bool {
	switch u {
	case UsageKindScriptAsset, UsageKindTimelineClip, UsageKindThumbnail, UsageKindBackground:
		return true
	default:
		return false
	}
}
