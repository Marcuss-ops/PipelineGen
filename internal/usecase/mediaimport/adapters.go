package mediaimport

import (
	"database/sql"
	"strings"

	"velox/go-master/internal/core/media"
)

// ClipsLegacyRow represents a row from the legacy clips table.
type ClipsLegacyRow struct {
	ClipID     string
	Filename   string
	MediaType  string
	Source     string
	Duration   int
	LocalPath  string
	DriveLink  string
	Tags       string
}

func (r *ClipsLegacyRow) GetClipID() string            { return r.ClipID }
func (r *ClipsLegacyRow) GetFilename() string          { return r.Filename }
func (r *ClipsLegacyRow) GetTitle() string             { return r.Filename }
func (r *ClipsLegacyRow) GetDuration() int            { return r.Duration }
func (r *ClipsLegacyRow) GetTags() string             { return r.Tags }
func (r *ClipsLegacyRow) GetMetadata() string {
	return `{"source":"` + r.Source + `","legacy":true}`
}
func (r *ClipsLegacyRow) GetLocalPath() string        { return r.LocalPath }
func (r *ClipsLegacyRow) GetDriveLink() string       { return r.DriveLink }
func (r *ClipsLegacyRow) GetSourceKind() media.SourceKind {
	switch r.Source {
	case "stock":
		return media.SourceKindStock
	case "artlist":
		return media.SourceKindArtlist
	case "youtube":
		return media.SourceKindYouTube
	default:
		return media.SourceKindManual
	}
}
func (r *ClipsLegacyRow) GetMediaType() media.MediaType {
	return media.MediaType(determineMediaType(r.MediaType))
}

// ClipsLegacyAdapter adapts the legacy clips table.
type ClipsLegacyAdapter struct{}

func (a *ClipsLegacyAdapter) Query() string {
	return `SELECT clip_id, filename, media_type, source, duration, local_path, drive_link, tags FROM clips`
}
func (a *ClipsLegacyAdapter) Scan(rows *sql.Rows) (LegacyRow, error) {
	var r ClipsLegacyRow
	err := rows.Scan(&r.ClipID, &r.Filename, &r.MediaType, &r.Source, &r.Duration, &r.LocalPath, &r.DriveLink, &r.Tags)
	return &r, err
}

// StockLegacyRow represents a row from the legacy stock_clips table.
type StockLegacyRow struct {
	ClipID    string
	Filename  string
	TopicSlug string
	Tags      string
	Duration  int
}

func (r *StockLegacyRow) GetClipID() string            { return r.ClipID }
func (r *StockLegacyRow) GetFilename() string          { return r.Filename }
func (r *StockLegacyRow) GetTitle() string             { return r.Filename }
func (r *StockLegacyRow) GetDuration() int            { return r.Duration }
func (r *StockLegacyRow) GetTags() string             { return r.Tags }
func (r *StockLegacyRow) GetMetadata() string {
	return `{"topic_slug":"` + r.TopicSlug + `","legacy":true}`
}
func (r *StockLegacyRow) GetLocalPath() string        { return "" }
func (r *StockLegacyRow) GetDriveLink() string       { return "" }
func (r *StockLegacyRow) GetSourceKind() media.SourceKind {
	return media.SourceKindStock
}
func (r *StockLegacyRow) GetMediaType() media.MediaType {
	return media.MediaTypeStockClip
}

// StockLegacyAdapter adapts the legacy stock_clips table.
type StockLegacyAdapter struct{}

func (a *StockLegacyAdapter) Query() string {
	return `SELECT clip_id, filename, topic_slug, tags, duration FROM stock_clips`
}
func (a *StockLegacyAdapter) Scan(rows *sql.Rows) (LegacyRow, error) {
	var r StockLegacyRow
	err := rows.Scan(&r.ClipID, &r.Filename, &r.TopicSlug, &r.Tags, &r.Duration)
	return &r, err
}

// ArtlistLegacyRow represents a row from the legacy artlist_clips table.
type ArtlistLegacyRow struct {
	ClipID     string
	Filename   string
	Title      string
	Duration   int
	FolderPath string
	Tags       string
}

func (r *ArtlistLegacyRow) GetClipID() string            { return r.ClipID }
func (r *ArtlistLegacyRow) GetFilename() string          { return r.Filename }
func (r *ArtlistLegacyRow) GetTitle() string             { return r.Title }
func (r *ArtlistLegacyRow) GetDuration() int            { return r.Duration }
func (r *ArtlistLegacyRow) GetTags() string             { return r.Tags }
func (r *ArtlistLegacyRow) GetMetadata() string {
	return `{"folder_path":"` + r.FolderPath + `","legacy":true}`
}
func (r *ArtlistLegacyRow) GetLocalPath() string        { return "" }
func (r *ArtlistLegacyRow) GetDriveLink() string       { return "" }
func (r *ArtlistLegacyRow) GetSourceKind() media.SourceKind {
	return media.SourceKindArtlist
}
func (r *ArtlistLegacyRow) GetMediaType() media.MediaType {
	return media.MediaTypeArtlistClip
}

// ArtlistLegacyAdapter adapts the legacy artlist_clips table.
type ArtlistLegacyAdapter struct{}

func (a *ArtlistLegacyAdapter) Query() string {
	return `SELECT clip_id, filename, title, duration, folder_path, tags FROM artlist_clips`
}
func (a *ArtlistLegacyAdapter) Scan(rows *sql.Rows) (LegacyRow, error) {
	var r ArtlistLegacyRow
	err := rows.Scan(&r.ClipID, &r.Filename, &r.Title, &r.Duration, &r.FolderPath, &r.Tags)
	return &r, err
}

func parseTags(tagsStr string) []string {
	if tagsStr == "" {
		return nil
	}
	parts := strings.Split(tagsStr, ",")
	var tags []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	return tags
}

func determineMediaType(mediaType string) string {
	switch mediaType {
	case "video":
		return string(media.MediaTypeVideo)
	case "audio":
		return string(media.MediaTypeAudio)
	case "image":
		return string(media.MediaTypeImage)
	default:
		return string(media.MediaTypeVideo)
	}
}
