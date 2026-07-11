// Package app — wire_script_preflight_test.go is the canonical hermetic
// TDD test surface for the buildPreflightCaps helper introduced by
// PR-SCRIPTCONTRACT-COMPOSITION-WIRE (July 2026, deadline 2026-07-15).
//
// godlike/06 SSOT: this file is the SOLE canonical test surface for
// buildPreflightCaps. The 3-permutation matrix (all-wired / partial /
// all-nil) is the canonical contract. New permutation tests (e.g. for
// future processor categories) MUST be added here AND extend the
// PreflightCaps struct in internal/api/script/postprocessor_preflight.go
// AND the requireRequestedProcessorsOne branch in the same file
// (3-surface lockstep).
//
// godlike/07 NO-FAKE-AVAILABILITY: every test asserts the exact booleans
// the helper returns. A regression that flips a nil-check to a truthy
// default (or vice versa) surfaces as test failure BEFORE the bug
// reaches production.
//
// godlike/07 minimum-blast-radius: pure-function tests (no I/O, no DB,
// no fixtures, no hermeticity-related flake). The stubDocClient is
// zero-overhead (4 no-op methods + 1 compile-time pin).
package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// stubDocClient is a minimal drive.DocClient implementation for hermetic
// testing of the buildPreflightCaps helper. The 4 methods are no-op
// stubs because the test only exercises the helper's nil-check semantics
// (the canonical 3-bool surface), not the DocClient surface itself.
//
// godlike/07 minimum-blast-radius: each method returns the canonical
// "empty success" tuple for its signature (nil, nil) so future tests
// that extend the test surface to actually invoke DocClient methods
// (e.g. for end-to-end preflight integration) have a sensible default.
type stubDocClient struct{}

func (s *stubDocClient) CreateDoc(ctx context.Context, title, content, folderID string) (*drive.Doc, error) {
	return nil, nil
}
func (s *stubDocClient) CreateDocIdempotent(ctx context.Context, title, content, folderID, idempotencyKey string, forceRefresh bool) (*drive.Doc, error) {
	return nil, nil
}
func (s *stubDocClient) ShareDoc(ctx context.Context, docID, email, role string) error {
	return nil
}
func (s *stubDocClient) ListRecentDocs(ctx context.Context, folderID string, limit int) ([]drive.Doc, error) {
	return nil, nil
}
func (s *stubDocClient) UpdateDoc(ctx context.Context, docID, title, content string) error {
	return nil
}

// Compile-time pin (godlike/06 SSOT forward-prevention): a future drift
// in the drive.DocClient interface signature (added method, removed
// method, signature change) surfaces here as a build failure, not a
// runtime test failure.
var _ drive.DocClient = (*stubDocClient)(nil)

// TestBuildPreflightCaps_AllWired: the canonical "healthy production
// deployment" permutation. All 3 composition-time deps are non-nil.
// The helper MUST return all 3 Enabled=true.
//
// Mirrors a deployment where BuildDomainBundle successfully wired
// voiceover + images + Drive bundles AND DriveBuilder successfully
// constructed DocClient via NewDocClient. In this state, every
// /api/script/generate request that asks for any of the 3 postprocessors
// passes the preflight and reaches the broker.
func TestBuildPreflightCaps_AllWired(t *testing.T) {
	root := &ComposeRoot{
		Domains: &DomainBundle{
			VoiceoverService: &voiceover.Service{},
			ImageService:     &imgservice.Service{},
		},
		Drive: &DriveBundle{
			DocClient: &stubDocClient{},
		},
	}

	caps := buildPreflightCaps(root)

	require.True(t, caps.VoiceoverEnabled,
		"VoiceoverEnabled must be true when root.Domains.VoiceoverService is non-nil (pre-PR-2 behavior was false → 503 on every generate_voiceover request)")
	require.True(t, caps.ImagesEnabled,
		"ImagesEnabled must be true when root.Domains.ImageService is non-nil")
	require.True(t, caps.DocumentEnabled,
		"DocumentEnabled must be true when root.Drive.DocClient is non-nil")
}

// TestBuildPreflightCaps_OnlyVoiceover: the canonical "partial
// deployment" permutation. Only voiceover is wired; images + doc are
// not yet built. The helper MUST return VoiceoverEnabled=true + the
// other 2 false.
//
// Mirrors a deployment in the middle of a feature rollout where
// voiceover works but the images/doc pipelines are not yet wired.
// In this state, /api/script/generate with generate_voiceover=true
// passes; with generate_scene_images=true or generate_document=true
// it fails with 503 preflight_processor_missing (canonical
// godlike/07 NO-FAKE-AVAILABILITY per-destination behavior).
func TestBuildPreflightCaps_OnlyVoiceover(t *testing.T) {
	root := &ComposeRoot{
		Domains: &DomainBundle{
			VoiceoverService: &voiceover.Service{},
		},
		Drive: &DriveBundle{},
	}

	caps := buildPreflightCaps(root)

	require.True(t, caps.VoiceoverEnabled,
		"VoiceoverEnabled must be true when root.Domains.VoiceoverService is non-nil")
	require.False(t, caps.ImagesEnabled,
		"ImagesEnabled must be false when root.Domains.ImageService is nil (preflight must reject generate_scene_images=true with 503)")
	require.False(t, caps.DocumentEnabled,
		"DocumentEnabled must be false when root.Drive.DocClient is nil (preflight must reject generate_document=true with 503)")
}

// TestBuildPreflightCaps_AllNil: the canonical "no services wired"
// permutation. All 3 composition-time deps are nil. The helper MUST
// return all 3 Enabled=false.
//
// Mirrors a misconfigured deployment OR a test fixture (e.g. a unit
// test that exercises postprocessor_preflight directly without
// standing up the full composition root). In this state, the
// preflight gate correctly fails-closed with 503
// preflight_processor_missing on EVERY generate_* request — this is
// the canonical godlike/07 fail-closed conservative default (the
// zero-value PreflightCaps MUST NOT silently accept user requests
// for postprocessors that are not actually wired).
func TestBuildPreflightCaps_AllNil(t *testing.T) {
	root := &ComposeRoot{
		Domains: &DomainBundle{},
		Drive:   &DriveBundle{},
	}

	caps := buildPreflightCaps(root)

	require.False(t, caps.VoiceoverEnabled,
		"VoiceoverEnabled must be false when root.Domains.VoiceoverService is nil")
	require.False(t, caps.ImagesEnabled,
		"ImagesEnabled must be false when root.Domains.ImageService is nil")
	require.False(t, caps.DocumentEnabled,
		"DocumentEnabled must be false when root.Drive.DocClient is nil")
}

// TestBuildPreflightCaps_ZeroValue: the canonical "empty ComposeRoot"
// permutation. root.Domains AND root.Drive are both nil. The helper
// MUST panic on nil-pointer dereference — the top-of-function
// guard in wireScriptFlow (PR-SCRIPTCONTRACT-COMPOSITION-WIRE NIT-1
// fix) is the production surface that prevents this; this test
// documents the helper's defensive behavior (defensive nil-check
// via Go's `!= nil` short-circuit on a nil interface field).
//
// godlike/07 fail-closed: in a hypothetical direct call to
// buildPreflightCaps with a zero-value root, the helper MUST panic
// (not return false booleans) so the operator sees the bug at the
// first call site, not in a downstream preflight failure with
// misleading diagnostics. The production guard upstream of the
// helper is the load-bearing defense — this test exists to lock
// the helper's contract that nil-interface access is a panic
// (recoverable upstream via the top guard) rather than silent
// false.
//
// Uses require.Panics (testify canonical pattern) per the
// internal/app/voiceover_wiring_test.go precedent — cleaner failure
// messages than the defer-recover() idiom.
func TestBuildPreflightCaps_ZeroValue(t *testing.T) {
	require.Panics(t, func() {
		_ = buildPreflightCaps(&ComposeRoot{})
	}, "buildPreflightCaps must panic on nil.Drive (production guard upstream in wireScriptFlow is the load-bearing defense)")
}
