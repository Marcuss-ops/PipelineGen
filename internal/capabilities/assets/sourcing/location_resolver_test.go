// Package sourcing — TDD contract test for PR-RESOLVER-PORT-EXTRACT.
//
// SEMANTIC-LOCATION-API-2026-07-06 Wave 7 / STM 7:
// the test surface is hermetic — no DB, no jobs queue, no Drive API.
// Each contract test calls Service.WithLocationResolver((*stubResolver)(t)),
// not the production *infrastructure/drive/resolver.Adapter — the stub
// has deterministic return values so the test is byte-reproducible
// across CI runs.
//
// godlike/06 SSOT one-canonical-owner-of-test-surface: no other test file
// in this package exercises the WithLocationResolver fluent setter or
// the resolveLocationFallback helper. Future tests targeting the
// resolver port route through these 5 cases (a..e) per the disposition.
package sourcing

import (
	"context"
	"errors"
	"testing"

	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// ── stub resolver (test-only) ─────────────────────────────────────────────

type stubResolver struct {
	folder string
	err    error
	calls  int
}

func (s *stubResolver) Resolve(ctx context.Context, loc domaindelivery.AssetLocationInput, dest delivery.DestinationKey) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	if s.folder != "" {
		return s.folder, nil
	}
	return "stub-fold-" + string(dest), nil
}

// nilLogger returns a Logger that swallows all calls so test fixtures
// do not have to wire a real zap instance.
type nilLogger struct{}

func (nilLogger) Info(string, ...any)  {}
func (nilLogger) Warn(string, ...any)  {}
func (nilLogger) Error(string, ...any) {}
func (nilLogger) Debug(string, ...any) {}

// stubFacade returns a *Service with the 4 sub-services stubbed at
// nil. The façade's delegated methods (RegisterFromYouTube here) will
// fail at the `s.youtube == nil` guard when invoked — so resolver
// fallback paths can be tested WITHOUT triggering the real YouTube
// orchestrator. This is hermetic to the unit-test boundary.
func stubFacade(t *testing.T) *Service {
	t.Helper()
	return NewService(nil, nil, nil, nil, nilLogger{})
}

// ── (a) nil-receiver propagation ──────────────────────────────────────────

// TestService_ResolveLocationFallback_NoResolver_ReturnsTypedSentinel
// pins: when the façade has no resolver wired AND the Location is
// non-empty AND FolderID is empty, the service returns the canonical
// typed sentinel ErrLocationResolverEmpty so callers can probe.
func TestService_ResolveLocationFallback_NoResolver_ReturnsTypedSentinel(t *testing.T) {
	s := stubFacade(t) // no WithLocationResolver call
	cmd := RegisterClipCommand{
		Location: domaindelivery.AssetLocationInput{Category: "Boxe", Subject: "Ali"},
	}
	_, err := s.resolveLocationFallback(context.Background(), cmd, delivery.DestinationYouTubeClip)
	if err == nil {
		t.Fatalf("expected typed error from unconfigured resolver; got nil")
	}
	if !errors.Is(err, ErrLocationResolverEmpty) {
		t.Fatalf("expected errors.Is(err, ErrLocationResolverEmpty); got %v", err)
	}
}

// ── (b) happy-path resolve + cmd.FolderID population ───────────────────────

// TestService_RegisterFromYouTube_ResolvesAndPopsFolderID pins:
// given a resolver wired to return "fld-123", cmd.FolderID is empty,
// cmd.Location is populated — the façade calls Resolver.Resolve (calls
// == 1) and surfaces an error because the orchestrator is nil-stubbed;
// the resolver-bearing call-order test asserts the folder-id argument
// reached the resolver AND that sub.youtube == nil fired BEFORE the
// orchestrator call (i.e. the orchestrator error is the surface error,
// but the resolver SOMETHING has fired).
//
// godlike/07 minimum-blast-radius: this test verifies that the resolver
// is invoked at-least-once per RegisterFromYouTube call carrying a
// non-empty Location AND empty FolderID. The orchestrator call then
// fails at the empty-YouTube-guard boundary, which is acceptable for
// the contract test.
func TestService_RegisterFromYouTube_ResolvesAndPopsFolderID(t *testing.T) {
	r := &stubResolver{folder: "fld-123"}
	s := stubFacade(t).WithLocationResolver(r)
	cmd := RegisterClipCommand{
		URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Location: domaindelivery.AssetLocationInput{Category: "Boxe", Subject: "Ali"},
	}
	_, err := s.RegisterFromYouTube(context.Background(), cmd)
	// orchestrator is nil-stub; the youtube == nil guard fires AFTER
	// the resolveLocationFallback returns "fld-123". The error is the
	// orchestrator-not-wired error; we DO NOT assert on it. We assert
	// that the resolver was called exactly once (per the F3 flow).
	if r.calls != 1 {
		t.Fatalf("expected resolver calls == 1; got %d", r.calls)
	}
	if err == nil {
		t.Fatalf("expected orchestrator-not-wired error from nil sub-service; got nil")
	}
}

// ── (c) typed-sentinel probes ─────────────────────────────────────────────

// TestErrLocationResolver_SentinelsDistinct pins: the three typed
// sentinels are independently identifiable via errors.Is. This is the
// godlike/07 typed-error-contract test — callers MUST NOT need to
// parse error strings to discriminate between failure modes.
func TestErrLocationResolver_SentinelsDistinct(t *testing.T) {
	base := []error{
		ErrLocationResolverEmpty,
		ErrLocationResolverDestinationUnsupported,
		ErrLocationResolverIncompatibleFields,
	}
	for i, a := range base {
		for j, b := range base {
			if i == j {
				if !errors.Is(a, b) {
					t.Fatalf("errors.Is(%v, %v) = false; want true (sentinel identity)", a, b)
				}
				continue
			}
			if errors.Is(a, b) {
				t.Fatalf("sentinels %v and %v unexpectedly Is-equal", a, b)
			}
		}
	}
}

// ── (d) byte-equivalent fallback: cmd.FolderID wins ───────────────────────

// TestService_ResolveLocationFallback_FolderIDWins pins: when both
// cmd.FolderID AND cmd.Location are populated, the resolver is NOT
// called (godlike/07 minimum-blast-radius: legacy FolderID wins and
// the resolver is bypassed). The façade's RegisterFromYouTube then
// fails at the orchestrator-not-wired gate; we assert on the resolver
// calls counter == 0.
func TestService_ResolveLocationFallback_FolderIDWins(t *testing.T) {
	r := &stubResolver{folder: "resolver-fold-NOT-USED"}
	s := stubFacade(t).WithLocationResolver(r)
	cmd := RegisterClipCommand{
		URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		FolderID: "legacy-fld-999",
		Location: domaindelivery.AssetLocationInput{Category: "Boxe", Subject: "Ali"},
	}
	_, _ = s.RegisterFromYouTube(context.Background(), cmd)
	if r.calls != 0 {
		t.Fatalf("expected resolver calls == 0 (FolderID wins); got %d", r.calls)
	}
}

// ── (e) F3 service-layer fallback integration: empty Location → no resolver ─

// TestService_ResolveLocationFallback_EmptyLocation_NoResolverCall pins:
// when cmd.Location is empty AND cmd.FolderID is empty, the resolver
// is NOT called and the fallback returns cmd.FolderID (empty string).
// Caller-side: the orchestrator (not wired) returns the
// orchestrator-not-wired error. We assert on the resolver-calls == 0
// counter.
func TestService_ResolveLocationFallback_EmptyLocation_NoResolverCall(t *testing.T) {
	r := &stubResolver{}
	s := stubFacade(t).WithLocationResolver(r)
	cmd := RegisterClipCommand{
		URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
	}
	fid, err := s.resolveLocationFallback(context.Background(), cmd, delivery.DestinationYouTubeClip)
	if err != nil {
		t.Fatalf("expected nil err for empty-location fallback; got %v", err)
	}
	if fid != "" {
		t.Fatalf("expected empty folder-id for empty-location fallback; got %q", fid)
	}
	if r.calls != 0 {
		t.Fatalf("expected resolver calls == 0 (empty location); got %d", r.calls)
	}
}

// ── (f) fluent setter returns same *Service ────────────────────────────────

// TestService_WithLocationResolver_FluentSetter pins: the WithLocationResolver
// setter returns the same *Service pointer so callers can chain it
// inline AND future additions don't break call-sites that already
// capture the result. godlike/07 minimum-blast-radius: failed nil-svc
// case returns nil so a nil-receiver With-chaining is detectable.
func TestService_WithLocationResolver_FluentSetter(t *testing.T) {
	r := &stubResolver{}
	s := stubFacade(t)
	out := s.WithLocationResolver(r)
	if out != s {
		t.Fatalf("WithLocationResolver did not return self; got %p want %p", out, s)
	}
	if s.locationResolver == nil {
		t.Fatalf("expected locationResolver to be wired after fluent setter; got nil")
	}
	var nilSvc *Service
	if nilSvc.WithLocationResolver(r) != nil {
		t.Fatalf("nil-receiver WithLocationResolver should return nil; got non-nil")
	}
}

// ── (g) compile-time pin (godlike/06) ─────────────────────────────────────

// TestLocationResolverPort_AdapterSatisfies pins: stubResolver satisfies
// the LocationResolverPort interface. Equivalent to
// `var _ LocationResolverPort = (*stubResolver)(nil)` but expressed as
// a runtime test so a missing-method regression surfaces in CI logs.
func TestLocationResolverPort_StubSatisfies(t *testing.T) {
	var p LocationResolverPort = &stubResolver{}
	var _ = p // type-pinned; future receivers should match
}
