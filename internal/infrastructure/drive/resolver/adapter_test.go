// Package resolver — DoD #5 TDD contract tests.
//
// SEMANTIC-LOCATION-API-2026-07-06 DoD #5:
// "se root specifica esiste → usa quella
//
//	se root specifica manca → usa media_root_folder + namespace
//	se entrambe mancano → errore chiaro"
//
// The test surface is hermetic — no Drive API, no DB, no Qdrant.
// Each test constructs an Adapter with a specific DriveConfig
// and asserts the resolved root folder for the destination.
package resolver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	sourcing "github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── test helper: fakeFolderEnsurer ─────────────────────────────────────

// fakeFolderEnsurer is a hermetic test double for FolderEnsurer.
// It returns a deterministic folder-id based on the root + segments,
// without touching any real Drive API.
type fakeFolderEnsurer struct {
	// lastRoot captures the rootID passed to EnsureFolder for assertion.
	lastRoot string
	// lastSegs captures the segments passed to EnsureFolder for assertion.
	lastSegs []string
	// err, if set, causes EnsureFolder to return this error.
	err error
}

func (f *fakeFolderEnsurer) EnsureFolder(_ context.Context, rootID string, segments ...string) (string, error) {
	f.lastRoot = rootID
	f.lastSegs = segments
	if f.err != nil {
		return "", f.err
	}
	// Deterministic: join root + segments with "/" to produce a
	// fake Drive folder-id. This mirrors what real EnsureFolderPath
	// would return (a Drive File.ID string).
	parts := make([]string, 0, len(segments)+1)
	parts = append(parts, rootID)
	parts = append(parts, segments...)
	return strings.Join(parts, "/"), nil
}

var _ FolderEnsurer = (*fakeFolderEnsurer)(nil)

// newTestAdapter constructs an Adapter with a fakeFolderEnsurer wired
// so Resolve exercises the real EnsureFolder path (not stub-mode).
func newTestAdapter(driveCfg config.DriveConfig) *Adapter {
	a, _ := NewAdapter(driveCfg, nil)
	return a.WithFolderEnsurer(&fakeFolderEnsurer{})
}

// ── (a) specific root wins over MediaRootFolder ─────────────────────────

// TestRootForDestination_SpecificRootWins pins: when both a destination-
// specific root (e.g. StockRootFolder) AND MediaRootFolder are set,
// the specific root wins (priority 1 in the DoD #5 chain).
func TestRootForDestination_SpecificRootWins(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{
		MediaRootFolder: "media-root",
		StockRootFolder: "stock-specific-root",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	root, err := a.rootForDestination(delivery.DestinationStock)
	if err != nil {
		t.Fatalf("rootForDestination(stock): expected success, got %v", err)
	}
	if root != "stock-specific-root" {
		t.Fatalf("expected stock-specific-root, got %q", root)
	}
}

// ── (b) MediaRootFolder fallback when specific is empty ─────────────────

// TestRootForDestination_FallsBackToMediaRoot pins: when the destination-
// specific root is empty but MediaRootFolder is set, the adapter returns
// MediaRootFolder + the destination namespace (DoD #5 priority 2).
func TestRootForDestination_FallsBackToMediaRoot(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{
		MediaRootFolder: "media-root",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	tests := []struct {
		dest     delivery.DestinationKey
		wantRoot string
	}{
		{delivery.DestinationStock, "media-root"},
		{delivery.DestinationImage, "media-root"},
		{delivery.DestinationYouTubeClip, "media-root"},
		{delivery.DestinationVoiceover, "media-root"},
		{delivery.DestinationBook, "media-root"},
		{delivery.DestinationScript, "media-root"},
		{delivery.DestinationArtlist, "media-root"},
		{delivery.DestinationSoundEffect, "media-root"},
		{delivery.DestinationDocument, "media-root"},
	}
	for _, tt := range tests {
		t.Run(string(tt.dest), func(t *testing.T) {
			root, err := a.rootForDestination(tt.dest)
			if err != nil {
				t.Fatalf("rootForDestination(%q): expected success, got %v", tt.dest, err)
			}
			if root != tt.wantRoot {
				t.Fatalf("rootForDestination(%q): expected %q, got %q", tt.dest, tt.wantRoot, root)
			}
		})
	}
}

// ── (c) ErrDestinationNoRootFolder when both empty ──────────────────────

// TestRootForDestination_NoRootConfigured_ReturnsSentinel pins DoD #5
// priority 3: when NEITHER the destination-specific root NOR
// MediaRootFolder are set, rootForDestination returns the typed
// sentinel ErrDestinationNoRootFolder with the config-key hint so
// operators can grep for the missing key.
func TestRootForDestination_NoRootConfigured_ReturnsSentinel(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	root, err := a.rootForDestination(delivery.DestinationStock)
	if err == nil {
		t.Fatalf("expected ErrDestinationNoRootFolder, got root=%q", root)
	}
	if !errors.Is(err, ErrDestinationNoRootFolder) {
		t.Fatalf("expected errors.Is(err, ErrDestinationNoRootFolder); got %v", err)
	}
	// The error message must contain the config-key hint so operators
	// know exactly which key to set.
	if !strings.Contains(err.Error(), "stock_root_folder") {
		t.Fatalf("expected error to mention 'stock_root_folder'; got %v", err)
	}
	if !strings.Contains(err.Error(), "media_root_folder") {
		t.Fatalf("expected error to mention 'media_root_folder'; got %v", err)
	}
}

// ── (d) different destinations get the right specific root ──────────────

// TestRootForDestination_PerDestinationSpecificRoot pins: each destination
// maps to the correct config field (ImagesFolder → ImagesRootFolder, etc.)
// and the specific root is returned verbatim.
func TestRootForDestination_PerDestinationSpecificRoot(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{
		MediaRootFolder:        "media-root",
		StockRootFolder:        "stock-root",
		ClipsRootFolder:        "clips-root",
		VoiceoverRootFolder:    "vo-root",
		ArtlistRootFolder:      "artlist-root",
		BooksRootFolder:        "books-root",
		ScriptsRootFolder:      "scripts-root",
		ImagesRootFolder:       "images-root",
		SoundEffectsRootFolder: "sfx-root",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	tests := []struct {
		dest     delivery.DestinationKey
		wantRoot string
	}{
		{delivery.DestinationStock, "stock-root"},
		{delivery.DestinationYouTubeClip, "clips-root"},
		{delivery.DestinationVoiceover, "vo-root"},
		{delivery.DestinationArtlist, "artlist-root"},
		{delivery.DestinationBook, "books-root"},
		{delivery.DestinationScript, "scripts-root"},
		{delivery.DestinationImage, "images-root"},
		{delivery.DestinationSoundEffect, "sfx-root"},
		{delivery.DestinationDocument, "scripts-root"}, // DocumentsFolder delegates to ScriptsRootFolder
	}
	for _, tt := range tests {
		t.Run(string(tt.dest), func(t *testing.T) {
			root, err := a.rootForDestination(tt.dest)
			if err != nil {
				t.Fatalf("rootForDestination(%q): %v", tt.dest, err)
			}
			if root != tt.wantRoot {
				t.Fatalf("rootForDestination(%q): want %q, got %q", tt.dest, tt.wantRoot, root)
			}
		})
	}
}

// ── (e) MediaRootFolder trims whitespace ────────────────────────────────

// TestRootForDestination_WhitespaceMediaRoot_Trimmed pins: MediaRootFolder
// with leading/trailing whitespace is trimmed before use (per
// config.ResolveFolder contract).
func TestRootForDestination_WhitespaceMediaRoot_Trimmed(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{
		MediaRootFolder: "  padded-media-root  ",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	root, err := a.rootForDestination(delivery.DestinationImage)
	if err != nil {
		t.Fatalf("rootForDestination(image): %v", err)
	}
	if root != "padded-media-root" {
		t.Fatalf("expected 'padded-media-root', got %q", root)
	}
}

// ── (f) whitespace-only specific root treated as empty ──────────────────

// TestRootForDestination_WhitespaceOnlySpecificRoot_FallsBack pins: when
// the destination-specific root is whitespace-only, the adapter treats
// it as empty and falls back to MediaRootFolder.
func TestRootForDestination_WhitespaceOnlySpecificRoot_FallsBack(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{
		MediaRootFolder: "media-root",
		StockRootFolder: "   ",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	root, err := a.rootForDestination(delivery.DestinationStock)
	if err != nil {
		t.Fatalf("rootForDestination(stock): %v", err)
	}
	if root != "media-root" {
		t.Fatalf("expected fallback to 'media-root', got %q", root)
	}
}

// ── (g) whitespace-only MediaRootFolder + empty specific → sentinel ─────

// TestRootForDestination_WhitespaceMediaRootAndEmptySpecific_ReturnsSentinel
// pins: when MediaRootFolder is whitespace-only AND the specific root is
// empty, both are treated as absent and ErrDestinationNoRootFolder fires.
func TestRootForDestination_WhitespaceMediaRootAndEmptySpecific_ReturnsSentinel(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{
		MediaRootFolder: "   ",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	_, err = a.rootForDestination(delivery.DestinationStock)
	if err == nil {
		t.Fatal("expected ErrDestinationNoRootFolder; got nil")
	}
	if !errors.Is(err, ErrDestinationNoRootFolder) {
		t.Fatalf("expected errors.Is(err, ErrDestinationNoRootFolder); got %v", err)
	}
}

// ── (h) Resolve integration: root populated into folder-id ──────────────

// TestResolve_FolderEnsurerCalledWithCorrectSegments pins: when
// rootForDestination returns "stock-specific-root", the real
// FolderEnsurer is called with the root as rootID and the destination
// key + metadata segments as the segment list.
func TestResolve_FolderEnsurerCalledWithCorrectSegments(t *testing.T) {
	fake := &fakeFolderEnsurer{}
	a, err := NewAdapter(config.DriveConfig{
		StockRootFolder: "stock-specific-root",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a = a.WithFolderEnsurer(fake)

	loc := domaindelivery.AssetLocationInput{Category: "Boxe", Subject: "Ali", Provider: "pexels"}
	folderID, err := a.Resolve(context.Background(), loc, delivery.DestinationStock)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Verify the FolderEnsurer received the correct root and segments.
	if fake.lastRoot != "stock-specific-root" {
		t.Fatalf("EnsureFolder root: expected %q, got %q", "stock-specific-root", fake.lastRoot)
	}
	wantSegs := []string{"stock", "Boxe", "Ali", "pexels"}
	if len(fake.lastSegs) != len(wantSegs) {
		t.Fatalf("EnsureFolder segments: expected %d, got %d (%v)", len(wantSegs), len(fake.lastSegs), fake.lastSegs)
	}
	for i, want := range wantSegs {
		if fake.lastSegs[i] != want {
			t.Fatalf("EnsureFolder segment[%d]: expected %q, got %q", i, want, fake.lastSegs[i])
		}
	}

	// Verify the returned folder-id is from the FolderEnsurer (not stub-mode).
	if strings.HasPrefix(folderID, stubModePrefix) {
		t.Fatalf("expected real Drive folder-id (no stub prefix), got %q", folderID)
	}
	if folderID == "" {
		t.Fatal("expected non-empty folder-id from FolderEnsurer")
	}
}

// ── (i) Resolve with missing root returns sentinel ──────────────────────

// TestResolve_NoRootConfigured_ReturnsSentinel pins: when neither
// specific root nor MediaRootFolder are set, Resolve returns
// ErrDestinationNoRootFolder (fail-closed at call-time).
func TestResolve_NoRootConfigured_ReturnsSentinel(t *testing.T) {
	a := newTestAdapter(config.DriveConfig{})

	loc := domaindelivery.AssetLocationInput{Category: "Boxe", Subject: "Ali", Provider: "pexels"}
	_, err := a.Resolve(context.Background(), loc, delivery.DestinationStock)
	if err == nil {
		t.Fatal("expected ErrDestinationNoRootFolder; got nil")
	}
	if !errors.Is(err, ErrDestinationNoRootFolder) {
		t.Fatalf("expected errors.Is(err, ErrDestinationNoRootFolder); got %v", err)
	}
}

// ── (j) nil-adapter rootForDestination ──────────────────────────────────

// TestRootForDestination_NilAdapter_ReturnsSentinel pins: a nil
// *Adapter receiver for rootForDestination returns the typed sentinel
// so callers don't nil-dereference.
func TestRootForDestination_NilAdapter_ReturnsSentinel(t *testing.T) {
	var a *Adapter
	_, err := a.rootForDestination(delivery.DestinationStock)
	if err == nil {
		t.Fatal("expected ErrDestinationNoRootFolder from nil receiver; got nil")
	}
	if !errors.Is(err, ErrDestinationNoRootFolder) {
		t.Fatalf("expected errors.Is(err, ErrDestinationNoRootFolder); got %v", err)
	}
}

// ── (k) FolderEnsurer not wired returns sentinel ───────────────────────

// TestResolve_FolderEnsurerNotWired_ReturnsSentinel pins: when the
// FolderEnsurer is nil (not wired at composition time), Resolve returns
// ErrFolderEnsurerNotWired for any non-empty Location. godlike/07
// NO-FAKE-AVAILABILITY: the adapter never produces a stub folder-id.
func TestResolve_FolderEnsurerNotWired_ReturnsSentinel(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{
		StockRootFolder: "stock-root",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	// Intentionally NOT calling WithFolderEnsurer — simulates composition
	// root where driveUploader was nil.

	loc := domaindelivery.AssetLocationInput{Category: "Boxe", Subject: "Ali", Provider: "pexels"}
	_, err = a.Resolve(context.Background(), loc, delivery.DestinationStock)
	if err == nil {
		t.Fatal("expected ErrFolderEnsurerNotWired; got nil")
	}
	if !errors.Is(err, ErrFolderEnsurerNotWired) {
		t.Fatalf("expected errors.Is(err, ErrFolderEnsurerNotWired); got %v", err)
	}
}

// ── (l) FolderEnsurer error propagates as ErrFolderEnsureFailed ─────────

// TestResolve_FolderEnsurerError_PropagatesAsWrappedSentinel pins: when
// the FolderEnsurer returns an error, Resolve wraps it with
// ErrFolderEnsureFailed so callers can probe via errors.Is.
func TestResolve_FolderEnsurerError_PropagatesAsWrappedSentinel(t *testing.T) {
	fake := &fakeFolderEnsurer{err: fmt.Errorf("drive API timeout")}
	a, err := NewAdapter(config.DriveConfig{
		StockRootFolder: "stock-root",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a = a.WithFolderEnsurer(fake)

	loc := domaindelivery.AssetLocationInput{Category: "Boxe", Subject: "Ali", Provider: "pexels"}
	_, err = a.Resolve(context.Background(), loc, delivery.DestinationStock)
	if err == nil {
		t.Fatal("expected ErrFolderEnsureFailed; got nil")
	}
	if !errors.Is(err, ErrFolderEnsureFailed) {
		t.Fatalf("expected errors.Is(err, ErrFolderEnsureFailed); got %v", err)
	}
	// The underlying cause must be preserved in the chain.
	if !strings.Contains(err.Error(), "drive API timeout") {
		t.Fatalf("expected error to contain underlying cause; got %v", err)
	}
}

// ── compile-time pin (godlike/06) ───────────────────────────────────────

// Compile-time assertion: Adapter satisfies LocationResolverPort.
var _ sourcing.LocationResolverPort = (*Adapter)(nil)
