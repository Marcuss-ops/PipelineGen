// Package scripts — media_curator_test.go pins the PJ-CURATE-1 (June 2026)
// typed-error contract: empty clip resolution + !AllowTextOnly surfaces
// ErrCurateNoClips instead of silently producing a text-only result.
//
// The test deliberately stubs the Qdrant-side dependencies to nil so the
// MediaCurator runs in HintClipIDs-only mode — the audit-recommended
// production default. Tests for the port-attached path live alongside
// the adapter in the qdrant package.
package scripts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestMediaCurator_Curate_NoClips_NoPort_DefaultError pins the canonical
// behaviour: when neither the search leg nor HintClipIDs produced any
// clips AND the caller did not opt into the text-only fallback, the
// curator returns the typed ErrCurateNoClips sentinel (detectable via
// errors.Is) wrapping *CurateNoClipsError with ResultCount=0.
//
// engine/clipsRepo/clipBuilder are all nil intentionally — the typed
// error short-circuits BEFORE phase 3, so no real infrastructure is
// touched. This is the real contract surface the audit demanded.
func TestMediaCurator_Curate_NoClips_NoPort_DefaultError(t *testing.T) {
	t.Parallel()

	m := &MediaCurator{log: zap.NewNop()}

	_, err := m.Curate(context.Background(), CurateRequest{
		Query:         "why observability matters",
		AllowTextOnly: false,
	})

	if err == nil {
		t.Fatal("expected ErrCurateNoClips, got nil")
	}
	if !errors.Is(err, ErrCurateNoClips) {
		t.Fatalf("expected errors.Is(err, ErrCurateNoClips)=true, got err=%v", err)
	}
	// Structured details via errors.As.
	var typed *CurateNoClipsError
	if !errors.As(err, &typed) {
		t.Fatalf("expected errors.As(*CurateNoClipsError)=true, got err=%v", err)
	}
	if typed.Query != "why observability matters" {
		t.Errorf("expected Query=why observability matters; got %q", typed.Query)
	}
	if typed.ResultCount != 0 {
		t.Errorf("expected ResultCount=0; got %d", typed.ResultCount)
	}
}

// TestMediaCurator_Curate_HintClipIDs_PassesGate_Pins_NonCurateError
// asserts the legacy HintClipIDs-only path: when the caller seeds the
// curation with pre-resolved IDs and the port is not wired, the
// typed-error gate MUST NOT fire — even with no engine wired, the
// curator reaches the engine-not-configured error path instead.
//
// This pins the negative surface ("the gate did NOT block") rather
// than the engine write path itself (which has its own integration
// tests in scripts/curation_engine_test.go). Avoids the panic-as-
// assertion pattern of the previous version: the test now relies on
// the documented "media curator: engine not configured" error path
// rather than a panic-based probe.
func TestMediaCurator_Curate_HintClipIDs_PassesGate_Pins_NonCurateError(t *testing.T) {
	t.Parallel()

	m := &MediaCurator{
		log:         zap.NewNop(),
		engine:      nil, // intentional: forces the engine-not-configured path
		clipBuilder: nil, // intentional: skips BuildClipContext
		clipSearch:  nil, // intentional: HintClipIDs-only legacy path
	}

	_, err := m.Curate(context.Background(), CurateRequest{
		Query:         "hint-list path",
		HintClipIDs:   []string{"clip-A", "clip-B"},
		AllowTextOnly: false,
	})

	if err == nil {
		t.Fatal("expected engine-not-configured error (engine is nil), got nil")
	}
	if errors.Is(err, ErrCurateNoClips) {
		t.Fatalf("HintClipIDs-resolved path MUST NOT surface ErrCurateNoClips; got err=%v", err)
	}
	// The downstream error must be the documented "engine not configured"
	// path. This is the negative gate contract: the typed-error gate
	// only fires when clipIDs == 0 AND !AllowTextOnly.
	if got, want := err.Error(), "media curator: engine not configured"; !strings.Contains(got, want) {
		t.Errorf("expected error %q to contain %q (engine-not-configured path), got %q", got, want, got)
	}
}
