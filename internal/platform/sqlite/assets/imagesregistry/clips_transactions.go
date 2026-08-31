package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// BeginTx opens a caller-owned transaction for dispatcher operations.
func (r *ClipsRepository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("clips.BeginTx: database is required")
	}
	return r.db.BeginTx(ctx, opts)
}

// UpsertClipTx is the dispatcher compatibility surface for a transaction-
// scoped clip commit. The repository only maps the domain asset to the
// canonical persistence request; SQLite writes remain owned by the injected
// AssetCommitter. The dispatcher owns the surrounding transaction and its
// outbox event, therefore EmitIndexEvent is deliberately false here.
func (r *ClipsRepository) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	if clip == nil {
		return fmt.Errorf("upsert clip: asset is required")
	}
	if tx == nil {
		return fmt.Errorf("upsert clip: transaction is required")
	}
	if r == nil || r.assetCommitter == nil {
		return fmt.Errorf("upsert clip: canonical AssetCommitter is required; repository SQL fallback has been removed")
	}

	if clip.SourceURL != "" && clip.MetadataSourceURL() == "" {
		clip.SetMetadataSourceURL(clip.SourceURL)
	}
	taxonomy, err := resolveClipTaxonomy(clip)
	if err != nil {
		return err
	}

	contentHash := clip.LegacyFileMD5()
	if contentHash == "" {
		contentHash = clip.ContentHash()
	}
	filename := clip.Filename
	if filename == "" {
		filename = clip.ID + ".asset"
	}
	name := clip.Name
	if name == "" {
		name = clip.ID
	}
	mediaType := string(clip.MediaType)
	if mediaType == "" || mediaType == "clip" {
		mediaType = "video"
	}
	lifecycle := string(clip.LifecycleState)
	if lifecycle == "" {
		lifecycle = string(asset.StateActive)
	}
	metadata := persistence.TypedMetadata{
		Title:          clip.Title(),
		Description:    clip.Description(),
		SourceVersion:  clip.GetMetadataString("source_version"),
		SourceProvider: clip.MetadataSourceProvider(),
		SourceVideoID:  clip.MetadataSourceVideoID(),
		Tags:           append([]string(nil), clip.Tags...),
		Category:       clip.Category,
		Extra:          clip.Metadata,
	}
	if metadata.Title == "" {
		metadata.Title = name
	}
	if metadata.SourceVersion == "" {
		metadata.SourceVersion = contentHash
	}

	_, err = r.assetCommitter.CommitTx(ctx, tx, persistence.CommitRequest{
		AssetID:        clip.ID,
		Source:         string(clip.Source),
		Name:           name,
		Filename:       filename,
		MediaType:      mediaType,
		Category:       clip.Category,
		GroupName:      clip.Group,
		DurationMs:     clip.Duration.Milliseconds(),
		ContentHash:    contentHash,
		Description:    clip.Description(),
		SearchText:     clip.SearchText,
		LifecycleState: lifecycle,
		IndexState:     clip.GetMetadataString("index_state"),
		LocalPath:      clip.LocalPath(),
		FolderID:       clip.FolderID(),
		FolderPath:     clip.FolderPath(),
		ThumbnailURL:   clip.ThumbnailURL,
		SourceURL:      clip.SourceURL,
		Title:          metadata.Title,
		SourceProvider: clip.MetadataSourceProvider(),
		SourceVideoID:  clip.MetadataSourceVideoID(),
		StartMs:        int64(asset.MetadataFloat(clip.Metadata, "start_sec") * 1000),
		EndMs:          int64(asset.MetadataFloat(clip.Metadata, "end_sec") * 1000),
		Metadata:       metadata,
		Taxonomy:       taxonomy,
		Locations:      clipLocationsForCanonicalCommit(clip, contentHash),
		EmitIndexEvent: false,
	})
	if err != nil {
		return fmt.Errorf("upsert clip %s through canonical writer: %w", clip.ID, err)
	}
	return nil
}

func clipLocationsForCanonicalCommit(clip *asset.Asset, contentHash string) []persistence.LocationCommit {
	locations := make([]persistence.LocationCommit, 0, 2)
	if localPath := clip.LocalPath(); localPath != "" {
		locations = append(locations, persistence.LocationCommit{
			Kind: "local", Provider: "local", URI: localPath,
			MimeType: string(clip.MediaType), LegacyFileMD5: contentHash,
		})
	}
	if fileID, link := clip.DriveFileID(), clip.DriveLink(); fileID != "" || link != "" {
		uri := link
		if fileID != "" {
			uri = "drive://" + fileID
		}
		locations = append(locations, persistence.LocationCommit{
			Kind: "drive", Provider: "drive", ExternalID: fileID,
			URI: uri, WebViewLink: link, DownloadURL: clip.DownloadLink(),
			MimeType: string(clip.MediaType), LegacyFileMD5: contentHash,
			IsPrimary: true,
		})
	}
	return locations
}

func resolveClipTaxonomy(clip *asset.Asset) (mediaregistry.AssetTaxonomy, error) {
	mediaType := mediaregistry.MediaType(string(clip.MediaType))
	if mediaType == "" || mediaType == "clip" {
		mediaType = mediaregistry.MediaVideo
		clip.MediaType = asset.MediaType(mediaType)
	}
	kind := mediaregistry.AssetKind(asset.MetadataString(clip.Metadata, "asset_kind"))
	role := asset.MetadataString(clip.Metadata, "semantic_role")
	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID: clip.ID, Provider: string(clip.Source), MediaType: mediaType,
		AssetKind: kind, SemanticRole: role,
	})
	if err != nil {
		return mediaregistry.AssetTaxonomy{}, fmt.Errorf("upsert clip %s: resolve taxonomy: %w", clip.ID, err)
	}
	return taxonomy, nil
}

// SetIndexStateTx applies the tx-scoped index-state mutation through the
// canonical AssetMutator. It intentionally has no SQL fallback: the
// dispatcher must fail closed when the canonical writer is absent.
func (r *ClipsRepository) SetIndexStateTx(ctx context.Context, tx *sql.Tx, id string, state asset.IndexState) error {
	if tx == nil {
		return fmt.Errorf("clips.SetIndexStateTx: tx is required (callers in production MUST supply the Dispatcher's tx; tests may build a tx via db.BeginTx)")
	}
	if id == "" {
		return fmt.Errorf("clips.SetIndexStateTx: id is required")
	}
	if state == "" {
		return fmt.Errorf("clips.SetIndexStateTx: state is required (got empty string; use the canonical 7-state enum)")
	}
	if !state.Valid() {
		return fmt.Errorf("clips.SetIndexStateTx: state %q is not a canonical IndexState — call sites in production must validate", state)
	}
	if r == nil || r.assetMutator == nil {
		return fmt.Errorf("clips.SetIndexStateTx: canonical AssetMutator is required; repository SQL fallback has been removed")
	}
	value := string(state)
	if err := r.assetMutator.PatchAssetTx(ctx, tx, persistence.AssetPatch{
		AssetID:    id,
		IndexState: &value,
	}); err != nil {
		return fmt.Errorf("clips.SetIndexStateTx(%s, %s): %w", id, state, err)
	}
	return nil
}
