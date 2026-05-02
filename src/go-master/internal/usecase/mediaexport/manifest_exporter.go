package mediaexport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"velox/go-master/internal/core/media"
)

type ManifestExporter struct {
	mediaRepo media.Repository
}

func NewManifestExporter(mediaRepo media.Repository) *ManifestExporter {
	return &ManifestExporter{mediaRepo: mediaRepo}
}

type ManifestItem struct {
	ID           string                 `json:"id"`
	Title        string                 `json:"title"`
	SourceKind   string                 `json:"source_kind"`
	MediaType    string                 `json:"media_type"`
	Status       string                 `json:"status"`
	DurationSecs int                    `json:"duration_seconds"`
	ExternalURL  string                 `json:"external_url,omitempty"`
	Files        []ManifestFile         `json:"files"`
	Tags         []string               `json:"tags,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

type ManifestFile struct {
	LocationKind string `json:"location_kind"`
	URI          string `json:"uri"`
	MimeType     string `json:"mime_type,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	DurationSecs int    `json:"duration_seconds,omitempty"`
	FileSize     int64  `json:"file_size_bytes,omitempty"`
}

type Manifest struct {
	WorkspaceID string          `json:"workspace_id"`
	ProjectID   string          `json:"project_id,omitempty"`
	Items       []ManifestItem  `json:"items"`
}

func BuildManifestFromMediaItems(items []*media.Item, filesMap map[string][]*media.File, tagsMap map[string][]string) Manifest {
	manifest := Manifest{}
	if len(items) > 0 {
		manifest.WorkspaceID = items[0].WorkspaceID
		manifest.ProjectID = items[0].ProjectID
	}

	for _, item := range items {
		mi := ManifestItem{
			ID:           item.ID,
			Title:        item.Title,
			SourceKind:   string(item.SourceKind),
			MediaType:    string(item.MediaType),
			Status:       string(item.Status),
			DurationSecs: item.DurationSecs,
			ExternalURL:  item.ExternalURL,
		}

		if filesMap != nil {
			if files, ok := filesMap[item.ID]; ok {
				for _, f := range files {
					mi.Files = append(mi.Files, ManifestFile{
						LocationKind: string(f.LocationKind),
						URI:          f.URI,
						MimeType:     f.MimeType,
						Width:        f.Width,
						Height:       f.Height,
						DurationSecs: f.DurationSecs,
						FileSize:     f.FileSizeBytes,
					})
				}
			}
		}

		if tagsMap != nil {
			if tags, ok := tagsMap[item.ID]; ok {
				mi.Tags = tags
			}
		}

		if item.MetadataJSON != "" {
			json.Unmarshal([]byte(item.MetadataJSON), &mi.Metadata)
		}

		manifest.Items = append(manifest.Items, mi)
	}

	return manifest
}

func (e *ManifestExporter) ExportFolderManifest(ctx context.Context, folderID string, outPath string) error {
	query := media.SearchQuery{
		ProjectID: folderID,
	}
	items, err := e.mediaRepo.ListItems(ctx, query)
	if err != nil {
		return err
	}

	filesMap := make(map[string][]*media.File)
	tagsMap := make(map[string][]string)

	for _, item := range items {
		files, err := e.mediaRepo.ListFiles(ctx, item.ID)
		if err == nil {
			filesMap[item.ID] = files
		}
	}

	manifest := BuildManifestFromMediaItems(items, filesMap, tagsMap)

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(outPath, data, 0644)
}

func (e *ManifestExporter) ExportWorkspaceManifest(ctx context.Context, workspaceID string, outPath string) error {
	query := media.SearchQuery{
		WorkspaceID: workspaceID,
	}
	items, err := e.mediaRepo.ListItems(ctx, query)
	if err != nil {
		return err
	}

	manifest := BuildManifestFromMediaItems(items, nil, nil)

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(outPath, data, 0644)
}
