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
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	sourcing "github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/domain/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

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

// TestResolve_RootInStubFolderID pins: when rootForDestination returns
// "stock-specific-root", the stub folder-id includes it in the
// composed path (e.g. "stub-shift:stock-specific-root/stock/Cat/Sub").
func TestResolve_RootInStubFolderID(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{
		StockRootFolder: "stock-specific-root",
	}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	loc := domaindelivery.AssetLocationInput{Category: "Boxe", Subject: "Ali", Provider: "pexels"}
	folderID, err := a.Resolve(context.Background(), loc, delivery.DestinationStock)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	wantPrefix := stubModePrefix + "/stock-specific-root/stock/Boxe/Ali/pexels"
	if folderID != wantPrefix {
		t.Fatalf("expected folder-id %q, got %q", wantPrefix, folderID)
	}
}

// ── (i) Resolve with missing root returns sentinel ──────────────────────

// TestResolve_NoRootConfigured_ReturnsSentinel pins: when neither
// specific root nor MediaRootFolder are set, Resolve returns
// ErrDestinationNoRootFolder (fail-closed at call-time).
func TestResolve_NoRootConfigured_ReturnsSentinel(t *testing.T) {
	a, err := NewAdapter(config.DriveConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	loc := domaindelivery.AssetLocationInput{Category: "Boxe", Subject: "Ali", Provider: "pexels"}
	_, err = a.Resolve(context.Background(), loc, delivery.DestinationStock)
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

// ── compile-time pin (godlike/06) ───────────────────────────────────────

// Compile-time assertion: Adapter satisfies LocationResolverPort.
var _ sourcing.LocationResolverPort = (*Adapter)(nil)
