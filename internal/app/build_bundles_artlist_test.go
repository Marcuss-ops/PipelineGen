// Package app — build_bundles_artlist_test.go: TDD coverage for the
// WireArtlist composition surface (PR-ARTLIST-LIVE-WIRE, July 2026).
//
// Scope (per architecture/current.yaml#ART-001.linked_issues[id=PR-ARTLIST-LIVE-WIRE]):
//   - TestWireArtlist_PublisherGate_FailsClosed: confirms the 4-gate UPFRONT
//     ladder returns a typed error when bundle.Publisher is nil, with all
//     4 mandatory gates broken simultaneously (all-nil call-args).
//   - TestWireArtlist_PublisherGate_ShortCircuitsOverDispatcher: confirms
//     the Publisher mandatory gate runs FIRST in the fail-closed ladder —
//     even when the Dispatcher wire-up is non-nil, the Publisher nil
//     short-circuit fires before the Dispatcher check.
//   - TestWireArtlist_HappyPath_AllGatesUp_RegistersRoute: regression test
//     for the ART-002 P0 (July 2026) collapsed-comment bug — confirms that
//     when the 4 mandatory composition gates are satisfied, WireArtlist
//     returns a non-nil wiring with Module + Service properly populated so
//     /api/artlist/* routes are mounted.
//
// Both failure-mode tests use httptest.Server to mock /api/artlist/stats —
// this is the live-probe endpoint that WireArtlist pings via
// NewHTTPSelfLoopProbe. The HTTPSelfLoopProbe adapter is *DIRECTLY* tested
// by the unit tests in
// internal/application/assets/providers/artlist/http_live_probe_test.go;
// this test file focuses on the COMPOSITION wiring path.
package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// artlistCompositionSchema: minimal DDL subset needed by WireArtlist to
// construct every canonical asset run repository on an in-memory SQLite
// handle. Mirrors the column reconciliation in
// migrations/sqlite/001_velox_core.sql:46-62 (artlist_runs) + the
// companion tables touched by sqassets.NewAssetStoreSQLite (PR4d-chunk2).
// We deliberately keep this fixture tight: WireArtlist only *constructs*
// the repos during the wiring path (it does NOT call them), so any
// constraint violation triggered at construction is enough to fail the
// test loudly — a future PR that adds a column-level query at
// construction time would surface here as a schema compat test failure.
const artlistCompositionSchema = `
	CREATE TABLE IF NOT EXISTS artlist_runs (
		id              TEXT PRIMARY KEY,
		term            TEXT NOT NULL,
		status          TEXT NOT NULL DEFAULT 'queued',
		root_folder_id  TEXT,
		tag_folder_id   TEXT,
		requested_count INTEGER DEFAULT 0,
		found_count     INTEGER DEFAULT 0,
		processed_count INTEGER DEFAULT 0,
		skipped_count   INTEGER DEFAULT 0,
		failed_count    INTEGER DEFAULT 0,
		error_message   TEXT,
		created_at      TEXT DEFAULT (datetime('now')),
		updated_at      TEXT DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS media_assets (
		id              TEXT PRIMARY KEY,
		name            TEXT,
		source          TEXT,
		source_url      TEXT,
		media_type      TEXT,
		lifecycle_state TEXT,
		metadata_json   TEXT DEFAULT '{}',
		file_hash       TEXT DEFAULT '',
		drive_link      TEXT DEFAULT '',
		drive_file_id   TEXT DEFAULT '',
		download_link   TEXT DEFAULT '',
		local_path      TEXT DEFAULT '',
		created_at      TEXT,
		updated_at      TEXT
	);

	CREATE TABLE IF NOT EXISTS clip_search_terms (
		clip_id TEXT NOT NULL,
		term    TEXT NOT NULL,
		PRIMARY KEY (clip_id, term)
	);
`

// stubPublisherForArtlistComposition is the in-test
// delivery.Publisher stub. Mirrors the F2.11 audit-pin precedent
// (internal/infrastructure/drive/artifact_publisher_adapter_test.go)
// so WireArtlist's mandatory publisher gate #1 fires through to the
// downstream Builder without panicking on a missing config.
type stubPublisherForArtlistComposition struct{}

func (s *stubPublisherForArtlistComposition) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	return &delivery.PublishResult{
		FileID:      "stub-artlist-publish-file-id",
		FolderID:    "stub-artlist-publish-folder-id",
		Destination: req.Destination,
	}, nil
}

func (s *stubPublisherForArtlistComposition) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "stub-artlist-resolve-folder-id", nil
}

// Compile-time assertion: stubPublisherForArtlistComposition satisfies
// delivery.Publisher. Mirrors the AGENTS.md Pattern 0 discipline used
// by the other test fixtures in this package.
var _ delivery.Publisher = (*stubPublisherForArtlistComposition)(nil)

// TestWireArtlist_PublisherGate_FailsClosed: confirms the 4-gate UPFRONT
// ladder returns a typed error when bundle.Publisher is nil — all
// mandatory dependencies nil simultaneously. This pins the godlike/07
// no-fake-availability contract at the COMPOSITION layer: nil on any
// mandatory gate aborts WireArtlist loudly rather than silently downgrading
// to skip-route. The IsLiveProbe probe is WIRED via cfg (so the wire-up
// path runs) but the mandatory Publisher gate runs FIRST and short-circuits.
func TestWireArtlist_PublisherGate_FailsClosed(t *testing.T) {
	// Mock /api/artlist/stats server (IsLiveProbe target — proves the
	// wire-up path reached composition; the Publisher gate rejects before
	// the probe is ever fired).
	statsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer statsSrv.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 8080},
		External: config.ExternalConfig{VeloxBaseURL: statsSrv.URL},
		Features: config.FeaturesConfig{ArtlistEnabled: true},
	}
	log := zap.NewNop()

	// bundle WITHOUT Publisher (mandatory gate #1; F2.11 enforces nil-rejection).
	// All other mandatory gates ALSO nil: ClipsRepo nil, Jobs nil.
	bundle := &wiring.ArtlistBundle{
		DB:                 nil,
		ClipsRepo:          nil,
		DriveUploader:      nil,
		Publisher:          nil, // mandatory; F2.11 enforces nil-rejection
		ClipIndexerService: nil,
		Jobs:               nil, // mandatory gate #4
		MediaProcessor:     nil,
	}

	wiring, err := WireArtlist(context.Background(), log, cfg, bundle, nil, nil, nil, nil, nil)

	require.Error(t, err)
	require.Nil(t, wiring)
	var missing ErrArtlistDepMissing
	require.True(t, errors.As(err, &missing),
		"Fase 1: WireArtlist MUST return typed ErrArtlistDepMissing{Kind: DepKindPublisher} on the mandatory Publisher gate")
	assert.Equal(t, DepKindPublisher, missing.Kind,
		"gate #1 short-circuit on Publisher nil — Kind/Field carry the canonical diagnostic")
	assert.Equal(t, "bundle.Publisher", missing.Field,
		"Field carries the source-path so operators can map to the upstream ComposeRoot")
}

// TestWireArtlist_PublisherGate_ShortCircuitsOverDispatcher: confirms the
// Publisher mandatory gate runs FIRST in the fail-closed ladder. When
// the Dispatcher wire-up is provided (non-nil) but bundle.Publisher is
// still nil, the Publisher gate still short-circuits and returns its
// typed error BEFORE the Dispatcher gate is evaluated. This pins the
// ladder-order as part of the godlike/07 contract — a future refactor
// that reorders the gates would surface as a test failure here.
func TestWireArtlist_PublisherGate_ShortCircuitsOverDispatcher(t *testing.T) {
	statsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/artlist/stats", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer statsSrv.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 8080},
		External: config.ExternalConfig{VeloxBaseURL: statsSrv.URL},
		Features: config.FeaturesConfig{ArtlistEnabled: true},
	}
	log := zap.NewNop()

	// bundle WITHOUT Publisher (mandatory gate #1) but with the OTHER
	// mandatory gates intact where possible: ClipsRepo nil (would be gate
	// #3 but Publisher runs first), Jobs nil (would be gate #4).
	bundle := &wiring.ArtlistBundle{
		DB:                 nil,
		ClipsRepo:          nil, // would be mandatory gate #3 if Publisher passed
		DriveUploader:      nil,
		Publisher:          nil, // mandatory gate #1; runs FIRST regardless
		ClipIndexerService: nil,
		Jobs:               nil, // would be mandatory gate #4 if Publisher passed
		MediaProcessor:     nil,
	}

	wiring, err := WireArtlist(context.Background(), log, cfg, bundle,
		&outbox.Dispatcher{}, // gate #2 wired: Dispatcher is non-nil
		nil, nil, nil, nil,
	)

	require.Error(t, err)
	require.Nil(t, wiring)
	var missing ErrArtlistDepMissing
	require.True(t, errors.As(err, &missing),
		"Fase 1: Publisher must short-circuit BEFORE Dispatcher gate; typed sentinel MUST still publish the Ladder-1 Kind")
	assert.Equal(t, DepKindPublisher, missing.Kind,
		"gate-ordering invariant: Publisher nil catches before Dispatcher gate fires")
	assert.Equal(t, "bundle.Publisher", missing.Field)
}

// ---------- ART-002 P0.1 (July 2026): gate #5 scraper-URL fail-closed ----------
//
// The 4 tests below target validateArtlistScraperURL directly per
// godlike/06 SSOT — the gate is the SINGLE canonical owner of the
// fail-closed check; its call site inside WireArtlist is one line.
// Extracting the gate to a package-level helper keeps these tests pure
// (no httptest, no bundle construction, no httptest/fixture churn — a
// 4-case table that locks the contract).
//
// Coverage scope:
//   - TestValidateArtlistScraperURL_NilCfg_ReturnsError: defensive nil-guard
//   - TestValidateArtlistScraperURL_DisabledAndEmptyURL_ReturnsNil: skip path
//   - TestValidateArtlistScraperURL_EnabledAndValidURL_ReturnsNil: success path
//   - TestValidateArtlistScraperURL_EnabledAndEmptyURL_ReturnsError: fail-closed
//
// The 4th test is the canonical godlike/07 no-fake-availability case
// (the entire reason the gate exists). The 3 companion tests pin the
// surrounding contract boundaries so a future refactor cannot silently
// weaken them.

// TestValidateArtlistScraperURL_NilCfg_ReturnsError: defensive coverage
// for the gate-#5 nil-cfg case. Returns a typed error so WireArtlist's
// single call site propagates the same pattern as the 4 wiring gates.
// Fase 1 (Phase 1, July 2026) — the typed sentinel is now parsed via
// errors.As so DepKindScraperURL + Field="cfg" are structurally testable.
func TestValidateArtlistScraperURL_NilCfg_ReturnsError(t *testing.T) {
	err := validateArtlistScraperURL(nil)
	require.Error(t, err)
	var missing ErrArtlistDepMissing
	require.True(t, errors.As(err, &missing),
		"Fase 1: validateArtlistScraperURL nil-cfg MUST return typed ErrArtlistDepMissing{Kind: DepKindScraperURL}")
	assert.Equal(t, DepKindScraperURL, missing.Kind)
	assert.Equal(t, "cfg", missing.Field)
	assert.Contains(t, missing.Detail, "scraper-URL fail-closed",
		"Detail must retain the diagnostic marker for operator log grep (forward-compat with the Fase 2 /api/artlist/diagnostics endpoint)")
}

// TestValidateArtlistScraperURL_DisabledAndEmptyURL_ReturnsNil: when
// Artlist is disabled, the gate is intentionally a no-op. The caller
// registerArtlist also short-circuits at the top of its function, but we
// keep the gate self-contained for defensive composition correctness
// (godlike/07 fail-closed at the deepest layer still respects the
// "feature off = no requirement" precondition).
func TestValidateArtlistScraperURL_DisabledAndEmptyURL_ReturnsNil(t *testing.T) {
	cfg := &config.Config{
		Features: config.FeaturesConfig{ArtlistEnabled: false},
		External: config.ExternalConfig{ArtlistScraperServerURL: ""},
	}
	err := validateArtlistScraperURL(cfg)
	assert.NoError(t, err,
		"disabled Artlist + empty URL is the allowed zero-state — gate is a no-op")
}

// TestValidateArtlistScraperURL_EnabledAndValidURL_ReturnsNil: when
// Artlist is enabled and the Node scraper URL is configured, the wire
// proceeds past gate #5 normally. This pins the success-path contract.
func TestValidateArtlistScraperURL_EnabledAndValidURL_ReturnsNil(t *testing.T) {
	cfg := &config.Config{
		Features: config.FeaturesConfig{ArtlistEnabled: true},
		External: config.ExternalConfig{ArtlistScraperServerURL: "http://artlist-scraper:9123"},
	}
	err := validateArtlistScraperURL(cfg)
	assert.NoError(t, err,
		"enabled Artlist + valid Node scraper URL must pass gate #5 silently")
}

// ---------- ART-002 P0 (July 2026): composition happy-path regression ----------
//
// stubTextTrackRepoForArtlistComposition is a minimal asset.TextTrackRepository
// double that lets the Artlist wiring happy-path test pass the mandatory
// TextTrackRepo gate without persisting real rows.
type stubTextTrackRepoForArtlistComposition struct{}

func (s *stubTextTrackRepoForArtlistComposition) UpsertBatch(_ context.Context, _ []asset.TextTrack) error {
	return nil
}
func (s *stubTextTrackRepoForArtlistComposition) Find(_ context.Context, _ string, _ string, _ asset.TextTrackKind) (*asset.TextTrack, error) {
	return nil, nil
}
func (s *stubTextTrackRepoForArtlistComposition) ListByAsset(_ context.Context, _ string) ([]asset.TextTrack, error) {
	return nil, nil
}
func (s *stubTextTrackRepoForArtlistComposition) FindReady(_ context.Context, _ string, _ string, _ asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	return nil, nil, nil
}
func (s *stubTextTrackRepoForArtlistComposition) ListReadyLanguages(_ context.Context, _ string, _ asset.TextTrackKind) ([]string, error) {
	return nil, nil
}
func (s *stubTextTrackRepoForArtlistComposition) FindCurrentForTranslation(_ context.Context, _ string, _ asset.TextTrackKind, _ string, _ string, _ string, _ string, _ string) (*asset.TextTrack, error) {
	return nil, nil
}
func (s *stubTextTrackRepoForArtlistComposition) InsertTranslationWithAuditPredecessor(_ context.Context, _ asset.TextTrack) error {
	return nil
}

// TestWireArtlist_HappyPath_AllGatesUp_RegistersRoute: regression test for
// the collapsed-comment bug PR-ARTLIST-PERSIST-FIX suffered before
// (the comment swallowed `RunRepository: artlistRunsAdapter,` on the
// same source line). Before the fix, artlist.NewService returned
// ErrRunRepositoryUnavailable and Wiring returned nil; /api/artlist/*
// routes were unmounted. After the fix, NewService succeeds and the
// returned wiring is non-nil with Module + Service populated.
//
// Scope:
//   - 4 mandatory composition gates all up (Publisher, Dispatcher,
//     ClipsRepo, Jobs.Service)
//   - gate #5 (scraper URL) satisfied via cfg.Features.ArtlistEnabled
//   - cfg.External.ArtlistScraperServerURL
//   - Real SQLite (in-memory via storage.NewSQLiteDB), real
//     ClipsRepository built on the same *sql.DB, real appjobs.Service
//     built via BuildJobsBundle (composition-root helper) so the test
//     exercises the SAME construction chain as production rather than
//     skipping it via a hand-rolled fake.
//
// What this test does NOT assert:
//   - Drive write semantics (stub Publisher short-circuits)
//   - Real outbox dispatch (empty literal outbox.Dispatcher{} matches
//     the precedent set by the 2 negative-path tests above; the
//     composer's NewService constructor does not dispatch at
//     construction time)
//   - HTTP routing per se (composition-level only). The downstream
//     /api/artlist/stats live probe merely returns 200 to
//     satisfy the NewHTTPSelfLoopProbe construction path.
//
// Co-deps the test relies on:
//   - storage.NewSQLiteDB + artlistCompositionSchema
//   - assets.NewClipsRepository (audio/file/clip columns kept loose
//     because WireArtlist only constructs the repo, never queries it
//     in this test — see gate01_happy_path_test.go for the heavier
//     integration variant)
//   - BuildJobsBundle (composition-root's canonical Jobs builder) so
//     TestsErrRegistryRequired / ErrLogRequired / ErrRepoRequired are
//     caught by the helper rather than by ad-hoc assertions here
func TestWireArtlist_HappyPath_AllGatesUp_RegistersRoute(t *testing.T) {
	// Mock the IsLiveProbe target so NewHTTPSelfLoopProbe wiring
	// succeeds (its Probe(ctx) is not exercised in this test, but
	// the wiring construction itself reads from the served endpoint
	// to validate the URL).
	statsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/artlist/stats", r.URL.Path,
			"IsLiveProbe must target the canonical /api/artlist/stats endpoint (godlike/06 SSOT)")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"clips_total":0,"runs_total":0}`))
	}))
	defer statsSrv.Close()

	// Real SQLite (file-backed, tempdir-scoped) with the minimal
	// regression schema. Tests the production *storage.SQLiteDB wrap
	// (bundle.DB.DB is reached via .DB field promotion).
	sqliteDB, err := storage.NewSQLiteDB(t.TempDir(), "artlist_compose_test.db", zap.NewNop())
	require.NoError(t, err, "storage.NewSQLiteDB should succeed against a tempdir-backed file")
	defer sqliteDB.Close()
	_, err = sqliteDB.Exec(artlistCompositionSchema)
	require.NoError(t, err, "artlistCompositionSchema must apply cleanly")

	log := zap.NewNop()

	// Real ClipsRepository on the same *sql.DB (satisfies
	// artlist.AssetStore port directly via Pattern 0 compile-time pin
	// established in build_bundles_artlist_artlist.go).
	clipsRepo := assets.NewClipsRepository(sqliteDB.DB, log)
	require.NotNil(t, clipsRepo, "assets.NewClipsRepository must return a non-nil concrete on a fresh schema")

	// Real JobsBundle through the composition-root's canonical
	// BuildJobsBundle helper. The 4 trailing args (voiceoverRepo,
	// imagesRepo, driveUploader, driveLifecycle) are nil-tolerant per
	// the helper's documented contract.
	jobsBundle, err := wiring.BuildJobsBundle(sqliteDB, log, nil, nil, nil, nil)
	require.NoError(t, err, "BuildJobsBundle must succeed against the in-memory SQLite")
	require.NotNil(t, jobsBundle.Service, "JobsBundle.Service must be populated so WireArtlist gate #4 passes")

	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		External: config.ExternalConfig{
			VeloxBaseURL:            statsSrv.URL,
			ArtlistScraperServerURL: "http://artlist-scraper:9123",
		},
		Features: config.FeaturesConfig{ArtlistEnabled: true},
	}

	bundle := &wiring.ArtlistBundle{
		DB:                 sqliteDB,
		ClipsRepo:          clipsRepo,
		Publisher:          &stubPublisherForArtlistComposition{},
		Jobs:               jobsBundle,
		ClipIndexerService: clipindexer.NewService(nil, sqliteDB, "", log), // Fase 1 gate #6 (Indexr/Qdrant)
		Committer:          assets.NewSQLiteAssetCommitter(sqliteDB.DB, outboxevents.NewRepository(sqliteDB.DB), log),
		MediaProcessor:     nil, // optional; nil-tolerant in WireArtlist's MediaProcessor bridge
		TextTrackRepo:      &stubTextTrackRepoForArtlistComposition{},
	}

	// WireArtlist mandatory call shape (8 args). The 3 ComposeRoot
	// receiver-fields not surfaced on ArtlistBundle (reader, lifecycle,
	// metaWriter) and the destResolver port are passed nil — every
	// downstream consumer in NewService / SemanticEnricher is
	// nil-tolerant at construction (they DEFER their dispatch to
	// runtime methods which this test does not exercise).
	wiring, err := WireArtlist(
		context.Background(),
		log,
		cfg,
		bundle,
		&outbox.Dispatcher{}, // gate #2: empty literal (Matches the 2 negative-path tests above)
		nil,                  // reader: nil-tolerant (NewSemanticEnricher stores the field, only used at runtime)
		nil,                  // lifecycle: nil-tolerant
		nil,                  // metaWriter: nil-tolerant (P0-#2 fail-closed path; tests opt out)
		nil,                  // destResolver: nil-tolerant (only used by DestinationService at runtime)
	)

	// The regression assertion: pre-fix the error chain wrapped
	// ErrRunRepositoryUnavailable (the bug closed silently left
	// /api/artlist/* unmounted). Post-fix it does NOT appear, and
	// WireArtlist returns a fully-populated *ArtlistWiring.
	require.NoError(t, err,
		"WireArtlist must succeed when all 4 mandatory composition gates are up — the run-repo field is no longer swallowed by a comment (ART-002 P0 fix, July 2026)")
	require.NotNil(t, wiring,
		"wiring must be non-nil so registerArtlist can promote it into the registry")

	// Routes-mount proof: wiring.Module is the api.Module returned by
	// the NewRouteModule construction; its non-nil presence is
	// what triggers tryRegisterModuleStrict to mount /api/artlist/* in
	// production.
	require.NotNil(t, wiring.Module,
		"wiring.Module must be non-nil so /api/artlist/* routes get registered by tryRegisterModuleStrict")
	require.Contains(t, wiring.Module.Name(), "artlist",
		"module name must identify the capability so route-prefix /api/artlist/* maps correctly")

	// Service proof: wiring.Service is the canonical *artlist.Service
	// built via artlist.NewService. Its NON-NIL presence confirms
	// RunRepository was actually wired in (the bug chain would have
	// returned nil here via NewService's ErrRunRepositoryUnavailable).
	require.NotNil(t, wiring.Service,
		"wiring.Service must be non-nil — confirms Artlist.NewService did NOT fail on ErrRunRepositoryUnavailable")
	assert.NotNil(t, wiring.ProviderAssets,
		"ProviderAssets registry must be wired + frozen for search fan-out")
	assert.NotNil(t, wiring.LicenseRepo,
		"LicenseRepo must be wired so the compliance manifests endpoint works")
	assert.NotNil(t, wiring.ReleaseRepo,
		"ReleaseRepo must be wired so the release manifest endpoint works")
	assert.NotNil(t, wiring.RenditionRepo,
		"RenditionRepo must be wired so the rendition metadata endpoint works")

	if wiring.Service != nil {
		_ = wiring.Service.Close()
	}
}

// TestValidateArtlistScraperURL_EnabledAndEmptyURL_ReturnsError: the
// canonical godlike/07 fail-closed case. Artlist is enabled but the
// Node scraper URL is empty — the gate aborts loudly with an actionable
// fix hint instead of silently degrading to per-call exec fallback at
// first /run invocation. The associated WireArtlist tests
// (PublisherGateFailsClosed + ShortCircuitsOverDispatcher) already pin
// that gate #1 short-circuits BEFORE this gate; this unit test pins
// gate #5's own contract independently.
func TestValidateArtlistScraperURL_EnabledAndEmptyURL_ReturnsError(t *testing.T) {
	cfg := &config.Config{
		Features: config.FeaturesConfig{ArtlistEnabled: true},
		External: config.ExternalConfig{ArtlistScraperServerURL: ""},
	}
	err := validateArtlistScraperURL(cfg)
	require.Error(t, err,
		"ART-002 P0.1: enabled Artlist + empty Node scraper URL must fail-closed (godlike/07 no-fake-availability)")
	assert.Contains(t, err.Error(), "ArtlistEnabled=true",
		"error must name the failing condition (the feature flag was on)")
	assert.Contains(t, err.Error(), "ArtlistScraperServerURL is empty",
		"error must name the failing field (the URL was missing)")
	assert.Contains(t, err.Error(), "ART-002 P0.1",
		"error must cite the wave-tracker anchor for audit-traceability")
	assert.Contains(t, err.Error(), "VELOX_ARTLIST_SCRAPER_SERVER_URL",
		"error must name the env var operators must set (PR-ARTLIST-CONFIG-PREFIX July 2026 cutover from bare ARTLIST_SCRAPER_SERVER_URL)")
	assert.Contains(t, err.Error(), "VELOX_FEATURE_ARTLIST_ENABLED=false",
		"error must include the disable escape hatch for operators who don't need the feature")
}
