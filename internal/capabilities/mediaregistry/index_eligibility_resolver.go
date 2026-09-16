// Package mediaregistry — index_eligibility_resolver.go: the single
// resolver for the canonical index-eligibility policy.
//
// "Registered" is not "searchable": this resolver reads an asset's canonical
// taxonomy dimensions (media_assets.asset_kind + media_type) and returns the
// searchability decision from AssetTaxonomy.IndexEligibility. The MediaIndexer
// consults it before any embedding work, so voiceover / final_audio / bgm / sfx
// stay REGISTERED without being projected into the vector store.
//
// MEDIA-SSOT P2-9 Phase 2. This file previously read those two columns with
// `SELECT COALESCE(asset_kind, ”), COALESCE(media_type, ”) FROM media_assets
// WHERE id = ?` through a `RowQuerier` satisfied by *storage.SQLiteDB. The
// question was therefore expressed as an engine-blind query against whatever
// handle the caller happened to hold, and the single production caller
// (clipindexer.Service.Eligibility) held the operational SQLite handle while
// PostgreSQL was the media SSOT — so the taxonomy gate graded a database that
// holds no committed media rows.
//
// The read is now a consumer-owned port. Naming the media domain explicitly is
// what lets the composition root resolve the engine (PostgreSQL) instead of this
// package inheriting one from a *sql.DB: this capability no longer names a
// database for the eligibility decision.
//
// Removal condition: `ErrTaxonomySchemaUnavailable` and the nil-reader branch
// exist only for the compatibility window described below. When the pre-195
// schema window closes, delete both.
package mediaregistry

import (
	"context"
	"fmt"
)

// AssetEligibilityReader is the narrow media-SSOT read behind the eligibility
// decision: the canonical taxonomy dimensions of one asset. It returns ONLY
// asset_kind and media_type — the two fields the policy consumes — rather than
// widening the shared media read model. If several consumers later need the
// same wider projection, promote it deliberately instead of growing this port.
//
// PostgreSQL is the only production implementation
// (pgmedia.MediaEligibilityReader); it is resolved from the media SSOT handle by
// the composition root. A nil reader is a media-plane-closed signal: the
// taxonomy cannot be read, and ResolveIndexEligibility reports that rather than
// falling back to a second engine.
type AssetEligibilityReader interface {
	AssetTaxonomy(ctx context.Context, assetID string) (assetKind string, mediaType string, err error)
}

// ErrTaxonomySchemaUnavailable identifies an eligibility read that could not be
// answered — either a pre-migration media_assets table (the historical cause) or
// no media-SSOT reader wired at all.
//
// It is the only eligibility error that IndexAsset may bridge to the legacy
// indexing path; ordinary read failures remain fail-closed.
var ErrTaxonomySchemaUnavailable = fmt.Errorf("taxonomy schema unavailable")

// ResolveIndexEligibility resolves the searchability decision for an asset from
// its canonical taxonomy dimensions. A missing taxonomy (asset predating the
// asset_kind/media_type columns) resolves to REGISTERED, which is the
// fail-closed default: an asset is only SEARCHABLE once it is explicitly
// classified as video/image.
//
// A nil reader returns ErrTaxonomySchemaUnavailable and the REGISTERED default.
// That is the honest mapping rather than a convenience: with no media-SSOT read
// surface the taxonomy is genuinely unreadable, so the caller's documented
// compatibility window applies. Silently reading the operational SQLite mirror
// instead is exactly the Postgres-writer/SQLite-reader split-brain this port
// removes.
func ResolveIndexEligibility(ctx context.Context, r AssetEligibilityReader, assetID string) (IndexEligibility, error) {
	if r == nil {
		return IndexEligibilityRegistered, fmt.Errorf(
			"%w: no media-SSOT eligibility reader wired (media plane closed)", ErrTaxonomySchemaUnavailable)
	}
	assetKind, mediaType, err := r.AssetTaxonomy(ctx, assetID)
	if err != nil {
		return IndexEligibilityRegistered, err
	}
	return AssetTaxonomy{
		AssetID:   assetID,
		AssetKind: AssetKind(assetKind),
		MediaType: MediaType(mediaType),
	}.IndexEligibility(), nil
}
