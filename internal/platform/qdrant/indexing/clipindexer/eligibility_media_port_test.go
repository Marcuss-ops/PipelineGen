// Package clipindexer — eligibility_media_port_test.go pins the MEDIA-SSOT
// P2-9 Phase 2 eligibility seam.
//
// Service.Eligibility used to resolve the taxonomy from s.db — the OPERATIONAL
// SQLite store — while media_assets is owned by PostgreSQL. The gate therefore
// graded a database that holds no committed media rows, and the only reason it
// never misfired in production is that IndexAsset short-circuits to the
// canonical PostgreSQL outbox first. That is a coincidence of wiring, not an
// invariant, so the read now resolves through the narrow media-SSOT port.
package clipindexer

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// sqliteTaxonomyReader adapts a test-owned SQLite fixture to the media
// eligibility port. It exists so tests can exercise the taxonomy gate without a
// live PostgreSQL instance; production always resolves the port from the media
// SSOT (see wiring.composition → SetMediaEligibilityReader).
type sqliteTaxonomyReader struct {
	db *sql.DB
}

func (r sqliteTaxonomyReader) AssetTaxonomy(ctx context.Context, assetID string) (string, string, error) {
	var assetKind, mediaType string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(asset_kind, ''), COALESCE(media_type, '') FROM media_assets WHERE id = ?`,
		assetID,
	).Scan(&assetKind, &mediaType)
	if err != nil {
		return "", "", err
	}
	return assetKind, mediaType, nil
}

var _ capregistry.AssetEligibilityReader = sqliteTaxonomyReader{}

// TestServiceEligibilityDoesNotBypassTheMediaPortOnOperationalDB pins the
// no-fallback rule directly: even when the operational store holds a row whose
// taxonomy would grade as SEARCHABLE, an unwired media reader must fail closed.
//
// This is the regression that matters. A nil-reader branch that silently fell
// back to s.db would look green here while restoring exactly the
// Postgres-writer/SQLite-reader split-brain the port removes.
func TestServiceEligibilityDoesNotBypassTheMediaPortOnOperationalDB(t *testing.T) {
	db := drive.NewMigratedTestDB(t)
	defer func() { _ = db.Close() }()

	// A fully-classified, searchable row on the OPERATIONAL store. It must not
	// be consulted: media_assets is PostgreSQL-owned.
	_, err := db.Exec(`
		INSERT INTO media_assets (id, name, source, media_type, asset_kind, source_version, lifecycle_state, index_state)
		VALUES ('op_only_asset', 'Operational Only', 'artlist', 'video', 'stock_video', 'op-v1', 'ACTIVE', 'DISCOVERED')
	`)
	require.NoError(t, err)

	svc := NewService(nil, &drive.SQLiteDB{DB: db}, ":memory:", zap.NewNop())

	eligibility, err := svc.Eligibility(context.Background(), "op_only_asset")
	require.Error(t, err, "an unwired media reader must fail closed")
	assert.True(t, errors.Is(err, capregistry.ErrTaxonomySchemaUnavailable),
		"expected ErrTaxonomySchemaUnavailable so IndexAsset's documented compatibility window applies, got %v", err)
	assert.Equal(t, capregistry.IndexEligibilityRegistered, eligibility,
		"the fail-closed default is REGISTERED: never guess that an unreadable taxonomy is searchable")
}

// TestServiceEligibilityResolvesThroughTheMediaPort pins that the decision comes
// from the injected port and follows the canonical policy (video/image are
// searchable; every other media type stays registered-but-not-searchable).
func TestServiceEligibilityResolvesThroughTheMediaPort(t *testing.T) {
	db := drive.NewMigratedTestDB(t)
	defer func() { _ = db.Close() }()

	_, err := db.Exec(`
		INSERT INTO media_assets (id, name, source, media_type, asset_kind, source_version, lifecycle_state, index_state)
		VALUES
			('clip_video', 'Clip Video', 'artlist', 'video', 'stock_video', 'v1', 'ACTIVE', 'DISCOVERED'),
			('vo_audio', 'Voiceover', 'voiceover', 'audio', 'voiceover', 'v1', 'ACTIVE', 'DISCOVERED')
	`)
	require.NoError(t, err)

	svc := NewService(nil, &drive.SQLiteDB{DB: db}, ":memory:", zap.NewNop())
	svc.SetMediaEligibilityReader(sqliteTaxonomyReader{db: db})

	for _, tc := range []struct {
		assetID string
		want    capregistry.IndexEligibility
	}{
		{"clip_video", capregistry.IndexEligibilitySearchable},
		{"vo_audio", capregistry.IndexEligibilityRegistered},
	} {
		got, err := svc.Eligibility(context.Background(), tc.assetID)
		require.NoErrorf(t, err, "Eligibility(%s)", tc.assetID)
		assert.Equalf(t, tc.want, got, "Eligibility(%s)", tc.assetID)
	}
}

// TestSetMediaEligibilityReaderIsNilSafe pins the composition-root contract:
// wiring sets the reader only when the media SSOT handle exists, and a nil
// Service (disabled module) must not panic.
func TestSetMediaEligibilityReaderIsNilSafe(t *testing.T) {
	var svc *Service
	svc.SetMediaEligibilityReader(sqliteTaxonomyReader{})
	if svc != nil {
		t.Fatal("nil-safety probe must run against a nil Service")
	}
	// A non-nil Service with an explicitly nil reader is also accepted; the
	// fail-closed behaviour is asserted above.
	real := NewService(nil, nil, "", zap.NewNop())
	real.SetMediaEligibilityReader(nil)
}
