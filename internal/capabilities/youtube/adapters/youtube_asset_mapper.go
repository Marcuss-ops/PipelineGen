// Package app — asset mapping helpers extracted from
// assets_register_adapters.go (PR-GODOBJ-8, July 2026).
package adapters

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// firstNonEmpty returns the first non-blank value, mirroring the composition
// root helper of the same name (which this package can no longer see).
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func fromExistingClip(c *sourcing.ExistingClip) *asset.Asset {
	if c == nil {
		return nil
	}
	out := &asset.Asset{
		ID:             c.ID,
		Name:           c.Name,
		Filename:       c.Filename,
		Source:         asset.Source(c.Source),
		MediaType:      asset.MediaTypeClip,
		LifecycleState: asset.StateActive,
		Category:       c.Category,
		SourceURL:      c.SourceURL,
		Tags:           append([]string(nil), c.Tags...),
		Duration:       c.Duration,
	}
	out.SetLocalPath(c.LocalPath)
	out.SetDriveLink(c.DriveLink)
	out.SetDriveFileID(c.DriveFileID)
	out.SetLegacyFileMD5(c.LegacyFileMD5)
	out.SetFolderID(c.DriveFolderID)
	out.SetFolderPath(c.DrivePath)
	// Rich metadata fields (RICH-METADATA-QDRANT-VERIFY, July 2026).
	// Stored in Metadata for round-trip through UpsertClipTx →
	// media_assets.metadata_json → Qdrant semantic search.
	// Nil-safe: SetMetadataString initializes Metadata if nil.
	if c.Summary != "" {
		out.SetClipSummary(c.Summary)
	}
	if len(c.Topics) > 0 {
		out.SetTopics(append([]string(nil), c.Topics...))
	}
	if len(c.Speakers) > 0 {
		out.SetSpeakers(append([]string(nil), c.Speakers...))
	}
	if len(c.MentionedPeople) > 0 {
		out.SetMentionedPeople(append([]string(nil), c.MentionedPeople...))
	}
	if c.Hook != "" {
		out.SetHook(c.Hook)
	}
	if c.SourceURL != "" {
		out.SetMetadataSourceURL(c.SourceURL)
	}
	if c.SourceProvider != "" {
		out.SetMetadataSourceProvider(c.SourceProvider)
	}
	if c.SourceVideoID != "" {
		out.SetMetadataSourceVideoID(c.SourceVideoID)
	}
	if c.StartSec != 0 {
		out.SetStartSec(c.StartSec)
	}
	if c.EndSec != 0 {
		out.SetEndSec(c.EndSec)
	}
	return out
}

func toExistingClip(c *asset.Asset) *sourcing.ExistingClip {
	if c == nil {
		return nil
	}
	return &sourcing.ExistingClip{
		ID:            c.ID,
		Name:          c.Name,
		Filename:      c.Filename,
		Duration:      c.Duration,
		Source:        string(c.Source),
		Category:      c.Category,
		Tags:          append([]string(nil), c.Tags...),
		LocalPath:     c.LocalPath(),
		DriveLink:     c.DriveLink(),
		DriveFileID:   c.DriveFileID(),
		LegacyFileMD5: c.LegacyFileMD5(),
		// source_url convergence (godlike/06): the typed field is the
		// canonical owner; the metadata key is a provenance mirror for
		// legacy rows. Read field-first so a round-trip through the mapper
		// never loses a URL that was persisted only in the url column.
		SourceURL:      firstNonEmpty(c.ExternalURL(), c.MetadataSourceURL()),
		SourceProvider: c.MetadataSourceProvider(),
		SourceVideoID:  c.MetadataSourceVideoID(),
		StartSec:       c.StartSec(),
		EndSec:         c.EndSec(),
		// Rich metadata fields (RICH-METADATA-QDRANT-VERIFY, July 2026)
		Summary:         c.ClipSummary(),
		Topics:          c.Topics(),
		Speakers:        c.Speakers(),
		MentionedPeople: c.MentionedPeople(),
		Hook:            c.Hook(),
	}
}
