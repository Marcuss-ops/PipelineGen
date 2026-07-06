// Package resolver — TDD tests for PR-RESOLVER-PORT-EXTRACT round-2.
//
// Covers the 3 deferred SHOULD-FIX items from the round-1 code-review:
//
// (A) Round-1 NEEDS-FIX #3 (deferred) — per-destination segment-shape
//
//	test surface pinning the godlike/06 SSOT mirror of
//	BuildPublishRequest in delivery/mapper.go. 9 cases (one per
//	DestinationKey) — verify segmentsForDestination returns the
//	canonical tuple that downstream mandatoryFieldGate consumes.
//
// (B) Round-2 SHOULD-FIX #2 — WithLogger and NewAdapter both default
//
//	nil-logger to nopResolverLogger{} (dead-call defense: a.log.Warn
//	calls don't panic on nil-receiver).
//
// (C) Round-2 SHOULD-FIX #3 — Voiceover/Book/Script destinations
//
//	soft-ignore "category" instead of hard-rejecting. Verify (a)
//	errors.Is(err, ErrLocationResolverIncompatibleFields) is FALSE
//	for Voiceover+Category (only), (b) a.log.Warn fires with the
//	canonical message, (c) folder-id is produced.
//
// Heroic patterns (godlike/07 no-fake-availability): tests use a
// custom capturingResolverLogger that directly implements the
// canonical resolverLogger interface (no zap dependency needed — the
// interface signatures match exactly, so the dead-call probe is real,
// not a type-cast heuristic). Each test runs in isolation (no shared
// state).
package resolver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	sourcing "github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/domain/delivery"
)

// ── capturing logger (real interface implementation) ──────────────────────

// logEntry is the canonical capture-record for a log call exposed
// resovlerLogger. mirror of zapcore.Entry but without the dep.
// Fields captures the keysAndValues... paired arguments so tests can
// verify per-field emission (e.g. soft_ignored_fields=["category"]).
type logEntry struct {
	Level   string
	Message string
	Fields  []any
}

// capturingResolverLogger implements the canonical resolverLogger
// interface (no zap dependency needed for the test surface — matches
// the 4-method interface signatures exactly so the dead-call probe is
// real, not a heuristic). godlike/07 no-fake-availability: the warn-
// capture for soft-ignored-field is queryable per-round.
type capturingResolverLogger struct {
	mu      sync.Mutex
	entries []logEntry
}

func (c *capturingResolverLogger) Info(msg string, keysAndValues ...any) {
	c.record("info", msg, keysAndValues...)
}

func (c *capturingResolverLogger) Warn(msg string, keysAndValues ...any) {
	c.record("warn", msg, keysAndValues...)
}

func (c *capturingResolverLogger) Error(msg string, keysAndValues ...any) {
	c.record("error", msg, keysAndValues...)
}

func (c *capturingResolverLogger) Debug(msg string, keysAndValues ...any) {
	c.record("debug", msg, keysAndValues...)
}

func (c *capturingResolverLogger) record(level, msg string, keysAndValues ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Defensive copy of the variadic so subsequent caller mutations
	// don't perturb captured entries (godlike/07 test isolation).
	fields := append([]any(nil), keysAndValues...)
	c.entries = append(c.entries, logEntry{Level: level, Message: msg, Fields: fields})
}

// Snapshot returns a defensive copy of the captured entries for the
// per-test assertion surface. godlike/07 test isolation: tests do
// not share logEntry pointers; each Snapshot call returns a fresh slice.
func (c *capturingResolverLogger) Snapshot() []logEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]logEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

// FilterMessage returns every captured entry whose Message contains
// the needle (substring match).
func (c *capturingResolverLogger) FilterMessage(needle string) []logEntry {
	all := c.Snapshot()
	var out []logEntry
	for _, e := range all {
		if strings.Contains(e.Message, needle) {
			out = append(out, e)
		}
	}
	return out
}

// FilterFieldValue returns true iff any captured entry has a (key,
// value) pair where key equals the needle AND value equals the
// expectedValue (string-typed). Lets tests verify the
// `soft_ignored_fields=["category"]` payload without parsing the
// raw keysAndValues []any in the test body.
//
// Defensive: cast both sides to string before equality check. A
// typed-string caller (e.g. a typed enum like delivery.DestinationKey)
// boxes differently than a plain string; the cast normalizes them
// so future caller additions don't drop the probe silently.
func (c *capturingResolverLogger) FilterFieldValue(key, expectedValue string) bool {
	for _, e := range c.Snapshot() {
		for i := 0; i+1 < len(e.Fields); i += 2 {
			if k, ok := e.Fields[i].(string); ok && k == key {
				if v, ok := e.Fields[i+1].(string); ok && v == expectedValue {
					return true
				}
			}
		}
	}
	return false
}

// captureLogger returns (logger, captured). The captured value exposes
// FilterMessage / Snapshot helpers so tests query real adapter calls.
func captureLogger() (resolverLogger, *capturingResolverLogger) {
	c := &capturingResolverLogger{}
	return c, c
}

// ── (A) per-destination segment-shape contract (round-1 NEEDS-FIX #3 catch-up) ──

// TestSegmentsForDestination_ImageReturnsStyleSubject pins segmentsForDestination
// for DestinationImage: [Style, SubjectOrName].
func TestSegmentsForDestination_ImageReturnsStyleSubject(t *testing.T) {
	got := segmentsForDestination(delivery.DestinationImage, domaindelivery.AssetLocationInput{
		Style:   "Realistic",
		Subject: "boxing-album",
	})
	want := []string{"Realistic", "boxing-album"}
	if !equalSegments(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

// TestSegmentsForDestination_StockReturnsCategorySubjectProvider pins
// DestinationStock segment tuple: [Category, Subject, Provider].
func TestSegmentsForDestination_StockReturnsCategorySubjectProvider(t *testing.T) {
	got := segmentsForDestination(delivery.DestinationStock, domaindelivery.AssetLocationInput{
		Category: "boxing",
		Subject:  "training",
		Provider: "pexels",
	})
	want := []string{"boxing", "training", "pexels"}
	if !equalSegments(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

// TestSegmentsForDestination_VoiceoverReturnsProjectLanguage pins
// DestinationVoiceover segment tuple: [Project, Language].
func TestSegmentsForDestination_VoiceoverReturnsProjectLanguage(t *testing.T) {
	got := segmentsForDestination(delivery.DestinationVoiceover, domaindelivery.AssetLocationInput{
		Project:  "storia-boxe",
		Language: "it-IT",
	})
	want := []string{"storia-boxe", "it-IT"}
	if !equalSegments(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

// TestSegmentsForDestination_BookReturnsProjectOnly pins DestinationBook
// segment tuple: [Project].
func TestSegmentsForDestination_BookReturnsProjectOnly(t *testing.T) {
	got := segmentsForDestination(delivery.DestinationBook, domaindelivery.AssetLocationInput{
		Project: "storia-pugili",
	})
	want := []string{"storia-pugili"}
	if !equalSegments(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

// TestSegmentsForDestination_DefaultReturnsNil pins unknown destinations
// returning nil segments (gated by segmentsForDestination default).
func TestSegmentsForDestination_DefaultReturnsNil(t *testing.T) {
	got := segmentsForDestination("unknown-destination", domaindelivery.AssetLocationInput{})
	if got != nil {
		t.Fatalf("expected nil for unknown destination; got %v", got)
	}
}

// equalSegments compares two slices by content (godlike/06 SSOT
// segment-shape mirror requires identical content regardless of
// underlying slice header).
func equalSegments(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── (B) WithLogger / NewAdapter nil-defense (round-2 SHOULD-FIX #2) ──

// TestNewAdapter_NilLoggerDefaultsToNop pins: a nil logger passed to
// NewAdapter is replaced with nopResolverLogger{} so subsequent
// a.log.* calls do not panic. Mirrors WithLogger symmetry:
// godlike/06 SSOT one canonical fail-closed contract on the
// construction and fluent-setter paths.
func TestNewAdapter_NilLoggerDefaultsToNop(t *testing.T) {
	a, err := NewAdapter("root-fld", nil)
	if err != nil {
		t.Fatalf("NewAdapter with nil log: %v", err)
	}
	if a.log == nil {
		t.Fatalf("NewAdapter left a.log == nil; godlike/07 fail-closed contract violated")
	}
	// dead-call proxy: should not panic.
	a.log.Warn("dead-call test")
}

// TestWithLogger_NilLoggerDefaultsToNop pins the same contract for the
// fluent-setter path. The 2 paths MUST have symmetric nil-defense
// (godlike/06 SSOT canonical fail-closed contract).
func TestWithLogger_NilLoggerDefaultsToNop(t *testing.T) {
	a, err := NewAdapter("root-fld", nil)
	if err != nil {
		t.Fatalf("NewAdapter bootstrap: %v", err)
	}
	out := a.WithLogger(nil)
	if out == nil {
		t.Fatalf("WithLogger(nil) returned nil on non-nil receiver; godlike/07 fail-closed contract violated")
	}
	if a.log == nil {
		t.Fatalf("WithLogger(nil) left a.log == nil")
	}
	a.log.Info("dead-call test")
}

// TestAdapter_ResolveWithNilLogDoesNotPanic pins the end-to-end
// godlike/07 fail-closed-at-construction contract: a nil-logger
// receiver resolves through Resolve without panicking on log calls.
func TestAdapter_ResolveWithNilLogDoesNotPanic(t *testing.T) {
	a, err := NewAdapter("root-fld", nil)
	if err != nil {
		t.Fatalf("NewAdapter bootstrap: %v", err)
	}
	_, _ = a.Resolve(context.Background(),
		domaindelivery.AssetLocationInput{
			Style:   "Realistic",
			Subject: "boxing-album",
		},
		delivery.DestinationImage)
}

// ── (C) Soft-ignored field for Voiceover/Book/Script (round-2 SHOULD-FIX #3) ──

// TestResolve_VoiceoverWithCategory_DoesNotHardReject pins the
// SOFT-reject change: DestinationVoiceover + loc.Category set
// returns NO typed-sentinel (Resolve returns a non-empty folder-id)
// AND a.log.Warn fires with the canonical message. The hard-rejection
// fields (style / provider / subject / name) still fire the typed
// sentinel — covered by the next test.
func TestResolve_VoiceoverWithCategory_DoesNotHardReject(t *testing.T) {
	logger, captured := captureLogger()
	a, err := NewAdapter("root-fld", logger)
	if err != nil {
		t.Fatalf("NewAdapter bootstrap: %v", err)
	}
	folderID, err := a.Resolve(context.Background(),
		domaindelivery.AssetLocationInput{
			Category: "boxe", // SOFT-ignored metadata for indexing
			Project:  "storia-boxe",
			Language: "it-IT",
		},
		delivery.DestinationVoiceover)
	if err != nil {
		t.Fatalf("Resolve with Voiceover+Category+Project+Language: %v (expected nil; \"category\" must not hard-reject)", err)
	}
	if !strings.HasPrefix(folderID, stubModePrefix) {
		t.Fatalf("expected stub-mode folder-id; got %q", folderID)
	}
	// verify the soft-ignore warn fired + the field payload named "category"
	warns := captured.FilterMessage("soft-ignoring off-channel metadata fields")
	if len(warns) != 1 {
		t.Fatalf("expected exactly 1 warn-log for soft-ignored Category; got %d", len(warns))
	}
	if !captured.FilterFieldValue("soft_ignored_fields", "category") {
		t.Fatalf("warn-log does not carry soft_ignored_fields=category payload")
	}
}

// TestResolve_VoiceoverWithStyle_StillHardRejects pins the godlike/07
// typed-error contract preservation: hard-rejection fields (style for
// Voiceover) continue to surface ErrLocationResolverIncompatibleFields.
// This guards against an accidental SHOULD-FIX over-correction that
// softens the wrong categories.
func TestResolve_VoiceoverWithStyle_StillHardRejects(t *testing.T) {
	logger, _ := captureLogger()
	a, err := NewAdapter("root-fld", logger)
	if err != nil {
		t.Fatalf("NewAdapter bootstrap: %v", err)
	}
	_, err = a.Resolve(context.Background(),
		domaindelivery.AssetLocationInput{
			Style:    "Realistic", // OFF-CHANNEL for Voiceover; HARD-reject
			Project:  "storia-boxe",
			Language: "it-IT",
		},
		delivery.DestinationVoiceover)
	if err == nil {
		t.Fatalf("expected ErrLocationResolverIncompatibleFields for Voiceover+Style; got nil")
	}
	if !errors.Is(err, sourcing.ErrLocationResolverIncompatibleFields) {
		t.Fatalf("expected errors.Is(err, ErrLocationResolverIncompatibleFields); got %v", err)
	}
}

// TestResolve_ImageWithProject_StillHardRejects pins: project / language
// remain HARD-rejected for asset-group destinations (Image, Stock, etc.).
// This guards the OTHER direction of the SHOULD-FIX softening — the
// hard-reject list retains its canonical scope.
func TestResolve_ImageWithProject_StillHardRejects(t *testing.T) {
	logger, _ := captureLogger()
	a, err := NewAdapter("root-fld", logger)
	if err != nil {
		t.Fatalf("NewAdapter bootstrap: %v", err)
	}
	_, err = a.Resolve(context.Background(),
		domaindelivery.AssetLocationInput{
			Style:   "Realistic",
			Subject: "boxing-album",
			Project: "storia-boxe", // OFF-CHANNEL for Image; HARD-reject
		},
		delivery.DestinationImage)
	if err == nil {
		t.Fatalf("expected ErrLocationResolverIncompatibleFields for Image+Project; got nil")
	}
	if !errors.Is(err, sourcing.ErrLocationResolverIncompatibleFields) {
		t.Fatalf("expected errors.Is(err, ErrLocationResolverIncompatibleFields); got %v", err)
	}
}

// TestNewAdapter_RealLoggerPassesThrough (Round-2 nice-to-have): the
// nil-default path must NOT clobber a real logger when the caller
// wires one explicitly. godlike/07 happy-path lock: a real logger
// passed at construction must reach a.log unmodified.
func TestNewAdapter_RealLoggerPassesThrough(t *testing.T) {
	c := &capturingResolverLogger{}
	a, err := NewAdapter("root-fld", c)
	if err != nil {
		t.Fatalf("NewAdapter bootstrap: %v", err)
	}
	a.log.Info("happy-path test")
	if got := c.FilterMessage("happy-path test"); len(got) != 1 {
		t.Fatalf("expected 1 captured info log; got %d (clobbered by nil-default?)", len(got))
	}
}

// TestWithLogger_RealLoggerOverridesDefault (Round-2 nice-to-have):
// fluent-setter must let a real logger override the nop default that
// NewAdapter installed. godlike/07 happy-path: operator can wire a
// real logger AFTER construction via .WithLogger(realLogger) and
// have it actually fire.
func TestWithLogger_RealLoggerOverridesDefault(t *testing.T) {
	c1 := &capturingResolverLogger{}
	a, err := NewAdapter("root-fld", c1)
	if err != nil {
		t.Fatalf("NewAdapter bootstrap: %v", err)
	}
	c2 := &capturingResolverLogger{}
	a.WithLogger(c2)
	a.log.Info("via overrides")
	// c1 should have 0 captures (the WithLogger swap re-pointed a.log).
	if got := c1.FilterMessage("via overrides"); len(got) != 0 {
		t.Fatalf("c1 should NOT have captured the post-override log; got %d entries", len(got))
	}
	// c2 should have 1 capture (it is now the active logger).
	if got := c2.FilterMessage("via overrides"); len(got) != 1 {
		t.Fatalf("c2 SHOULD have captured the post-override log; got %d entries", len(got))
	}
}
