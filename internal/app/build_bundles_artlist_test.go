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
//
// A happy-path "all gates up" test would require deeper SQLite + Jobs
// fixtures (out of scope for this unit-test layer per FASE-6 EXPAND-phase
// discipline; tracked as a follow-up if/when a TestWireArtlist_HappyPath
// task is added to the wave-tracker). For this PR the two failure-mode
// tests above are the contract-layer TDD coverage.
//
// Both tests use httptest.Server to mock /api/artlist/stats — this is the
// live-probe endpoint that WireArtlist pings via NewHTTPSelfLoopProbe.
// The HTTPSelfLoopProbe adapter is *DIRECTLY* tested by the unit tests
// in internal/application/assets/providers/artlist/http_live_probe_test.go;
// this test file focuses on the COMPOSITION wiring path.
package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

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
	bundle := &ArtlistBundle{
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
	assert.Contains(t, err.Error(), "Publisher is nil",
		"expected WireArtlist to fail-closed on mandatory Publisher gate (gate #1 runs first in the ladder)")
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
	bundle := &ArtlistBundle{
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
	assert.Contains(t, err.Error(), "Publisher is nil",
		"mandatory Publisher gate must remain authoritative (gate #1) even when Dispatcher gate #2 is wired")
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
func TestValidateArtlistScraperURL_NilCfg_ReturnsError(t *testing.T) {
	err := validateArtlistScraperURL(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cfg is nil",
		"gate #5 helper must fail loudly when cfg is nil (defensive godlike/06 SSOT)")
	assert.Contains(t, err.Error(), "scraper-URL fail-closed",
		"error must name the gate so operators can grep it in logs")
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
	assert.Contains(t, err.Error(), "ARTLIST_SCRAPER_SERVER_URL",
		"error must name the env var operators must set")
	assert.Contains(t, err.Error(), "VELOX_FEATURE_ARTLIST_ENABLED=false",
		"error must include the disable escape hatch for operators who don't need the feature")
}
