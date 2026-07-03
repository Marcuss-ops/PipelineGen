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
