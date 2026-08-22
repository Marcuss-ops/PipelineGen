package ingest

import (
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assetop"
)

func mediaRecordToAssetRecord(rec *artifacts.MediaRecord) *assetop.AssetRecord {
	if rec == nil {
		return nil
	}
	return &assetop.AssetRecord{
		ID:           rec.ID,
		Name:         rec.Name,
		Filename:     rec.Filename,
		Source:       rec.Source,
		MediaType:    rec.MediaType,
		DriveFileID:  rec.DriveFileID,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		LegacyFileMD5:     rec.LegacyFileMD5,
		LocalPath:    rec.LocalPath,
		Status:       rec.Status,
		Error:        rec.Error,
		Metadata:     rec.Metadata,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
}

func stripKindPrefix(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if idx := strings.IndexByte(id, ':'); idx >= 0 && idx+1 < len(id) {
		return id[idx+1:]
	}
	return id
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
