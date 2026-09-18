// Package e2e — argos_sidecar_live_test.go: the LOCAL Argos translation
// runtime certificate.
//
// WHY THIS TEST EXISTS: the multilingual pipeline's fast path is
// "Argos primary + Ollama fallback" (config/multilingual.yaml
// translation_provider: argos). Argos is a local, deterministic, CPU-only
// translator that must be PRESENT for that strategy to be real: when the
// .venv-argos interpreter or the .argosmodel packages are missing, the
// composition root logs "ArgosTranslator unavailable; using Ollama-only
// translation" and EVERY scene/cue/language pays an LLM round-trip — the
// measured translation bottleneck. This test is the gate that turns
// "silently Ollama-only" into a visible, reproducible failure.
//
// It needs NO database, NO YouTube, and NO Whisper: the sidecar is spawned the
// same way the production adapter spawns it (same interpreter, same
// ARGOS_PACKAGES_DIR), and each registry language must come back non-empty.
//
// Gate (opt-in, like the other live tests):
//
//	VELOX_E2E_ARGOS_LIVE=1 go test ./tests/e2e -run TestLiveArgosSidecarTranslatesRegistryLanguages -count=1 -v
//
// Overrides: VELOX_E2E_ARGOS_PYTHON (.venv-argos/bin/python3),
//
//	VELOX_E2E_ARGOS_SCRIPTS_DIR (scripts),
//	VELOX_E2E_ARGOS_PACKAGE_DIR (data/argos-packages).
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"go.uber.org/zap"
)

// argosRegistryTargets is the target-language list of the canonical registry
// (config/multilingual.yaml) minus the "en" source pivot.
var argosRegistryTargets = []string{"it", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}

func TestLiveArgosSidecarTranslatesRegistryLanguages(t *testing.T) {
	if strings.TrimSpace(os.Getenv("VELOX_E2E_ARGOS_LIVE")) != "1" {
		t.Skip("set VELOX_E2E_ARGOS_LIVE=1 to run the local Argos sidecar certificate")
	}

	pythonBin := liveEnv("VELOX_E2E_ARGOS_PYTHON", filepath.Join("..", "..", ".venv-argos", "bin", "python3"))
	scriptsDir := liveEnv("VELOX_E2E_ARGOS_SCRIPTS_DIR", filepath.Join("..", "..", "scripts"))
	packageDir := liveEnv("VELOX_E2E_ARGOS_PACKAGE_DIR", filepath.Join("..", "..", "data", "argos-packages"))

	if _, err := os.Stat(pythonBin); err != nil {
		t.Fatalf("Argos interpreter %s is missing: run scripts/requirements-argos.txt install "+
			"(python3 -m venv .venv-argos && .venv-argos/bin/pip install -r scripts/requirements-argos.txt): %v",
			pythonBin, err)
	}
	if entries, err := os.ReadDir(packageDir); err != nil || len(entries) == 0 {
		t.Fatalf("no Argos models in %s: run .venv-argos/bin/python scripts/tools/argos_install_models.py (err=%v)", packageDir, err)
	}

	adapter, err := translation.NewArgosServerTranslator(translation.ArgosServerConfig{
		PythonBin:   pythonBin,
		ScriptsDir:  scriptsDir,
		PackageDir:  packageDir,
		Concurrency: 4,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("Argos sidecar unavailable: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	source := "The boxer enters the ring and the crowd goes silent."
	start := time.Now()
	for _, target := range argosRegistryTargets {
		langStart := time.Now()
		res, err := adapter.Translate(ctx, translation.TranslationCommand{
			SourceLang: "en",
			TargetLang: target,
			Text:       source,
		})
		if err != nil {
			t.Fatalf("translate en->%s: %v", target, err)
		}
		elapsed := time.Since(langStart)
		if strings.TrimSpace(res.TranslatedText) == "" {
			t.Fatalf("en->%s returned empty text", target)
		}
		if res.TranslatedText == source {
			t.Fatalf("en->%s returned the source text unchanged", target)
		}
		if res.UsedProvider != translation.ProviderArgos {
			t.Fatalf("en->%s provider = %q, want %q", target, res.UsedProvider, translation.ProviderArgos)
		}
		t.Logf("en->%s (%s) in %s: %q", target, res.UsedModel, elapsed.Round(time.Millisecond), res.TranslatedText)
	}
	t.Logf("Argos translated %d languages in %s", len(argosRegistryTargets), time.Since(start).Round(time.Millisecond))
}
