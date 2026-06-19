// Package assetversions — adapter.go implements the canonical
// assets.VersionRepository interface backed by the concrete *Repository.
// The Adapter wraps the concrete type and delegates directly — since
// the concrete Repository already uses domain-compatible types, only
// lightweight field mapping is needed.
package assetversions

import (
	"context"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

// Adapter implements assets.VersionRepository by delegating to the
// concrete SQLite *Repository.
type Adapter struct {
	inner *Repository
}

// NewAdapter wraps a concrete *Repository as an assets.VersionRepository.
func NewAdapter(inner *Repository) *Adapter {
	return &Adapter{inner: inner}
}

// GetCurrent returns the latest Version for the asset, or (nil, nil).
func (a *Adapter) GetCurrent(ctx context.Context, assetID string) (*assets.Version, error) {
	v, err := a.inner.GetCurrent(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return convertVersion(v), nil
}

// List returns all Versions for the asset, newest first.
func (a *Adapter) List(ctx context.Context, assetID string) ([]assets.Version, error) {
	vs, err := a.inner.List(ctx, assetID)
	if err != nil {
		return nil, err
	}
	out := make([]assets.Version, len(vs))
	for i, v := range vs {
		out[i] = *convertVersion(&v)
	}
	return out, nil
}

// Append inserts a new Version row. Uses CreateNext for atomic version
// allocation inside a transaction — no race conditions with concurrent callers.
func (a *Adapter) Append(ctx context.Context, v *assets.Version) error {
	metaJSON := v.MetadataJSON
	// Merge SourceURI into MetadataJSON so it survives round-trips.
	if v.SourceURI != "" {
		metaJSON = mergeSourceURI(metaJSON, v.SourceURI)
	}
	input := VersionInput{
		ContentHash:   "",
		FileHash:      v.FileHash,
		FileSizeBytes: v.FileSizeBytes,
		MimeType:      v.MimeType,
		MetadataJSON:  metaJSON,
		CreatedBy:     "",
	}
	_, err := a.inner.CreateNext(ctx, v.AssetID, input)
	return err
}

// mergeSourceURI merges sourceURI into an existing metadata JSON string.
// Uses encoding/json for safe escaping. If the existing JSON is empty or
// invalid, a fresh object is created.
func mergeSourceURI(existingJSON, sourceURI string) string {
	var m map[string]any
	if existingJSON != "" && existingJSON != "{}" {
		if err := json.Unmarshal([]byte(existingJSON), &m); err != nil {
			m = make(map[string]any)
		}
	} else {
		m = make(map[string]any)
	}
	m["source_uri"] = sourceURI
	b, err := json.Marshal(m)
	if err != nil {
		return existingJSON
	}
	return string(b)
}

// convertVersion maps a concrete Version to the domain assets.Version.
func convertVersion(v *Version) *assets.Version {
	dv := &assets.Version{
		ID:            int64(v.Version),
		AssetID:       v.AssetID,
		VersionNumber: v.Version,
		FileHash:      v.FileHash,
		FileSizeBytes: v.FileSizeBytes,
		MimeType:      v.MimeType,
		MetadataJSON:  v.MetadataJSON,
		CreatedAt:     v.CreatedAt,
	}
	// Extract SourceURI from MetadataJSON when present.
	if v.MetadataJSON != "" && v.MetadataJSON != "{}" {
		var m map[string]any
		if err := json.Unmarshal([]byte(v.MetadataJSON), &m); err == nil {
			if uri, ok := m["source_uri"].(string); ok {
				dv.SourceURI = uri
			}
		}
	}
	return dv
}

// Compile-time check.
var _ assets.VersionRepository = (*Adapter)(nil)
