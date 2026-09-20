// cmd/admin/internal/audit/matt_damon_media_source.go — the media-engine
// boundary of the Matt Damon asset audit.
//
// MEDIA-SSOT (2026-09-20): the asset inventory is a media_assets read, so it
// resolves from the PostgreSQL media SSOT through the narrow port below. The
// operational SQLite handle stays bound to the IMAGE-domain membership tables
// (subjects / entity_image_catalog_*), which have no PostgreSQL home, so the two
// engines are named separately rather than sharing a generic handle. A nil media
// port fails closed.
//
// Membership selection stays in Go on purpose: the identity rules (structured
// metadata keys, catalog candidate evidence) are the audit's contract, and
// pushing them into SQL would let two definitions of "member" drift.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

type mattDamonAssetRow struct {
	ID             string
	MediaType      string
	DriveFileID    string
	YouTubeVideoID string
	StartMS        int64
	EndMS          int64
	ContentSHA256  string
	BinarySHA256   string
	MetadataJSON   string
}

// mattDamonMediaSource is the narrow media read this audit depends on.
//
// MEDIA-SSOT: production binds the PostgreSQL reader
// (pgmedia.StructuredAssetIdentityReader). The port is engine-specific rather
// than a generic *sql.DB for the same reason the read moved: a handle-typed port
// is how the media read would drift back onto the operational store.
type mattDamonMediaSource interface {
	ListStructuredAssetIdentities(ctx context.Context) ([]pgmedia.StructuredAssetIdentityRow, error)
}

func mattDamonStructuredAssets(ctx context.Context, media mattDamonMediaSource, subjectTokens, catalogAssetIDs map[string]struct{}, catalogEvidence map[string][]string) ([]mattDamonAssetRecord, error) {
	rows, err := media.ListStructuredAssetIdentities(ctx)
	if err != nil {
		return nil, fmt.Errorf("audit-matt-damon-assets: read media assets: %w", err)
	}

	out := make([]mattDamonAssetRecord, 0, len(rows))
	seen := make(map[string]struct{})
	for _, source := range rows {
		row := mattDamonAssetRow{
			ID:             source.ID,
			MediaType:      source.MediaType,
			DriveFileID:    source.DriveFileID,
			YouTubeVideoID: source.YouTubeVideoID,
			StartMS:        source.StartMS,
			EndMS:          source.EndMS,
			ContentSHA256:  source.ContentSHA256,
			BinarySHA256:   source.BinarySHA256,
			MetadataJSON:   source.MetadataJSON,
		}
		row.ID = strings.TrimSpace(row.ID)
		if row.ID == "" {
			continue
		}
		metadata := map[string]any{}
		if strings.TrimSpace(row.MetadataJSON) != "" && strings.TrimSpace(row.MetadataJSON) != "{}" {
			if err := json.Unmarshal([]byte(row.MetadataJSON), &metadata); err != nil {
				return nil, fmt.Errorf("audit-matt-damon-assets: malformed metadata_json for asset %q: %w", row.ID, err)
			}
		}

		evidence := append([]string(nil), catalogEvidence[row.ID]...)
		if structuredMetadataMatches(metadata, subjectTokens) {
			evidence = appendUniqueString(evidence, "metadata_json.structured_entity")
		}
		if _, ok := catalogAssetIDs[row.ID]; ok {
			evidence = appendUniqueString(evidence, "entity_catalog_materialization")
		}
		if len(evidence) == 0 {
			continue
		}
		if _, ok := seen[row.ID]; ok {
			continue
		}
		seen[row.ID] = struct{}{}
		out = append(out, mattDamonAssetRecord{
			AssetID:        row.ID,
			MediaType:      row.MediaType,
			DriveFileID:    row.DriveFileID,
			YouTubeVideoID: row.YouTubeVideoID,
			StartMS:        row.StartMS,
			EndMS:          row.EndMS,
			ContentSHA256:  row.ContentSHA256,
			BinarySHA256:   row.BinarySHA256,
			Evidence:       evidence,
		})
	}
	return out, nil
}

func structuredMetadataMatches(metadata map[string]any, subjectTokens map[string]struct{}) bool {
	for _, key := range []string{"canonical_entity_id", "subject_id"} {
		value, ok := metadata[key].(string)
		if !ok {
			continue
		}
		if _, ok := subjectTokens[strings.TrimSpace(value)]; ok {
			return true
		}
	}
	return false
}
