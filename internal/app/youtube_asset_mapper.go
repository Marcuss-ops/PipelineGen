// Package app — asset mapping helpers extracted from
// assets_register_adapters.go (PR-GODOBJ-8, July 2026).
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// metadataStringSlice reads a []string from Asset.Metadata via the canonical
// asset.MetadataStringSlice accessor. Preserved as a thin wrapper for type
// compatibility (callers pass *asset.Asset, not map[string]any).
func metadataStringSlice(m *asset.Asset, key string) []string {
	return asset.MetadataStringSlice(m.Metadata, key)
}

func fromExistingClip(c *sourcing.ExistingClip) *asset.Asset {
	if c == nil {
		return nil
	}
	out := &asset.Asset{
		ID:            c.ID,
		Name:          c.Name,
		Filename:      c.Filename,
		Source:        asset.Source(c.Source),
		MediaType:     asset.MediaTypeClip,
		LifecycleState: asset.StateActive,
		Category:      c.Category,
		Tags:          append([]string(nil), c.Tags...),
		Duration:      c.Duration,
	}
	out.SetLocalPath(c.LocalPath)
	out.SetDriveLink(c.DriveLink)
	out.SetDriveFileID(c.DriveFileID)
	out.SetFileHash(c.FileHash)
	// Rich metadata fields (RICH-METADATA-QDRANT-VERIFY, July 2026).
	// Stored in Metadata for round-trip through UpsertClipTx →
	// media_assets.metadata_json → Qdrant semantic search.
	// Nil-safe: SetMetadataString initializes Metadata if nil.
	if c.Summary != "" {
		out.SetMetadataString("clip_summary", c.Summary)
	}
	if len(c.Topics) > 0 {
		if out.Metadata == nil {
			out.Metadata = make(map[string]any)
		}
		out.Metadata["topics"] = append([]string(nil), c.Topics...)
	}
	if len(c.Speakers) > 0 {
		if out.Metadata == nil {
			out.Metadata = make(map[string]any)
		}
		out.Metadata["speakers"] = append([]string(nil), c.Speakers...)
	}
	if len(c.MentionedPeople) > 0 {
		if out.Metadata == nil {
			out.Metadata = make(map[string]any)
		}
		out.Metadata["mentioned_people"] = append([]string(nil), c.MentionedPeople...)
	}
	if c.Hook != "" {
		out.SetMetadataString("hook", c.Hook)
	}
	return out
}

func toExistingClip(c *asset.Asset) *sourcing.ExistingClip {
	if c == nil {
		return nil
	}
	return &sourcing.ExistingClip{
		ID:          c.ID,
		Name:        c.Name,
		Filename:    c.Filename,
		Duration:    c.Duration,
		Source:      string(c.Source),
		Category:    c.Category,
		Tags:        append([]string(nil), c.Tags...),
		LocalPath:   c.LocalPath(),
		DriveLink:   c.DriveLink(),
		DriveFileID: c.DriveFileID(),
		FileHash:    c.FileHash(),
		// Rich metadata fields (RICH-METADATA-QDRANT-VERIFY, July 2026)
		Summary:         c.GetMetadataString("clip_summary"),
		Topics:          metadataStringSlice(c, "topics"),
		Speakers:        metadataStringSlice(c, "speakers"),
		MentionedPeople: metadataStringSlice(c, "mentioned_people"),
		Hook:            c.GetMetadataString("hook"),
	}
}
