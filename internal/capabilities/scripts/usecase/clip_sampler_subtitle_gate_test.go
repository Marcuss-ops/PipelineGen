// Package usecase — clip_sampler_subtitle_gate_test.go pins the MEDIA-SSOT P2-9
// Phase 2 split of the subtitle_ready sampler gate.
//
// The gate used to read its two facts in ONE statement through a package-global
// `SamplerDB *sql.DB` bound to the operational SQLite store:
//
//	SELECT source, (SELECT COUNT(*) FROM asset_subtitle_artifacts ...)
//	FROM media_assets WHERE id = ?
//
// Measuring the two tables showed they do not share an engine — media_assets is
// the PostgreSQL media SSOT while asset_subtitle_artifacts exists only on SQLite
// (0 PostgreSQL tables, no PostgreSQL writer) — so the statement could be neither
// re-pointed at PostgreSQL nor left graded against the operational mirror. The
// facts are now separate engine-named ports.
package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

type stubAssetSource struct {
	sourceByID map[string]string
	err        error
	calls      int
}

func (s *stubAssetSource) AssetSource(_ context.Context, assetID string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.sourceByID[assetID], nil
}

type stubReadyASS struct {
	countByID map[string]int
	err       error
	calls     int
}

func (s *stubReadyASS) CountReadyASSArtifacts(_ context.Context, assetID string) (int, error) {
	s.calls++
	if s.err != nil {
		return 0, s.err
	}
	return s.countByID[assetID], nil
}

func gateInput(clipID string) ClipSamplerGateInput {
	return ClipSamplerGateInput{Candidate: ports.ClipSamplerCandidate{ClipID: clipID}}
}

// TestSubtitleReadyGate_IsVacuousWithoutEitherPort pins the preserved degrade
// contract: an unwired gate passes, exactly as the retired `SamplerDB == nil`
// check did. A degraded sampler must not start rejecting every candidate because
// a read surface is missing.
func TestSubtitleReadyGate_IsVacuousWithoutEitherPort(t *testing.T) {
	for name, deps := range map[string]SamplerGateDeps{
		"both nil":        {},
		"no source":       {ReadyASSArtifacts: &stubReadyASS{}},
		"no artifact cnt": {AssetSource: &stubAssetSource{}},
	} {
		passed, reason := subtitleReadyGate{deps: deps}.Evaluate(gateInput("clip-1"))
		if !passed {
			t.Errorf("%s: gate must be vacuous, got passed=false reason=%q", name, reason)
		}
	}
}

// TestSubtitleReadyGate_MediaSourceErrorFailsClosed pins that an unreadable media
// SSOT fails the gate rather than being read as "subtitles not required".
func TestSubtitleReadyGate_MediaSourceErrorFailsClosed(t *testing.T) {
	deps := SamplerGateDeps{
		AssetSource:       &stubAssetSource{err: errors.New("media SSOT unavailable")},
		ReadyASSArtifacts: &stubReadyASS{},
	}
	passed, reason := subtitleReadyGate{deps: deps}.Evaluate(gateInput("clip-1"))
	if passed {
		t.Fatal("an unreadable media source must fail the gate")
	}
	if reason == "" {
		t.Fatal("a failed gate must carry a reason (audit contract)")
	}
}

// TestSubtitleReadyGate_SkipsArtifactReadWhenSubtitlesNotRequired pins the
// deliberate tightening over the retired combined statement. The old query failed
// the gate on ANY error from the joined read, including for a candidate whose
// source never required subtitles — i.e. it could reject a clip over a fact it
// was never going to consult. Now the artifact count is read only when it
// matters, which also removes a needless operational round-trip per candidate.
func TestSubtitleReadyGate_SkipsArtifactReadWhenSubtitlesNotRequired(t *testing.T) {
	artifacts := &stubReadyASS{err: errors.New("operational store down")}
	deps := SamplerGateDeps{
		AssetSource:       &stubAssetSource{sourceByID: map[string]string{"clip-1": "artlist"}},
		ReadyASSArtifacts: artifacts,
	}
	passed, reason := subtitleReadyGate{deps: deps}.Evaluate(gateInput("clip-1"))
	if !passed {
		t.Fatalf("a source that does not require subtitles must pass regardless of the artifact store, got reason=%q", reason)
	}
	if artifacts.calls != 0 {
		t.Errorf("artifact counter called %d times, want 0 (only read when subtitles are required)", artifacts.calls)
	}
}

// TestSubtitleReadyGate_RequiresReadyASSWhenSubtitlesNeeded pins the gate's
// actual purpose for a subtitle-requiring source, in both directions.
func TestSubtitleReadyGate_RequiresReadyASSWhenSubtitlesNeeded(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    int
		wantPass bool
	}{
		{"ready artifact present", 1, true},
		{"no ready artifact", 0, false},
	} {
		deps := SamplerGateDeps{
			AssetSource:       &stubAssetSource{sourceByID: map[string]string{"clip-1": "youtube"}},
			ReadyASSArtifacts: &stubReadyASS{countByID: map[string]int{"clip-1": tc.count}},
		}
		passed, reason := subtitleReadyGate{deps: deps}.Evaluate(gateInput("clip-1"))
		if passed != tc.wantPass {
			t.Errorf("%s: passed=%v want %v (reason=%q)", tc.name, passed, tc.wantPass, reason)
		}
		if !tc.wantPass && reason == "" {
			t.Errorf("%s: a failed gate must carry a reason", tc.name)
		}
	}
}

// TestSubtitleReadyGate_ArtifactStoreErrorFailsClosed pins that a subtitle-
// requiring candidate fails closed when the operational artifact store is
// unreadable — the fail-closed half must survive the removal of the shared
// statement.
func TestSubtitleReadyGate_ArtifactStoreErrorFailsClosed(t *testing.T) {
	deps := SamplerGateDeps{
		AssetSource:       &stubAssetSource{sourceByID: map[string]string{"clip-1": "youtube"}},
		ReadyASSArtifacts: &stubReadyASS{err: errors.New("operational store down")},
	}
	passed, reason := subtitleReadyGate{deps: deps}.Evaluate(gateInput("clip-1"))
	if passed {
		t.Fatal("an unreadable artifact store must fail a subtitle-requiring candidate")
	}
	if reason == "" {
		t.Fatal("a failed gate must carry a reason")
	}
}

// TestDefaultGatesAlwaysIncludesSubtitleGate pins that the 11-gate audit
// sequence (and its order) is unchanged by the dependency split — the order is
// itself part of the audit contract.
func TestDefaultGatesAlwaysIncludesSubtitleGate(t *testing.T) {
	for _, deps := range []SamplerGateDeps{{}, {AssetSource: &stubAssetSource{}, ReadyASSArtifacts: &stubReadyASS{}}} {
		gates := defaultGates(deps)
		if len(gates) != 11 {
			t.Fatalf("len(defaultGates) = %d, want 11", len(gates))
		}
		if gates[len(gates)-1].Name() != "subtitle_ready" {
			t.Errorf("last gate = %q, want subtitle_ready (order is part of the audit contract)", gates[len(gates)-1].Name())
		}
	}
}
