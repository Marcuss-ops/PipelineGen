// Package script — handler_facade_semantic_test.go: PR-P12-HANDLER-FACADE-SEMANTIC
// contract tests (P0 absolute, deadline 2026-08-01).
//
// Pins the canonical semantic-routing contract for
// FacadeHandler.ResolveDriveFolderID:
//
//  1. Single-segment path → Group = segment, Subject = "_script",
//     RootFolderOverride is INTENTIONALLY OMITTED (canonical
//     Publisher resolves the root folder via DestinationRegistry +
//     DestinationPolicy.RootFolderID per the
//     architecture/current.yaml#DRIVE-AS-CENTRAL-CAPABILITY wave).
//  2. Multi-segment path → Group = join(clean[0..n-1], "/"),
//     Subject = clean[n-1], RootFolderOverride is INTENTIONALLY OMITTED.
//  3. Empty clean path (only separators) → returns defaultRootID
//     without calling the publisher (fail-closed at the resolver
//     boundary, not at the Publisher).
//
// godlike/06 SSOT: the recorded `lastReq.RootFolderOverride` is the
// canonical assertion target — the legacy `RootFolderOverride =
// defaultRootID` literal is the exact violation PR-P12 retires.
//
// godlike/07 NO-FAKE-AVAILABILITY: every case asserts the recorded
// `lastReq` matches the expected wire-shape exactly; a future refactor
// that silently re-introduces RootFolderOverride (or splits
// Group/Subject differently) surfaces as a test failure.
package script

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

// recordingPublisher is a stub delivery.Publisher that captures the
// PublishRequest it receives so tests can assert the wire-shape
// (Group/Subject/RootFolderOverride/Destination) propagated from
// ResolveDriveFolderID. ResolveFolder is the only method exercised
// by the facade contract; Publish + Close + ResolveFolder returns a
// canned folder ID.
type recordingPublisher struct {
	lastReq delivery.PublishRequest
	calls   int
	// optional error to surface from ResolveFolder (default nil)
	resolveErr error
}

var _ delivery.Publisher = (*recordingPublisher)(nil)

func (r *recordingPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	r.lastReq = req
	r.calls++
	return &delivery.PublishResult{Action: delivery.PublishActionCreated}, nil
}

func (r *recordingPublisher) ResolveFolder(_ context.Context, req delivery.PublishRequest) (string, error) {
	r.lastReq = req
	r.calls++
	if r.resolveErr != nil {
		return "", r.resolveErr
	}
	return "resolved-folder-id", nil
}

func (r *recordingPublisher) Close() error { return nil }

// newFacadeForSemanticTest builds a minimal FacadeHandler with the
// supplied publisher (logs via zap.NewNop per the canonical
// composition root convention).
func newFacadeForSemanticTest(pub *recordingPublisher) *FacadeHandler {
	return NewFacadeHandler(
		nil, // voService (unused by ResolveDriveFolderID)
		nil, // groupsResolver (unused by ResolveDriveFolderID)
		pub, // publisher
		zap.NewNop(),
	)
}

// TestResolveDriveFolderID_SingleSegment_GroupSubjectSplit_NoRootOverride
// pins the canonical single-segment contract per PR-P12:
//
//   - input "boxing-clips" (1 segment) → lastReq.Group = "boxing-clips"
//   - lastReq.Subject = "_script" (placeholder for single-segment paths)
//   - lastReq.Destination = DestinationYouTubeClip
//   - lastReq.RootFolderOverride = "" (RETIRED — Publisher resolves
//     root via DestinationRegistry per godlike/06 SSOT)
func TestResolveDriveFolderID_SingleSegment_GroupSubjectSplit_NoRootOverride(t *testing.T) {
	pub := &recordingPublisher{}
	fh := newFacadeForSemanticTest(pub)

	got, err := fh.ResolveDriveFolderID(context.Background(), "boxing-clips", "default-root-fallback")
	if err != nil {
		t.Fatalf("ResolveDriveFolderID err: %v", err)
	}
	if got != "resolved-folder-id" {
		t.Fatalf("folder ID = %q, want %q", got, "resolved-folder-id")
	}
	if pub.calls != 1 {
		t.Fatalf("publisher called %d times, want 1", pub.calls)
	}
	if pub.lastReq.Group != "boxing-clips" {
		t.Errorf("Group = %q, want %q (single segment must be Group)", pub.lastReq.Group, "boxing-clips")
	}
	if pub.lastReq.Subject != "_script" {
		t.Errorf("Subject = %q, want %q (single-segment placeholder)", pub.lastReq.Subject, "_script")
	}
	if pub.lastReq.Destination != delivery.DestinationYouTubeClip {
		t.Errorf("Destination = %q, want %q", pub.lastReq.Destination, delivery.DestinationYouTubeClip)
	}
	// godlike/07 NO-FAKE-AVAILABILITY: the legacy bypass literal is
	// RETIRED. The Publisher must NEVER see a caller-supplied
	// RootFolderOverride — it resolves root folders via
	// DestinationRegistry per the architecture/current.yaml
	// DRIVE-AS-CENTRAL-CAPABILITY wave.
	if pub.lastReq.RootFolderOverride != "" {
		t.Fatalf("RootFolderOverride = %q, want \"\" (legacy bypass retired per PR-P12)", pub.lastReq.RootFolderOverride)
	}
}

// TestResolveDriveFolderID_MultiSegment_GroupJoined_SubjectLast
// pins the canonical multi-segment contract: input "italy/boxe/mike-tyson"
// (3 segments) → Group = "italy/boxe" (clean[0..n-1] joined by "/"),
// Subject = "mike-tyson" (clean[n-1]), RootFolderOverride = "" (retired).
func TestResolveDriveFolderID_MultiSegment_GroupJoined_SubjectLast(t *testing.T) {
	pub := &recordingPublisher{}
	fh := newFacadeForSemanticTest(pub)

	got, err := fh.ResolveDriveFolderID(context.Background(), "italy/boxe/mike-tyson", "default-root-fallback")
	if err != nil {
		t.Fatalf("ResolveDriveFolderID err: %v", err)
	}
	if got != "resolved-folder-id" {
		t.Fatalf("folder ID = %q, want %q", got, "resolved-folder-id")
	}
	if pub.calls != 1 {
		t.Fatalf("publisher called %d times, want 1", pub.calls)
	}
	if pub.lastReq.Group != "italy/boxe" {
		t.Errorf("Group = %q, want %q (clean[0..n-1] joined by /)", pub.lastReq.Group, "italy/boxe")
	}
	if pub.lastReq.Subject != "mike-tyson" {
		t.Errorf("Subject = %q, want %q (last clean segment)", pub.lastReq.Subject, "mike-tyson")
	}
	if pub.lastReq.Destination != delivery.DestinationYouTubeClip {
		t.Errorf("Destination = %q, want %q", pub.lastReq.Destination, delivery.DestinationYouTubeClip)
	}
	if pub.lastReq.RootFolderOverride != "" {
		t.Fatalf("RootFolderOverride = %q, want \"\" (legacy bypass retired per PR-P12)", pub.lastReq.RootFolderOverride)
	}
}

// TestResolveDriveFolderID_EmptyCleanPath_FailsClosedToDefault
// pins the fail-closed contract for whitespace/separator-only inputs:
// input "   ///  " → clean is empty → return defaultRootID verbatim
// WITHOUT calling the publisher. godlike/07 NO-FAKE-AVAILABILITY:
// the resolver never invokes the Publisher with an empty path
// (which would otherwise route through DestinationRegistry with
// no semantic keys and could fail-closed in a different,
// non-deterministic location).
func TestResolveDriveFolderID_EmptyCleanPath_FailsClosedToDefault(t *testing.T) {
	pub := &recordingPublisher{}
	fh := newFacadeForSemanticTest(pub)

	const defaultRoot = "default-root-fallback"
	for _, input := range []string{
		"   ",      // whitespace only
		"///",      // separators only
		"  /  /  ", // whitespace + separators
		strings.Repeat("/", 8),
	} {
		pub.calls = 0 // reset between sub-cases
		got, err := fh.ResolveDriveFolderID(context.Background(), input, defaultRoot)
		if err != nil {
			t.Fatalf("input=%q: ResolveDriveFolderID err: %v", input, err)
		}
		if got != defaultRoot {
			t.Errorf("input=%q: folder ID = %q, want %q (fail-closed defaultRootID)", input, got, defaultRoot)
		}
		if pub.calls != 0 {
			t.Errorf("input=%q: publisher called %d times, want 0 (empty clean path must fail-closed BEFORE Publisher dispatch)", input, pub.calls)
		}
	}
}
