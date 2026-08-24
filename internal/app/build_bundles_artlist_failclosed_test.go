// Package app — build_bundles_artlist_failclosed_test.go: table-driven
// per-dep fail-closed coverage for the Artlist composition root's
// 8 mandatory gates (Phase 1, Fase 1, July 2026).
//
// Purpose (per user-spec literal "Aggiungi test che dimostrano il
// fail-closed eliminando una dipendenza alla volta"):
//   - TestWireArtlist_FailClosed_AllMandatoryGates: table-driven, mutates
//     ONE wiring dep at a time, asserts typed ErrArtlistDepMissing{Kind,
//     Field} fires.
//   - TestWireArtlist_FailClosed_BundleNil: separate case (gate #1); the
//     table mutates FIELDS of an existing bundle, not the bundle itself.
//   - TestWireArtlist_FailClosed_ScraperURLCfgNil + ScraperURLEnabledAnd
//     EmptyURL: gate #8 (validateArtlistScraperURL sub-cases) at the
//     WireArtlist ladder integration surface.
//   - TestWireArtlist_FinalizerGate_SourceLevelContract: parity with
//     TestRegisterArtlist_NoSilentWarnOnJobBindFailure (pr2_test.go);
//     the canonical NewAssetTxFinalizer never returns nil today so
//     the gate cannot fire at test runtime — the source-level
//     literal assertion pins the contract for future regressions.
//
// Composition-time "fail fast at boot" (godlike/07) is verified per
// each gate. The verify-script side at tests/operational/artlist/
// run_all.sh validates end-to-end runtime behavior (the operator-only
// battery surface, Fase 14 follow-up).
package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// happyPathWireArtlistArgs bundles the 8 fully-populated inputs to
// WireArtlist for the per-dep table-test. Position 6 / 7 / 8 / 9
// (reader / lifecycle / metaWriter / destResolver) are runtime-nil
// tolerant per the production happy-path test fixture
// (build_bundles_artlist_test.go line 369) so the table mutator
// only needs to operate on the 4 hard-gate fields + the dispatcher
// parameter.
type happyPathWireArtlistArgs struct {
	cfg        *config.Config
	bundle     *wiring.ArtlistBundle
	dispatcher *outbox.Dispatcher
	log        *zap.Logger
}

// newHappyPathWireArtlistArgs constructs the 8-slot happy-path batch:
//
//   - tempdir-backed SQLite with artlistCompositionSchema applied (real
//     tables so jobs/clips repos construct cleanly).
//   - real ClipsRepository on the same SQLite (Pattern 0 compile-time
//     pin: *assets.ClipsRepository satisfies artlist.AssetStore 1:1).
//   - real JobsBundle via BuildJobsBundle (composition-root's canonical
//     Jobs helper so test exercises the SAME construction chain as
//     production).
//   - stub Publisher (delivery.Publisher) so gate #2 is satisfied.
//   - clipindexer.NewService stub so gate #6 is satisfied (the
//     constructor's empty-dbPath + nil-cfg defaults exercise the
//     minimum-service surface — no probe path runs).
//   - cfg with ArtlistEnabled=true + valid Node scraper URL.
//   - httptest stats server so NewHTTPSelfLoopProbe wire-up rounds
//     cleanly (Probe(ctx) is not exercised; URL roundtrip is read).
func newHappyPathWireArtlistArgs(t *testing.T) *happyPathWireArtlistArgs {
	t.Helper()

	// Mock the IsLiveProbe target so NewHTTPSelfLoopProbe wiring
	// succeeds (its Probe(ctx) is not exercised here, but the
	// wiring construction itself reads the served URL).
	statsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/artlist/stats", r.URL.Path,
			"IsLiveProbe must target the canonical /api/artlist/stats endpoint (godlike/06 SSOT)")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"clips_total":0,"runs_total":0}`))
	}))
	t.Cleanup(statsSrv.Close)

	log := zap.NewNop()

	// Real SQLite (file-backed, tempdir-scoped) with the minimal
	// regression schema.
	sqliteDB, err := storage.NewSQLiteDB(t.TempDir(), "artlist_failclosed_test.db", log)
	require.NoError(t, err, "storage.NewSQLiteDB must succeed against a tempdir-backed file")
	t.Cleanup(func() { _ = sqliteDB.Close() })
	_, err = sqliteDB.Exec(artlistCompositionSchema)
	require.NoError(t, err, "artlistCompositionSchema must apply cleanly")

	// Real ClipsRepository on the same *sql.DB.
	clipsRepo := assets.NewClipsRepository(sqliteDB.DB, log)
	require.NotNil(t, clipsRepo,
		"assets.NewClipsRepository must return a non-nil concrete on a fresh schema (Phase 1 gate #4)")

	// Real JobsBundle through the composition-root's canonical
	// BuildJobsBundle helper.
	jobsBundle, err := wiring.BuildJobsBundle(sqliteDB, log, nil, nil, nil, nil)
	require.NoError(t, err, "BuildJobsBundle must succeed against the in-memory SQLite")
	require.NotNil(t, jobsBundle.Service,
		"JobsBundle.Service must be populated so WireArtlist gate #5 passes")

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
		// MediaProcessor / AssetIndexService / DestinationService /
		// AssetLocRepo / AssetVerRepo are intentionally nil: the
		// production WireArtlist treats them as runtime-nil-tolerant
		// (NOT hard gates — see build_bundles_artlist.go godoc on
		// "Documented-not-gated-by-design").
	}

	return &happyPathWireArtlistArgs{
		cfg:        cfg,
		bundle:     bundle,
		dispatcher: &outbox.Dispatcher{}, // gate #3 satisfied: non-nil pointer to zero-value
		log:        log,
	}
}

// TestWireArtlist_FailClosed_AllMandatoryGates: table-driven per-dep
// coverage of the 5 hard wiring gates (gates #2–6) eliminating one
// dependency at a time. Each row nil-replaces a single field; the
// expected ErrArtlistDepMissing{Kind, Field} is verified via errors.As.
//
// Note: gate #1 (bundle==nil) is covered separately at TestWireArtlist_
// FailClosed_BundleNil because the table mutates FIELDS of an EXISTING
// bundle, not the bundle pointer itself; gate #8 (ScraperURL) is
// covered by 2 sub-cases configured via cfg variants; gate #7
// (Finalizer) is covered by TestWireArtlist_FinalizerGate_Source
// LevelContract (the constructor never returns nil in production,
// so runtime tests cannot verify it — the source-level literal
// matches the precedent set by TestRegisterArtlist_NoSilentWarnOn
// JobBindFailure).
func TestWireArtlist_FailClosed_AllMandatoryGates(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*happyPathWireArtlistArgs)
		wantKind  DepKind
		wantField string
	}{
		{
			name: "Publisher nil (gate #2)",
			mutate: func(h *happyPathWireArtlistArgs) {
				h.bundle.Publisher = nil
			},
			wantKind:  DepKindPublisher,
			wantField: "bundle.Publisher",
		},
		{
			name: "Dispatcher nil (gate #3)",
			mutate: func(h *happyPathWireArtlistArgs) {
				h.dispatcher = nil
			},
			wantKind:  DepKindDispatcher,
			wantField: "dispatcher",
		},
		{
			name: "ClipsRepo nil (gate #4)",
			mutate: func(h *happyPathWireArtlistArgs) {
				h.bundle.ClipsRepo = nil
			},
			wantKind:  DepKindClipsRepo,
			wantField: "bundle.ClipsRepo",
		},
		{
			name: "JobsService nil (gate #5)",
			mutate: func(h *happyPathWireArtlistArgs) {
				h.bundle.Jobs = nil
			},
			wantKind:  DepKindJobsService,
			wantField: "bundle.Jobs.Service",
		},
		{
			name: "Indexer nil (gate #6)",
			mutate: func(h *happyPathWireArtlistArgs) {
				h.bundle.ClipIndexerService = nil
			},
			wantKind:  DepKindIndexer,
			wantField: "bundle.ClipIndexerService",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHappyPathWireArtlistArgs(t)
			tc.mutate(h)
			_, err := WireArtlist(
				context.Background(),
				h.log,
				h.cfg,
				h.bundle,
				h.dispatcher,
				nil, // reader: nil-tolerant per production happy-path
				nil, // lifecycle: nil-tolerant
				nil, // metaWriter: nil-tolerant
				nil, // destResolver: nil-tolerant
			)
			require.Error(t, err,
				"Fase 1: per-dep nil MUST fail-closed at composition; gate-ordering ensures the failing dep's Kind+Field surface verbatim")
			var missing ErrArtlistDepMissing
			require.True(t, errors.As(err, &missing),
				"Fase 1: WireArtlist MUST return typed ErrArtlistDepMissing sentinel — not fmt.Errorf/stringly-typed")
			assert.Equal(t, tc.wantKind, missing.Kind,
				"Kind=canonical wire-id tag; matches the typed constant for grep/log scan")
			assert.Equal(t, tc.wantField, missing.Field,
				"Field=source-path identifier so operators can map to the upstream ComposeRoot path")
		})
	}
}

// TestWireArtlist_FailClosed_BundleNil: gate #1 cover — the entire
// ArtlistBundle pointer is nil. The table-test mutates FIELDS of an
// EXISTING bundle; nil-ing the bundle itself is handled here as a
// separate case.
func TestWireArtlist_FailClosed_BundleNil(t *testing.T) {
	h := newHappyPathWireArtlistArgs(t)
	_, err := WireArtlist(
		context.Background(), h.log, h.cfg, nil,
		h.dispatcher, nil, nil, nil, nil,
	)
	require.Error(t, err,
		"Fase 1: bundle==nil MUST fire gate #1 in the WireArtlist ladder")
	var missing ErrArtlistDepMissing
	require.True(t, errors.As(err, &missing),
		"Fase 1: gate #1 must return typed ErrArtlistDepMissing{Kind: DepKindRunRepo}")
	assert.Equal(t, DepKindRunRepo, missing.Kind)
	assert.Equal(t, "bundle", missing.Field)
}

// TestWireArtlist_FailClosed_ScraperURLCfgNil: gate #8 sub-case — cfg
// itself is nil. Validates WireArtlist threads nil cfg through the
// validateArtlistScraperURL helper correctly.
func TestWireArtlist_FailClosed_ScraperURLCfgNil(t *testing.T) {
	h := newHappyPathWireArtlistArgs(t)
	_, err := WireArtlist(
		context.Background(), h.log, nil, h.bundle,
		h.dispatcher, nil, nil, nil, nil,
	)
	require.Error(t, err,
		"Fase 1: nil cfg MUST propagate from WireArtlist through validateArtlistScraperURL as a typed sentinel")
	var missing ErrArtlistDepMissing
	require.True(t, errors.As(err, &missing),
		"Fase 1: validateArtlistScraperURL nil-cfg MUST surface typed ErrArtlistDepMissing{Kind: DepKindScraperURL}")
	assert.Equal(t, DepKindScraperURL, missing.Kind)
	assert.Equal(t, "cfg", missing.Field)
	assert.Contains(t, missing.Detail, "scraper-URL fail-closed",
		"Detail must retain the diagnostic marker for operator log grep")
}

// TestWireArtlist_FailClosed_ScraperURLEnabledAndEmptyURL: gate #8
// sub-case — Artlist feature is enabled but Node scraper URL is empty.
// This is the operational godlike/07 fail-closed invariant: without
// this gate, the searcher chain silently degrades to per-call exec
// fallback at first /run invocation rather than aborting at boot.
func TestWireArtlist_FailClosed_ScraperURLEnabledAndEmptyURL(t *testing.T) {
	h := newHappyPathWireArtlistArgs(t)
	h.cfg.External.ArtlistScraperServerURL = "" // re-introduce godlike/07 trigger
	_, err := WireArtlist(
		context.Background(), h.log, h.cfg, h.bundle,
		h.dispatcher, nil, nil, nil, nil,
	)
	require.Error(t, err,
		"Fase 1: enabled + empty URL MUST abort at composition (godlike/07 no-fake-availability)")
	var missing ErrArtlistDepMissing
	require.True(t, errors.As(err, &missing))
	assert.Equal(t, DepKindScraperURL, missing.Kind)
	assert.Equal(t, "cfg.External.ArtlistScraperServerURL", missing.Field)
	// Detail must retain the operator-fix hint string verbatim so
	// production-log greppers find the env-var names.
	assert.Contains(t, missing.Detail, "ART-002 P0.1")
	assert.Contains(t, missing.Detail, "VELOX_ARTLIST_SCRAPER_SERVER_URL")
	assert.Contains(t, missing.Detail, "VELOX_FEATURE_ARTLIST_ENABLED=false")
}

// TestWireArtlist_FinalizerGate_SourceLevelContract: source-level
// assertion that the gate's nil-discard check is present at the
// expected position in build_bundles_artlist.go.
//
// Companion to TestRegisterArtlist_NoSilentWarnOnJobBindFailure
// (build_bundles_artlist_pr2_test.go) which uses the same source-
// level pattern. NewAssetTxFinalizer returns non-nil today so the
// gate cannot fire at test runtime without modifying the finalizer
// package; the source-level literal pin prevents a future refactor
// from silently regressing the fail-closed invariant.
func TestWireArtlist_FinalizerGate_SourceLevelContract(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller(0) must succeed for this test file")
	// Composition-root 4-file split (July 2026): WireArtlist moved to
	// build_bundles_artlist_artlist.go (the file that owns the function
	// post-split). The Finalizer gate #7 is wired INSIDE the WireArtlist
	// function body (not as a helper), so this test points at the file
	// that owns WireArtlist.
	sourcePath := filepath.Join(filepath.Dir(thisFile), "build_bundles_artlist_artlist.go")

	body, err := os.ReadFile(sourcePath)
	require.NoError(t, err, "build_bundles_artlist_artlist.go must be readable at "+sourcePath)
	src := string(body)

	require.Contains(t, src, `finalizerTx := assetfinalizer.NewAssetTxFinalizer(log, bundle.Committer)`,
		"Fase 1: Finalizer gate MUST construct via the canonical helper; lock-step with composition fail-closed")
	require.Contains(t, src, `if finalizerTx == nil`,
		"Fase 1: Finalizer gate nil-discard MUST be present so a future NewAssetTxFinalizer returning nil aborts boot (godlike/07)")
	require.Contains(t, src, `Kind: DepKindFinalizer,`,
		"Fase 1: Finalizer gate MUST surface the typed DepKindFinalizer constant for diagnostic structured-log matching")
}

// NOTE: the compile-time assertion `var _ delivery.Publisher =
// (*stubPublisherForArtlistComposition)(nil)` is intentionally absent in
// this file — the assertion is already present at
// build_bundles_artlist_test.go line ~91 (same package, same fixture
// type) and is not redistributed. Adding the duplicate here would
// only generate dead-weight noise per the code-reviewer verdict
// (MUST-FIX: none; NICE-TO-HAVE #1 applied).
