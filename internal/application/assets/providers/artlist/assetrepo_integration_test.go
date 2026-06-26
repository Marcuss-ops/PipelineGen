// PR12b integration test: verifies that artlist.SearchService.UpsertClip,
// when wired with an asset.Repository via SetAssetRepo, routes through
// the canonical writer AND legacy readers (assets.ClipsRepository) observe the
// same row data.
//
// QDRANT-002 close-out (June 2026): the legacy SetAssetRepo override
// was retired. Raw repo.UpsertClip no longer exists on *ClipsRepository;
// the canonical write path is now outbox.Dispatcher.EnqueueAndIndex,
// and SearchService.NewSearchService requires a non-nil dispatcher.
//
// The PR12b integration suite is now reduced to a single smoke that
// asserts the constructor fails closed when the dispatcher is missing
// (composition root invariant). The deep round-trip tests are slated
// for the next PR in the wave, which will build a fake dispatcher
// that emits a real outbox_event + writes to media_assets in the
// same tx (so the round-trip semantic is verifiable without the
// actual Qdrant backend).
package artlist

import (
	"database/sql"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// pr12bArtlistSchema mirrors the production schema: CanonicalMediaAssetsSchema
// (single source of truth for media_assets) + asset_locations + outbox_events.
// Keeps fixtures in lockstep with migration changes — a new canonical column
// added by migration only requires updating internal/infrastructure/database/canonical.go.
const pr12bArtlistSchema = drive.CanonicalMediaAssetsSchema + `

CREATE TABLE IF NOT EXISTS asset_locations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	asset_id TEXT NOT NULL,
	location_kind TEXT NOT NULL,
	uri TEXT NOT NULL,
	external_id TEXT NOT NULL DEFAULT '',
	web_view_link TEXT NOT NULL DEFAULT '',
	download_url TEXT NOT NULL DEFAULT '',
	mime_type TEXT NOT NULL DEFAULT '',
	file_size_bytes INTEGER NOT NULL DEFAULT 0,
	file_hash TEXT NOT NULL DEFAULT '',
	is_primary INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE(asset_id, location_kind)
);

CREATE TABLE IF NOT EXISTS outbox_events (
	id TEXT PRIMARY KEY,
	aggregate_id TEXT NOT NULL DEFAULT '',
	aggregate_type TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	event_key TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 10,
	last_error TEXT NOT NULL DEFAULT '',
	next_attempt_at TEXT,
	worker_id TEXT NOT NULL DEFAULT '',
	lease_id TEXT NOT NULL DEFAULT '',
	lease_expiry TEXT,
	completed_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_outbox_aggregate_id ON outbox_events(aggregate_id);
`

// setupArtlistPR12b creates a fresh SQLite DB with the full PR12b schema,
// wires clips + assetrepo repos, and registers teardown. Returns the DB
// handle so tests can also query outbox_events directly.
func setupArtlistPR12b(t *testing.T) (db *sql.DB, clipsRepo *assets.ClipsRepository, assetRepo asset.Repository) {
	t.Helper()
	db = drive.NewTestDBWithSchema(t, pr12bArtlistSchema)
	t.Cleanup(func() { _ = db.Close() })
	log := zap.NewNop()
	clipsRepo = assets.NewClipsRepository(db, log)
	assetStore := asset.NewAssetStoreSQLite(db, log)
	assetRepo = assetStore.AssetRepository()
	return
}

// zeroTime is the canonical zero-time used by DeletedAt fixtures so that
// timeutil.FormatPtrRFC3339 binds a non-NULL string (which the test schema's
// `deleted_at TEXT NOT NULL DEFAULT ”` accepts). Without this, nil pointer
// formatting binds SQL NULL and trips the NOT NULL constraint.
var zeroTime = time.Time{}

func TestArtlistPR12b_NewSearchServiceRequiresDispatcherQDRANT002(t *testing.T) {
	// QDRANT-002 close-out (June 2026): the SearchService constructor
	// now requires a non-nil dispatcher. Every write must route through
	// outbox.Dispatcher.EnqueueAndIndex; there is no raw fallback.
	// This smoke asserts the wiring invariant without exercising the
	// deep round-trip (which would need a fake dispatcher + a tx and
	// would belong to the next-wave integration suite).
	db, clipsRepo, _ := setupArtlistPR12b(t)
	svc := &Service{log: zap.NewNop(), assetStore: clipsRepo}

	if _, err := NewSearchService(svc, nil); err == nil {
		t.Fatalf("expected NewSearchService(svc, nil) to return an error (QDRANT-002 close-out invariant), got nil")
	}

	if _, err := NewService(ServiceDeps{
		ServicePorts: ServicePorts{AssetStore: clipsRepo, Indexer: nil, MetadataWriter: nil},
		ServiceDependencies: ServiceDependencies{
			Cfg:    nil,
			MainDB: db,
			Log:    zap.NewNop(),
		},
	}); err == nil {
		t.Fatalf("expected NewService with nil dispatcher to return an error from NewSearchService propagation, got nil")
	}
}

func TestArtlistPR12b_UpsertClipRoutesThroughAssetRepo(t *testing.T) {
	t.Skip("QDRANT-002 close-out (June 2026): round-trip test retired. Replaced by TestArtlistPR12b_NewSearchServiceRequiresDispatcherQDRANT002 above. New dispatcher-driven round-trip tests land in the next wave with a fake dispatcher that emits real outbox_event + writes media_assets in the same tx.")
}

func TestArtlistPR12b_UpsertClipWithoutAssetRepoFallsBack(t *testing.T) {
	t.Skip("QDRANT-002 close-out (June 2026): legacy fallback test retired. raw repo writes were the canonical write-bypass the close-out eliminated. See TestArtlistPR12b_NewSearchServiceRequiresDispatcherQDRANT002.")
}
