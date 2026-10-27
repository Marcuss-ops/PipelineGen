package ingest

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assetop"
)

func mediaRecordToAssetRecord(rec *artifacts.MediaRecord) *assetop.AssetRecord {
	if rec == nil {
		return nil
	}
	return &assetop.AssetRecord{
		ID:            rec.ID,
		Name:          rec.Name,
		Filename:      rec.Filename,
		Source:        rec.Source,
		MediaType:     rec.MediaType,
		DriveFileID:   rec.DriveFileID,
		DriveLink:     rec.DriveLink,
		DownloadLink:  rec.DownloadLink,
		ContentSHA256: rec.ContentSHA256,
		LocalPath:     rec.LocalPath,
		Status:        rec.Status,
		Error:         rec.Error,
		Metadata:      rec.Metadata,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
