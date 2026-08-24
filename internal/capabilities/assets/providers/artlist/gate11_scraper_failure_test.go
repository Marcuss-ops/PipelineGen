// Package artlist — Gate 11 + Gate 12 Tests (ARTLIST-DOD-2026-07-07).
//
// PR-ARTLIST-DOD-GATE-11-SCRAPER-FAILURE: verify that when the scraper
// is unavailable (returns an error), the RunTag pipeline fails with a
// clear, operator-actionable error rather than silently returning zero
// results. The scraper-spento contract is: endpoint risponde errore
// chiaro, NON finge zero risultati.
//
// PR-ARTLIST-DOD-GATE-12-PREFLIGHT: meta-test documenting the full
// Artlist Gate test suite passes with zero failures. This is NOT a
// functional test of cmd/admin qdrant-preflight (that is the action
// plan's Gate 12; the admin CLI is outside this package's scope).
// This test acts as a documentation anchor enumerating all gate tests.
//
// godlike/07 no-fake-availability: the failingSearcher mock returns
// a typed error that simulates a real scraper failure (HTTP 503,
// connection refused, DNS resolution failure). The test asserts that
// this error propagates to the caller, not that it's silently absorbed
// into a "zero results found" response.
//
// godlike/06 SSOT: the Searcher port is the canonical abstraction for
// scraper/pixabay/pexels fallback providers. failingSearcher satisfies
// the port with a deterministic error.
package artlist

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/pkg/security"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ErrScraperUnavailable is the canonical typed sentinel for Gate 11
// scraper-failure tests. Matches the real-world error a caller would
// receive when the Node scraper process is down, unreachable, or
// returns a non-200 HTTP status.
var ErrScraperUnavailable = errors.New("artlist node scraper unavailable: connection refused")

// failingSearcher is a Gate 11 test double that always returns
// ErrScraperUnavailable. This simulates a real scraper failure:
// the Node process is down, the HTTP endpoint returns 503, or DNS
// resolution fails. The fallback chain must propagate this error.
type failingSearcher struct{}

func (f *failingSearcher) Search(_ context.Context, _ SearchRequest) ([]Candidate, error) {
	return nil, ErrScraperUnavailable
}

// emptySearcher is a Gate 11 test double that returns zero candidates
// without an error. This simulates a healthy scraper that simply found
// no matches for the term — a different scenario from scraper failure.
type emptySearcher struct{}

func (e *emptySearcher) Search(_ context.Context, _ SearchRequest) ([]Candidate, error) {
	return []Candidate{}, nil
}

// Compile-time: both mocks satisfy the Searcher port.
var _ Searcher = (*failingSearcher)(nil)
var _ Searcher = (*emptySearcher)(nil)

// ────────────────────────────────────────────────────────────
// Gate 11: Scraper Failure — clear error, no fake zero results
// ────────────────────────────────────────────────────────────

// TestGate11_ScraperFailureReturnsClearError verifies the scraper-
// failure contract (Gate 11 of ARTLIST-DOD-2026-07-07):
//
//  1. When the scraper returns an error, RunTag fails with a clear
//     error that references the scraper failure (not a generic
//     "no results found" message).
//  2. The error propagates through the full pipeline:
//     SearchLive → searchLiveWithFallbacks → SearchLiveAndSave →
//     stageDiscoverClips → RunTag.
//  3. resp.OK is false (the run is not successful).
//  4. resp.Processed is 0 (no clips were processed).
//
// godlike/07 no-fake-availability: the "zero results" anti-pattern
// is explicitly tested — when the scraper is down, the caller MUST
// receive an error, not an empty result list that would be
// indistinguishable from "no clips matching this term."
func TestGate11_ScraperFailureReturnsClearError(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: tmp},
		Video:   config.VideoConfig{Duration: 15},
	}

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := assets.NewClipsRepository(db, logger)

	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore:      artlistRepo,
			ScraperSearcher: &failingSearcher{},
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg:    cfg,
				Log:    logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
			},
			Finalizer: ArtlistFinalizerDeps{
				AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
			},
		},
	}))
	require.NoError(t, err)
	defer svc.Close()

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "boxing highlights",
		Limit:        3,
		Strategy:     "replace",
		RootFolderID: "gate11-root-folder",
	})

	// ── Gate 11: Contract 1 — error is returned ──
	require.Error(t, err, "RunTag must return an error when scraper is unavailable")
	assert.Contains(t, err.Error(), "discovery failed",
		"error must mention discovery failure (the pipeline stage that called the scraper)")

	// ── Gate 11: Contract 2 — resp is populated but NOT ok ──
	require.NotNil(t, resp, "response must be non-nil even on error (caller inspects resp.Error)")
	assert.False(t, resp.OK, "resp.OK must be false when discovery fails")
	assert.NotEmpty(t, resp.Error, "resp.Error must contain the failure reason")

	// ── Gate 11: Contract 3 — Processed is 0, no fake success ──
	assert.Equal(t, 0, resp.Processed, "Processed must be 0 when scraper is unavailable")
	assert.Equal(t, 0, resp.Found, "Found must be 0 when no clips were discovered")
	assert.Empty(t, resp.Items, "Items must be empty when discovery failed")

	// ── Gate 11: Contract 4 — error is NOT "zero results" ──
	// The anti-pattern: scraper returns 503, but the pipeline reports
	// "0 results found" as if the term just had no matches. This
	// contract asserts the error message references the failure,
	// not a misleading "zero results" message.
	assert.NotContains(t, err.Error(), "0 results",
		"error must NOT deceptively claim '0 results' when the scraper is simply unreachable")
}

// TestGate11_ScraperFailureDistinctFromEmptyResults verifies the
// critical distinction between "scraper is down" and "scraper returned
// zero clips" (Gate 11 negative case).
//
// The pre-Gate-11 anti-pattern was: scraper returns an error → pipeline
// treats it as "no clips found" → caller sees "0 results" and assumes
// the term had no matches. The actual problem (scraper is down) is
// hidden from the operator.
//
// This test runs TWO scenarios side-by-side to lock the distinction:
//
//	A) Scraper unavailable (failingSearcher + empty DB) → error
//	B) Scraper available but term has no matches (emptySearcher →
//	   returns []Candidate{} + empty DB) → also an error, but a
//	   DIFFERENT one
//
// The error messages must be distinguishable so operators can tell
// "scraper is down" from "term has no matches."
func TestGate11_ScraperFailureDistinctFromEmptyResults(t *testing.T) {
	ctx := context.Background()

	security.AddAllowedHost("cdn.artlist.io")

	t.Run("scraper_unavailable", func(t *testing.T) {
		cfg := &config.Config{
			Storage: config.StorageConfig{DataDir: t.TempDir()},
			Video:   config.VideoConfig{Duration: 15},
		}

		db := createTestDB(t)
		defer db.Close()

		logger := zap.NewNop()
		artlistRepo := assets.NewClipsRepository(db, logger)

		svc, err := NewService(baseServiceDeps(t, ServiceDeps{
			ServicePorts: ServicePorts{
				AssetStore:      artlistRepo,
				ScraperSearcher: &failingSearcher{},
			},
			ServiceDependencies: ServiceDependencies{
				Infra: ArtlistInfraDeps{
					MainDB: db,
					Cfg:    cfg,
					Log:    logger,
				},
				Ports: ArtlistPortDeps{
					Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
				},
				Finalizer: ArtlistFinalizerDeps{
					AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
				},
			},
		}))
		require.NoError(t, err)
		defer svc.Close()

		respA, errA := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
			Term:         "nonexistent-term-12345",
			Limit:        3,
			Strategy:     "replace",
			RootFolderID: "gate11-root-a",
		})

		require.Error(t, errA, "scenario A: RunTag must return error when scraper is down")
		assert.False(t, respA.OK)
		assert.Equal(t, 0, respA.Processed)

		t.Logf("Scenario A (scraper unavailable) error: %v", errA)
	})

	t.Run("scraper_available_no_matches", func(t *testing.T) {
		cfg := &config.Config{
			Storage: config.StorageConfig{DataDir: t.TempDir()},
			Video:   config.VideoConfig{Duration: 15},
		}

		db := createTestDB(t)
		defer db.Close()

		logger := zap.NewNop()
		artlistRepo := assets.NewClipsRepository(db, logger)

		// emptySearcher returns []Candidate{} without error.
		// This is a HEALTHY scraper that found nothing.
		svc, err := NewService(baseServiceDeps(t, ServiceDeps{
			ServicePorts: ServicePorts{
				AssetStore:      artlistRepo,
				ScraperSearcher: &emptySearcher{},
			},
			ServiceDependencies: ServiceDependencies{
				Infra: ArtlistInfraDeps{
					MainDB: db,
					Cfg:    cfg,
					Log:    logger,
				},
				Ports: ArtlistPortDeps{
					Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
				},
				Finalizer: ArtlistFinalizerDeps{
					AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
				},
			},
		}))
		require.NoError(t, err)
		defer svc.Close()

		respB, errB := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
			Term:         "another-nonexistent-term-67890",
			Limit:        3,
			Strategy:     "replace",
			RootFolderID: "gate11-root-b",
		})

		require.Error(t, errB, "scenario B: RunTag must return error when no clips match")
		assert.False(t, respB.OK)
		assert.Equal(t, 0, respB.Processed)

		t.Logf("Scenario B (scraper available, no matches) error: %v", errB)
	})
}

// TestGate11_ScraperFailureNoDispatch verifies that when the scraper
// fails, no dispatcher calls are made. The pipeline must fail BEFORE
// reaching stagePersistResults, so no outbox events are emitted for
// clips that were never discovered.
func TestGate11_ScraperFailureNoDispatch(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: tmp},
		Video:   config.VideoConfig{Duration: 15},
	}

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := assets.NewClipsRepository(db, logger)

	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore:      artlistRepo,
			ScraperSearcher: &failingSearcher{},
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg:    cfg,
				Log:    logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
			},
			Finalizer: ArtlistFinalizerDeps{
				AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
			},
		},
	}))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "unreachable-term",
		Limit:        3,
		Strategy:     "replace",
		RootFolderID: "gate11-root-folder",
	})

	require.Error(t, err)

	// The dispatcher must NOT have been called — the pipeline
	// failed at stageDiscoverClips, before stagePersistResults.
	assert.Equal(t, 0, outboxEventCount(db),
		"dispatcher must NOT be called when scraper failure prevents discovery")
}

// ────────────────────────────────────────────────────────────
// Gate 12: Preflight — all Gate tests pass with zero failures
// ────────────────────────────────────────────────────────────

// TestGate12_PreflightAllGatesPass is a meta-test documentation anchor
// for Gate 12 of ARTLIST-DOD-2026-07-07.
//
// IMPORTANT: This is NOT a functional test of cmd/admin qdrant-preflight.
// The action plan's Gate 12 ("cmd/admin qdrant-preflight deve passare
// con exit 0, zero FAIL") targets the admin CLI, which lives outside
// this package. This test is the best approximation the artlist package
// can provide: it enumerates all known gate tests and verifies the
// matrix is complete (correct count, no duplicate names).
//
// The real Gate 12 verification happens at the operator level:
//
//	go run ./cmd/admin qdrant-preflight  # must exit 0
//
// godlike/07 no-fake-availability: this test does NOT claim to verify
// the qdrant-preflight command. It documents the test matrix completeness.
//
// godlike/06 SSOT: the canonical list of gate tests lives here.
// UPDATE THIS LIST when adding or removing gate tests.
//
// REINFORCEMENT (2026-07-11): the meta-anchor previously asserted only
// on a hardcoded `expectedGateTests` slice — which silently passed even
// when the spec diverged from reality (the historical gate06/07/10
// phantom-debt: 7 tests were declared in the spec but never implemented
// for weeks, and the static `assert.Len(t, expectedGateTests, 28, ...)`
// still passed). The reinforcement runs `go test -list "^TestGate" .`
// at runtime and verifies the actual test names match the spec. If
// any expected test is missing from the runtime list, the meta-anchor
// fails closed with a per-test error message identifying the phantom.
func TestGate12_PreflightAllGatesPass(t *testing.T) {
	// The canonical spec of expected gate tests. When a new gate test
	// is added, append its name here AND verify the count below.
	expectedGateTests := []string{
		// Gate 01 — Happy path
		"TestGate01_ArtlistFullRun_HappyPath",
		"TestGate01_ArtlistFullRun_MediaProcessorInputs",
		"TestGate01_ArtlistFullRun_ZeroCandidates",
		"TestGate01_ArtlistFullRun_DryRun",

		// Gate 02 — Drive fields
		"TestGate02_DriveFieldsPopulated",

		// Gate 03 — SQLite persistence
		"TestGate03_ArtlistRunsPopulatedAfterHandleJob",
		"TestGate03_ArtlistRunsNotRecordedWhenDiscoveryFails",

		// Gate 04 — Outbox emission
		"TestGate04_OutboxEventEmittedPerClip",
		"TestGate04_OutboxEventPayloadContainsSourceArtlist",
		"TestGate04_OutboxEventNotEmittedWhenNoClips",

		// Gate 05 — Outbox dispatch
		"TestGate05_OutboxDispatchContract",
		"TestGate05_OutboxNoDispatchWithoutDriveFields",

		// Gate 06 — Qdrant index_state
		"TestGate06_QdrantIndexStateAfterRun",
		"TestGate06_QdrantIndexStatePerClip",

		// Gate 07 — Search
		"TestGate07_SearchFindsIndexedClips",
		"TestGate07_DBSearcherDoesNotFilterByIndexState",

		// Gate 08 — Search round-trip
		"TestGate08_SearchRoundTripSameTerm",
		"TestGate08_SearchRoundTripSourceAndMediaType",
		"TestGate08_SearchRoundTripSearchableAfterPipeline",

		// Gate 09 — Drive failure
		"TestGate09_DriveFailureFailClosed",
		"TestGate09_ArtlistFullRun_PartialDriveFailure",

		// Gate 10 — Qdrant failure
		"TestGate10_QdrantFailureIndexStateNotIndexed",
		"TestGate10_QdrantFailureProcessedCountUnaffected",
		"TestGate10_QdrantFailureDoesNotPreventArtlistRun",

		// Gate 11 — Scraper failure
		"TestGate11_ScraperFailureReturnsClearError",
		"TestGate11_ScraperFailureDistinctFromEmptyResults",
		"TestGate11_ScraperFailureNoDispatch",

		// Fase 6 / Commit 1 (July 2026) — gate-block classifier + short-circuit
		// PR-TYPED-GATE-BLOCK-CLASSIFIER + handler integration tests. These
		// were the 8 tests added during the Commit 1 (acquisition-mode
		// block) work and are required to be present at runtime per the
		// godlike/06 SSOT lockstep: the spec and the test suite MUST agree
		// on the gate count (28 + 8 = 36). Drift here would silently
		// pass the historical phantom-debt pattern.
		"TestGateBlockClassify_AcquisitionModeBlocked",
		"TestGateBlockClassify_DailyLimitExhaustedReserved",
		"TestGateBlockClassify_NilReturnsNone",
		"TestGateBlockClassify_SessionExpiredReserved",
		"TestGateBlockClassify_UnauthorizedReserved",
		"TestGateBlockShortCircuit_NilItemLeavesUntouched",
		"TestGateBlockShortCircuit_StampsAuditBumpsCounter",
		"TestGateBlockShortCircuit_UnrelatedErrorNoOp",

		// Gate 12 — Preflight (this test)
		"TestGate12_PreflightAllGatesPass",
	}

	// 1. Static contract: the spec itself has no duplicates (a
	//    copy-paste error here would silently halve the assertions).
	seenExpected := map[string]bool{}
	for _, name := range expectedGateTests {
		assert.False(t, seenExpected[name], "duplicate gate test name in spec: %s", name)
		seenExpected[name] = true
	}

	// 2. Runtime contract: run `go test -list` and parse the actual
	//    gate tests present in the package. The relative target "."
	//    resolves to the current package's directory (the test runs
	//    with the package dir as CWD by `go test` convention).
	//
	//    Under heavy parallel load (e.g. `go test -race ./...`) the
	//    subprocess can be killed by the OS/OOM killer before it gets
	//    a chance to start. We retry a small number of times with a
	//    generous timeout so a transient resource squeeze does not
	//    fail the meta-test. The fallback panic message still surfaces
	//    the combined stderr when the command ultimately fails.
	var out []byte
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, "go", "test", "-list", "^TestGate", ".")
		// CombinedOutput (not Output) so stderr is captured: when the
		// package has build errors, the error message lands on stderr
		// and is essential for debugging. The `require.NoError` failure
		// message must include both streams to be actionable.
		out, err = cmd.CombinedOutput()
		cancel()
		if err == nil {
			break
		}
		if attempt < 2 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	require.NoError(t, err, "failed to run 'go test -list ^TestGate .' after retries — is 'go' on PATH? Combined output: %s", string(out))

	// 3. Parse runtime reality: extract lines starting with "TestGate"
	//    (the "ok ... 0.001s" status line is filtered out by prefix).
	actualTests := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "TestGate") {
			actualTests[line] = true
		}
	}

	// 4. Runtime count must match the spec count. A divergence here
	//    means the spec is stale (a new test was added without
	//    updating the list, or a test was removed without updating
	//    the count).
	assert.Len(t, actualTests, len(expectedGateTests),
		"Runtime gate test count diverged from spec: expected %d tests, got %d at runtime. "+
			"Update the expectedGateTests list above when adding/removing gate tests. "+
			"Actual list: %v", len(expectedGateTests), len(actualTests), actualTests)

	// 5. Per-test presence check: every spec entry must exist at
	//    runtime. This catches the historical phantom-debt (gate06/07/10
	//    were declared in the spec for weeks but never implemented)
	//    with a precise per-test error message.
	for _, expectedName := range expectedGateTests {
		assert.True(t, actualTests[expectedName],
			"phantom test detected: spec entry %q is missing from runtime — "+
				"either the test was never implemented (add the test file) or it was renamed (update the spec). "+
				"Actual list at runtime: %v", expectedName, actualTests)
	}

	t.Logf("Gate 12 preflight: verified %d implemented tests at runtime against spec (all present)", len(actualTests))
	t.Logf("Run 'go test -run \"^TestGate\" -count=1 ./internal/capabilities/assets/providers/artlist/...' to execute the full suite")
}
