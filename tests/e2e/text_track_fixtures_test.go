package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	youtubeusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	texttrackssql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/texttracks"
)

// Resolver-focused text-track fixture: SQLite text-track repository,
// resolver, and PayloadMapper wiring on the shared Qdrant E2E fixture.

// textTrackFixture extends e2eFixture with text track components:
// TextTrackRepository (SQLite), TextTrackResolver (usecase), and
// the PayloadMapper wired with TextTrackQuerier + index_languages.
type textTrackFixture struct {
	*e2eFixture
	TTRepo   *texttrackssql.TextTrackRepositorySQLite
	Resolver *youtubeusecase.TextTrackResolver
}

// newTextTrackFixture creates a hermetic fixture with text track support.
// The base e2eFixture provides in-memory SQLite + mock Qdrant + production
// adapters. This wrapper adds:
//   - asset_text_tracks table (migration 137 DDL)
//   - TextTrackRepositorySQLite wired to the same in-memory DB
//   - TextTrackResolver for the priority-chain lookup
//   - PayloadMapper wired with TextTrackQuerier + index_languages
func newTextTrackFixture(t *testing.T, collection string) *textTrackFixture {
	t.Helper()
	fx := newE2EFixture(t, collection)

	// asset_text_tracks is created by the migration chain applied via newE2EFixture.

	// Add asset_text_track_segments table (migration 14X DDL).
	// Bucket-B closure (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3):
	// the text-track e2e resolver's Priority 2 lookup walks
	// asset_text_track_segments via findCuesForTrackID. The hardcoded
	// hermetic DDL must mirror the production migration
	// (migrations/sqlite/1410427846_create_asset_text_track_segments.sql)
	// so failures here are NOT obscured by feature drift between fixture
	// and production schema. Foreign-key ON DELETE CASCADE preserves the
	// production delete contract: removing an asset_text_tracks.parent
	// cascades to its segments (godlike/06 SSOT — fixture is HERMETICALLY
	// BYTE-EQUIVALENT to the canonical production SUBSET; any drift surfaces
	// here at e2e time, not silently at production runtime).
	_, err := fx.DB.Exec(`
CREATE TABLE IF NOT EXISTS asset_text_track_segments (
    id TEXT PRIMARY KEY,
    track_id TEXT NOT NULL,
    sequence_no INTEGER NOT NULL DEFAULT 0,
    start_ms INTEGER NOT NULL,
    end_ms INTEGER NOT NULL,
    text TEXT NOT NULL,
    text_hash TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(track_id) REFERENCES asset_text_tracks(id) ON DELETE CASCADE
)`)
	require.NoError(t, err, "CREATE TABLE asset_text_track_segments must succeed")

	// Construct TextTrackRepository from the same in-memory DB.
	ttRepo, err := texttrackssql.NewTextTrackRepository(fx.DB, fx.Log)
	require.NoError(t, err, "NewTextTrackRepository must succeed")

	// Wire PayloadMapper with TextTrackQuerier + index_languages so
	// resolveSearchText populates SearchTextInput.TextTracks at
	// indexing time. Mirrors production buildQdrantDeps wiring.
	fx.Mapper.SetTextTrackQuerier(ttRepo)
	fx.Mapper.SetIndexLanguages("en,it")

	// Construct TextTrackResolver for the priority-chain lookup.
	resolver := &youtubeusecase.TextTrackResolver{
		Repo: ttRepo,
		Log:  fx.Log,
	}

	return &textTrackFixture{
		e2eFixture: fx,
		TTRepo:     ttRepo,
		Resolver:   resolver,
	}
}

// ── Test 1: Resolver payload hit + persistence ────────────────────────
//
// Verifies:
//   - TextTrackResolver resolves transcript from payload Texts[] (Priority 1)
//   - Save persists to asset_text_tracks (row created)
//   - TextTrackResolver resolves from DB on second call (Priority 2)
//   - Whisper transcriber is NOT needed (resolver short-circuits)
//
// Fase 1.b (PR-PY-CLIPS-CORRETTE-TRADOTTE): the legacy
// `Resolver.Resolve(ctx, clipID, payloadTexts)` is RETIRED. The
// migration uses the typed methods:
//   - ResolveOriginal (priority 1) returns *detail.ResolvedTextBundle
//   - ResolveBestAvailable (priority 2) returns *detail.TextTrack
//   - A non-existent clip returns (nil, nil) from ResolveBestAvailable
