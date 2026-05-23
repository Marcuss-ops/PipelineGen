package images

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnosticsReportsNvidiaAndAnimateScriptState(t *testing.T) {
	scriptsDir := t.TempDir()
	scriptPath := filepath.Join(scriptsDir, "animate_image.py")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env python3\n"), 0644); err != nil {
		t.Fatalf("failed to write script fixture: %v", err)
	}

	svc := &Service{
		imagesDir:     filepath.Join(t.TempDir(), "images"),
		animationsDir: filepath.Join(t.TempDir(), "animations"),
		nvidiaAPIKey:  "real-key",
		nvidiaModel:   "flux-1-dev",
		scriptsDir:    scriptsDir,
	}

	diag := svc.Diagnostics()
	if !diag.NvidiaConfigured {
		t.Fatal("expected nvidia to be reported as configured")
	}
	if !diag.AnimateScriptOK {
		t.Fatal("expected animate script to be reported as present")
	}
	if diag.NvidiaModel != "flux-1-dev" {
		t.Fatalf("unexpected model: %q", diag.NvidiaModel)
	}
}

func TestLooksLikeProperNameAndWikidataSelection(t *testing.T) {
	if !looksLikeProperName("Cus D'Amato") {
		t.Fatal("expected Cus D'Amato to be treated as a proper name")
	}
	if looksLikeProperName("boxing training") {
		t.Fatal("expected generic phrase to not be treated as a proper name")
	}

	hits := []struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}{
		{ID: "Q1", Label: "Rosa D'Amato", Description: "politician"},
		{ID: "Q2", Label: "Cus D'Amato", Description: "boxing trainer"},
	}

	best := selectBestWikidataHit("Cus D'Amato", hits)
	if best == nil || best.Label != "Cus D'Amato" {
		t.Fatalf("expected Cus D'Amato to win selection, got %#v", best)
	}

	wikiHits := []struct {
		Title string `json:"title"`
	}{
		{Title: "Rosa D'Amato"},
		{Title: "Cus D'Amato"},
	}
	if got := selectBestWikiTitle("Cus D'Amato", wikiHits); got != "Cus D'Amato" {
		t.Fatalf("expected Cus D'Amato wiki title, got %q", got)
	}
}
