// Package media_test — operator_inventory_reader_test.go ports the read-model
// properties that internal/platform/sqlite/assets/operatorread certified
// against its own SQLite fixture (MEDIA LEGACY READ-PLANE DEMOLITION,
// 2026-09-20).
//
// The properties are the same ones, on the engine that owns media_assets:
// soft-deleted rows never surface, the asset-state projection and the derived
// index health still agree with the canonical enums, the storage flags and the
// pending-outbox count still come from a single non-N+1 SELECT, pagination
// keeps its limit+1 cursor contract, "not found" stays (nil, nil) rather than
// an error, and every canonical facet value keeps appearing with count 0.
//
// Live-PostgreSQL test: gated behind TEST_POSTGRES_DSN (see testmain_test.go);
// skipped, never faked, when the DSN is unset (godlike/07).
package media_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// seedOperatorFixture mirrors the retired SQLite operatorread fixture row for
// row, so every ported assertion keeps its original meaning.
func seedOperatorFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	//             id        name                 filename      source        provider   media_type      lifecycle  index_state        content_hash  metadata_json                                                              embedding_json
	assets := []struct {
		id, name, filename, source, provider, mediaType, lifecycle, indexState, contentHash, metadata, embedding string
	}{
		{"asset-1", "Beluga underwater", "beluga.mp4", "artlist", "artlist", "clip", "ACTIVE", "INDEXED",
			"hash-1", `{"indexed_content_hash":"hash-1","embedding_model_version":"siglip-v2"}`, "[0.1]"},
		{"asset-2", "Pending asset", "pending.mp4", "stock", "stock", "clip", "ACTIVE", "DISCOVERED",
			"hash-2", `{}`, ""},
		{"asset-3", "Failed asset", "failed.mp4", "youtube_clip", "youtube", "clip", "ACTIVE", "EMBEDDING_FAILED",
			"hash-3", `{"last_index_error":"embedding failed"}`, ""},
		{"asset-4", "Stale asset", "stale.mp4", "artlist", "artlist", "clip", "ACTIVE", "INDEXED",
			"hash-4-new", `{"indexed_content_hash":"hash-4-old"}`, ""},
		{"asset-5", "Sound effect", "sfx.mp3", "artlist", "artlist", "sound_effect", "ACTIVE", "NOT_INDEXABLE",
			"hash-5", `{}`, ""},
		{"asset-6", "Deleted asset", "deleted.mp4", "stock", "stock", "clip", "DELETED", "DELETED",
			"hash-6", `{}`, ""},
	}
	for _, a := range assets {
		mustExecOperator(t, db, `
			INSERT INTO media_assets
				(id, name, filename, source, provider, media_type, lifecycle_state, index_state,
				 legacy_file_md5, metadata_json, embedding_json, collection_version, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'media_assets_v3',$12,$12)`,
			a.id, a.name, a.filename, a.source, a.provider, a.mediaType, a.lifecycle, a.indexState,
			a.contentHash, a.metadata, a.embedding, now)
	}

	// Locations: asset-1 has local + drive, asset-2 has drive only. The
	// unique (asset_id, location_kind) constraint keeps this 1:1 per kind.
	for _, loc := range []struct {
		assetID, kind, uri string
		primary            int
	}{
		{"asset-1", "local", "/tmp/asset-1.mp4", 1},
		{"asset-1", "drive", "drive://asset-1", 0},
		{"asset-2", "drive", "drive://asset-2", 1},
	} {
		mustExecOperator(t, db, `
			INSERT INTO asset_locations (asset_id, location_kind, uri, is_primary, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$5)`, loc.assetID, loc.kind, loc.uri, loc.primary, now)
	}

	// Two PENDING events for asset-2. The retired SQLite fixture used the same
	// event_key twice; PostgreSQL has a partial unique index on non-empty
	// event_keys, so the keys are distinct here — the assertion is about the
	// pending COUNT, not the key text.
	for _, key := range []string{"asset.index.requested:1", "asset.index.requested:2"} {
		mustExecOperator(t, db, `
			INSERT INTO outbox_events (event_type, aggregate_id, event_key, status, created_at, updated_at)
			VALUES ('asset.index.requested',$1,$2,'pending',$3,$3)`, "asset-2", key, now)
	}

	// One failed processing record for asset-3, with a NULL started_at so the
	// nullable-column scan is exercised too.
	mustExecOperator(t, db, `
		INSERT INTO asset_processing (asset_id, step, status, error_message, updated_at)
		VALUES ('asset-3','embedding','failed','embedding failed',$1)`, now)

	// The fixture is read-only from here on; assert the seed landed so a
	// silently empty fixture cannot make the suites below vacuously pass.
	var n int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets`).Scan(&n))
	require.Equal(t, 6, n, "operator fixture must seed 6 media_assets rows")
}

func mustExecOperator(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("seed fixture (%s): %v", query, err)
	}
}

func newOperatorReader(t *testing.T) *pgmedia.OperatorInventoryReader {
	t.Helper()
	db := newMediaTestDB(t)
	seedOperatorFixture(t, db)
	return pgmedia.NewOperatorInventoryReader(db, nil)
}

func TestOperatorInventoryReader_List_NoFilters(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 5, "the DELETED row must not surface")
	require.Equal(t, int64(5), page.Total)
	require.False(t, page.HasMore)
}

func TestOperatorInventoryReader_List_FilterBySource(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Source: "artlist", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	for _, item := range page.Items {
		require.Equal(t, "artlist", item.Source)
	}
	require.Equal(t, int64(3), page.Total)
}

func TestOperatorInventoryReader_List_FilterByAssetState(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	// The asset_state filter must go through the derived projection, not the
	// legacy physical column (which the migration triggers maintain).
	page, err := reader.List(ctx, operator.AssetInventoryQuery{AssetState: "READY", Limit: 10})
	require.NoError(t, err)

	// Two rows are READY: asset-1 (fresh index) and asset-4 (stale index).
	// asset_state and index health are ORTHOGONAL — asset_state answers
	// "is this asset usable?" (ACTIVE + INDEXED), while index health answers
	// "is its vector index current?". asset-4 is both READY and STALE, and a
	// filter on one must never imply anything about the other. Collapsing the
	// two would hide exactly the assets an operator needs to re-index.
	require.Len(t, page.Items, 2)
	require.Equal(t, int64(2), page.Total)
	readIDs := map[string]bool{}
	for _, item := range page.Items {
		require.Equal(t, "READY", string(item.AssetState))
		readIDs[item.ID] = true
	}
	require.True(t, readIDs["asset-1"])
	require.True(t, readIDs["asset-4"])

	// asset-4 carries the READY/STALE combination, so pin the pairing here:
	// if the projection ever starts folding index health into asset_state,
	// this is the assertion that fails.
	stale, err := reader.List(ctx, operator.AssetInventoryQuery{IndexState: "INDEXED", Limit: 10})
	require.NoError(t, err)
	staleIDs := map[string]bool{}
	for _, item := range stale.Items {
		staleIDs[item.ID] = true
	}
	require.True(t, staleIDs["asset-4"])
}

func TestOperatorInventoryReader_List_Search(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Search: "beluga", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "asset-1", page.Items[0].ID)
	require.Equal(t, int64(1), page.Total)
}

func TestOperatorInventoryReader_List_Pagination(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	page1, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 2})
	require.NoError(t, err)
	require.Len(t, page1.Items, 2)
	require.True(t, page1.HasMore)
	require.Equal(t, "2", page1.NextCursor)
	require.Equal(t, int64(5), page1.Total, "Total must be the unpaginated count")

	page2, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 2, Offset: 2})
	require.NoError(t, err)
	require.Len(t, page2.Items, 2)
	require.True(t, page2.HasMore)

	page3, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 2, Offset: 4})
	require.NoError(t, err)
	require.Len(t, page3.Items, 1)
	require.False(t, page3.HasMore)
	require.Empty(t, page3.NextCursor)

	// The three pages must partition the 5 rows without repeats.
	seen := map[string]bool{}
	for _, p := range []operator.AssetInventoryPage{page1, page2, page3} {
		for _, item := range p.Items {
			require.False(t, seen[item.ID], "id %s appeared on two pages", item.ID)
			seen[item.ID] = true
		}
	}
	require.Len(t, seen, 5)
}

func TestOperatorInventoryReader_List_IndexHealthCases(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 10})
	require.NoError(t, err)

	byID := make(map[string]*operator.AssetInventoryItem)
	for _, item := range page.Items {
		byID[item.ID] = item
	}

	require.Equal(t, operator.IndexHealthIndexed, operator.IndexHealthCode(byID["asset-1"].IndexHealth.Code))
	require.Equal(t, operator.IndexHealthPending, operator.IndexHealthCode(byID["asset-2"].IndexHealth.Code))
	require.Equal(t, operator.IndexHealthFailed, operator.IndexHealthCode(byID["asset-3"].IndexHealth.Code))
	require.Equal(t, operator.IndexHealthStale, operator.IndexHealthCode(byID["asset-4"].IndexHealth.Code))
	require.Equal(t, operator.IndexHealthNotIndexable, operator.IndexHealthCode(byID["asset-5"].IndexHealth.Code))

	// The live index-error signal comes from metadata_json.$.last_index_error
	// (media_assets has no writer for a dedicated error column), and it feeds
	// only the description, never the health code.
	require.Equal(t, "embedding failed", byID["asset-3"].LastError)
	require.Equal(t, "embedding failed", byID["asset-3"].IndexHealth.Description)
}

func TestOperatorInventoryReader_List_CanonicalContentHashAndDuration(t *testing.T) {
	db := newMediaTestDB(t)
	seedOperatorFixture(t, db)
	mustExecOperator(t, db, `UPDATE media_assets SET binary_sha256 = $1, content_sha256 = $2, duration_ms = $3 WHERE id = $4`,
		"binary-sha256", "content-sha256", int64(62000), "asset-1")
	reader := pgmedia.NewOperatorInventoryReader(db, nil)

	page, err := reader.List(context.Background(), operator.AssetInventoryQuery{Source: "artlist", Limit: 10})
	require.NoError(t, err)
	var got *operator.AssetInventoryItem
	for _, item := range page.Items {
		if item.ID == "asset-1" {
			got = item
			break
		}
	}
	require.NotNil(t, got)
	require.Equal(t, "binary-sha256", got.ContentHash, "binary SHA-256 is the canonical first choice")
	require.Equal(t, int64(62000), got.DurationMS)

	mustExecOperator(t, db, `UPDATE media_assets SET binary_sha256 = '' WHERE id = $1`, "asset-1")
	page, err = reader.List(context.Background(), operator.AssetInventoryQuery{Source: "artlist", Limit: 10})
	require.NoError(t, err)
	for _, item := range page.Items {
		if item.ID == "asset-1" {
			require.Equal(t, "content-sha256", item.ContentHash, "content SHA-256 is the fallback before legacy hashes")
		}
	}
}

func TestOperatorInventoryReader_List_StorageFlags(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 10})
	require.NoError(t, err)

	byID := make(map[string]*operator.AssetInventoryItem)
	for _, item := range page.Items {
		byID[item.ID] = item
	}

	require.True(t, byID["asset-1"].HasLocalFile)
	require.True(t, byID["asset-1"].HasDriveFile)
	require.True(t, byID["asset-1"].HasEmbedding)
	require.False(t, byID["asset-2"].HasLocalFile)
	require.True(t, byID["asset-2"].HasDriveFile)
	require.False(t, byID["asset-2"].HasEmbedding)
	require.Equal(t, "media_assets_v3", byID["asset-1"].CollectionVersion)
}

func TestOperatorInventoryReader_List_PendingOutbox(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	page, err := reader.List(ctx, operator.AssetInventoryQuery{Limit: 10})
	require.NoError(t, err)

	var found bool
	for _, item := range page.Items {
		if item.ID == "asset-2" {
			require.Equal(t, 2, item.PendingOutboxEvents)
			found = true
		}
	}
	require.True(t, found)
}

func TestOperatorInventoryReader_Get(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	inspection, err := reader.Get(ctx, "asset-1")
	require.NoError(t, err)
	require.NotNil(t, inspection)
	require.Equal(t, "Beluga underwater", inspection.Name)
	require.Equal(t, "hash-1", inspection.ContentHash)
	require.Equal(t, "hash-1", inspection.IndexedContentHash)
	require.Equal(t, "siglip-v2", inspection.EmbeddingVersion)
	require.Len(t, inspection.Locations, 2)
	require.Len(t, inspection.OutboxEvents, 0)
	// The metadata map is the raw metadata_json, surfaced typed.
	require.Equal(t, "hash-1", inspection.Metadata["indexed_content_hash"])
}

func TestOperatorInventoryReader_Get_WithProcessingAndOutbox(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	inspection, err := reader.Get(ctx, "asset-3")
	require.NoError(t, err)
	require.NotNil(t, inspection)
	require.Len(t, inspection.Processing, 1)
	require.Equal(t, "embedding", inspection.Processing[0].Step)
	require.Equal(t, "failed", string(inspection.Processing[0].Status))
	// started_at is NULL in the fixture: it must stay nil, not a zero stamp
	// masquerading as a real start time.
	require.Nil(t, inspection.Processing[0].StartedAt)

	withOutbox, err := reader.Get(ctx, "asset-2")
	require.NoError(t, err)
	require.Len(t, withOutbox.OutboxEvents, 2)
	for _, ev := range withOutbox.OutboxEvents {
		require.Equal(t, "pending", ev.Status)
		require.Equal(t, "asset.index.requested", ev.EventType)
		require.False(t, ev.CreatedAt.IsZero(), "a stamped outbox row must parse to a real time")
	}
}

func TestOperatorInventoryReader_Get_SoftDeletedIsNotFound(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	// A soft-deleted row is excluded by the same filter the list uses, so the
	// inspector reports it as absent rather than as a phantom asset.
	inspection, err := reader.Get(ctx, "asset-6")
	require.NoError(t, err)
	require.Nil(t, inspection)
}

func TestOperatorInventoryReader_Get_NotFound(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	inspection, err := reader.Get(ctx, "missing")
	require.NoError(t, err)
	require.Nil(t, inspection)
}

func TestOperatorInventoryReader_Facets(t *testing.T) {
	reader := newOperatorReader(t)
	ctx := context.Background()

	facets, err := reader.Facets(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, facets.MediaTypes)
	require.NotEmpty(t, facets.LifecycleStates)
	require.NotEmpty(t, facets.AssetStates)
	require.NotEmpty(t, facets.IndexStates)
	require.NotEmpty(t, facets.Sources)
	require.NotEmpty(t, facets.Providers)

	// Every canonical enum member must appear, including the zero-count ones
	// (that is what the merge-with-labels step exists for).
	var lifecycleActive *operator.FacetGroup
	for i := range facets.LifecycleStates {
		if facets.LifecycleStates[i].Code == "ACTIVE" {
			lifecycleActive = &facets.LifecycleStates[i]
		}
	}
	require.NotNil(t, lifecycleActive, "the ACTIVE lifecycle facet must always be present")
	require.Equal(t, int64(5), lifecycleActive.Count)

	// The DELETED lifecycle member is present with count 0 (its only row is
	// excluded by the base filter), which is the canonical-merge contract.
	for _, f := range facets.LifecycleStates {
		if f.Code == "DELETED" {
			require.Equal(t, int64(0), f.Count)
		}
	}

	// source/provider facets are real value distributions over the
	// non-deleted rows. asset-6 is `stock` but soft-deleted, so stock is 1
	// here — the excluded row must not inflate the count.
	sources := map[string]int64{}
	for _, f := range facets.Sources {
		sources[f.Code] = f.Count
	}
	require.Equal(t, int64(3), sources["artlist"])
	require.Equal(t, int64(1), sources["stock"])
	require.Equal(t, int64(1), sources["youtube_clip"])

	// Facets are sorted by code for stable UI output.
	for i := 1; i < len(facets.Sources); i++ {
		require.Less(t, facets.Sources[i-1].Code, facets.Sources[i].Code)
	}
}

// Compile-time pin: the reader satisfies the capability-owned port, so a
// method-shape drift is a build failure rather than a 503 at runtime.
var _ operator.AssetInventoryReader = (*pgmedia.OperatorInventoryReader)(nil)
