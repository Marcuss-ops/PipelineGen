package media

import (
	"context"
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// GetClip hydrates the canonical kernel asset for a media_assets id from the
// PostgreSQL media SSOT.
//
// It is the PostgreSQL counterpart of the legacy SQLite
// ClipsRepository.GetClip read surface, so admin/operator readers can run on
// the media SSOT without a second media registry. A missing row returns
// (nil, nil) — the SAME contract as the legacy repository — so callers keep
// their existing nil checks unchanged.
func (s *MediaSearcher) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	rec, err := s.GetAsset(ctx, id)
	if err != nil {
		if errors.Is(err, ErrMediaAssetNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return rec.HydrateAsset(), nil
}

// AssetDetailsReader adapts the media read repository to the narrow
// `Get(ctx, id) (*asset.Details, error)` lookup surface consumed by the media
// preflight and the Drive location verifier. It is the PostgreSQL counterpart
// of *detail.Service for those ports, so both keep ONE implementation.
type AssetDetailsReader struct {
	reader *MediaSearcher
}

// NewAssetDetailsReader wires the adapter. A nil reader returns nil.
func NewAssetDetailsReader(reader *MediaSearcher) *AssetDetailsReader {
	if reader == nil {
		return nil
	}
	return &AssetDetailsReader{reader: reader}
}

// Get implements the narrow details lookup against the media SSOT.
func (r *AssetDetailsReader) Get(ctx context.Context, id string) (*asset.Details, error) {
	if r == nil || r.reader == nil {
		return nil, errors.New("postgres media: asset details reader not wired")
	}
	return r.reader.GetAssetDetails(ctx, id)
}

// GetAssetDetails returns the canonical asset plus its location projection,
// mirroring detail.Service.Get against the PostgreSQL media SSOT. A missing
// row returns (nil, nil) so the LocationVerifier orphan check keeps its exact
// contract (any error there means "not found", a nil means "lookup ok").
func (s *MediaSearcher) GetAssetDetails(ctx context.Context, id string) (*asset.Details, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	rec, err := s.GetAsset(ctx, id)
	if err != nil {
		if errors.Is(err, ErrMediaAssetNotFound) {
			return nil, nil
		}
		return nil, err
	}
	details := &asset.Details{Asset: rec.HydrateAsset()}
	if details.Asset == nil {
		return nil, nil
	}
	if rec.DriveFileID != "" || rec.DriveLink != "" || rec.DownloadLink != "" {
		details.Locations = append(details.Locations, &asset.Location{
			AssetID:      rec.ID,
			LocationKind: asset.LocationKindDrive,
			URI:          rec.DriveLink,
			ExternalID:   rec.DriveFileID,
			AccessURL:    rec.DriveLink,
			DownloadURL:  rec.DownloadLink,
			IsPrimary:    true,
		})
	}
	return details, nil
}

// HydrateAsset projects the canonical read model onto the kernel asset type.
// It is the SINGLE translation site so every PostgreSQL-backed reader surfaces
// the same kernel shape and no caller re-implements field mapping.
func (r *MediaAssetRecord) HydrateAsset() *asset.Asset {
	if r == nil {
		return nil
	}
	out := &asset.Asset{
		ID:             r.ID,
		Source:         asset.Source(r.Source),
		Name:           r.Name,
		Filename:       r.Filename,
		MediaType:      asset.MediaType(r.MediaType),
		Category:       r.Category,
		SourceURL:      r.SourceURL,
		ThumbnailURL:   r.ThumbnailURL,
		Duration:       time.Duration(r.DurationMS) * time.Millisecond,
		Tags:           append([]string(nil), r.Tags...),
		LifecycleState: asset.LifecycleState(r.LifecycleState),
		CreatedAt:      r.CreatedAtTime(),
		Metadata:       r.MetadataMap(),
	}
	// The column projection wins over the metadata mirror: the columns are the
	// canonical post-cutover storage; the mirrors exist only for rows written
	// before the column projection existed.
	if r.DriveFileID != "" {
		out.SetDriveFileID(r.DriveFileID)
	}
	if r.DriveLink != "" {
		out.SetDriveLink(r.DriveLink)
	}
	if r.DownloadLink != "" {
		out.SetDownloadLink(r.DownloadLink)
	}
	if r.LocalPath != "" {
		out.SetLocalPath(r.LocalPath)
	}
	if r.SHA256 != "" {
		out.SetLegacyFileMD5(r.SHA256)
		out.SetContentHash(r.SHA256)
	}
	if r.IndexState != "" {
		out.SetMetadataString("index_state", r.IndexState)
	}
	if r.SearchTerms != "" {
		out.SearchTerms = decodeTagsJSON(r.SearchTerms)
	}
	if r.ReviewStatus != "" {
		out.ReviewStatus = asset.ReviewStatus(r.ReviewStatus)
	}
	return out
}
