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
