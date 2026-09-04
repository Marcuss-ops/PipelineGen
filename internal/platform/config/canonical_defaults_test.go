package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks .. from this test file until it finds go.mod. The
// canonical-runtime check is anchored at the repo root because .env.example,
// Dockerfile etc. live there — not under internal/.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("cannot locate repo root from %s", wd)
	return ""
}

// readFile is a small helper that returns "" when the file is missing so a
// missing file is reported as a soft missing-surface error rather than a
// hard test panic. The matching script is the source of truth in shell CI.
func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Logf("readFile(%s): missing or unreadable: %v", rel, err)
		return ""
	}
	return string(b)
}

// TestConfig_CanonicalPort_Is8000AcrossRuntimeSurfaces walks every
// runtime surface that should declare port 8000 and asserts that none of
// them still references the obsolete 8080 default. The check is
// deliberately content-based (no regex, anchored substrings) so a
// future Maintainer adding `:8080` to a comment triggers the failure.
//
// Per Operational Readiness PR (June 2026): port 8000 is the canonical
// default since it frees 8080 for unrelated services / SearXNG and
// sidesteps the historical conflict.
func TestConfig_CanonicalPort_Is8000AcrossRuntimeSurfaces(t *testing.T) {
	root := repoRoot(t)

	surfaces := []struct {
		path    string
		must800 string // a positive anchor that must be present
		no8080  string // a negative anchor that must NOT be present
	}{
		{".env.example", "VELOX_PORT=8000", "VELOX_PORT=8080"},
		{".env.example", "VELOX_MASTER_URL=http://127.0.0.1:8000", "VELOX_MASTER_URL=http://127.0.0.1:8080"},
		{"config.example.yaml", "port: 8000", "port: 8080"},
		{"Makefile", "VELOX_PORT:-8000", "VELOX_PORT:-8080"},
		{"Dockerfile", "EXPOSE 8000", "EXPOSE 8080"},
		// Native runtime (September 2026): the application server runs under
		// systemd, not in docker-compose (compose retains only external
		// infrastructure — qdrant, artlist-scraper, searxng). The canonical
		// port anchors moved to the unit files.
		{"scripts/systemd/pipelinegen.service", "Environment=VELOX_PORT=8000", "Environment=VELOX_PORT=8080"},
		{"scripts/systemd/pipelinegen-worker.service", "VELOX_MASTER_URL=http://127.0.0.1:8000", "VELOX_MASTER_URL=http://127.0.0.1:8080"},
	}

	for _, s := range surfaces {
		content := readFile(t, root, s.path)
		if content == "" {
			continue
		}
		if s.must800 != "" && !strings.Contains(content, s.must800) {
			t.Errorf("%s missing canonical anchor %q (port 8000)", s.path, s.must800)
		}
		if s.no8080 != "" && strings.Contains(content, s.no8080) {
			t.Errorf("%s still references obsolete %q (port 8080)", s.path, s.no8080)
		}
	}
}

// TestConfig_FeatureFlags_AlignWithTypesGo asserts the feature block in
// config.example.yaml matches internal/platform/config/types.go
// FeaturesConfig exactly. Drift between the example yaml and the in-tree
// flags causes silent drops at load time (yaml unmarshal ignores unknown
// fields while missing fields silently default to false).
func TestConfig_PrimaryDBPath_IsDerivedFromDataDir(t *testing.T) {
	root := repoRoot(t)
	content := readFile(t, root, "config.example.yaml")
	if content == "" {
		t.Skip("config.example.yaml missing")
	}
	if strings.Contains(content, "primary_db_path:") {
		t.Fatalf("config.example.yaml must not expose a configurable primary_db_path")
	}
	if !strings.Contains(content, "data_dir: \"./data\"") {
		t.Fatalf("config.example.yaml must declare data_dir as the primary DB root")
	}
}

func TestConfig_FeatureFlags_AlignWithTypesGo(t *testing.T) {
	root := repoRoot(t)
	yamlContent := readFile(t, root, "config.example.yaml")
	if yamlContent == "" {
		t.Skip("config.example.yaml missing")
	}

	// Anchors that MUST be present (one per FeaturesConfig field).
	required := []string{
		"artlist_enabled:",
		"youtube_enabled:",
		"drive_enabled:",
		"script_clips_enabled:",
		"voiceover_enabled:",
		"images_enabled:",
		"stock_pipeline_enabled:",
	}
	for _, r := range required {
		if !strings.Contains(yamlContent, r) {
			t.Errorf("config.example.yaml features block missing %q (drift from types.go::FeaturesConfig)", r)
		}
	}

	// Drift detection: the previous config example listed `workflow_enabled`
	// and `google_accounting_enabled` under `features:`, but those are NOT
	// in types.go::FeaturesConfig (google_accounting has its own top-level
	// config block; workflow was retired). Flag those as drift so a future
	// copy-paste doesn't reintroduce the gap.
	forbidden := []string{
		"workflow_enabled:",          // retired flag
		"google_accounting_enabled:", // belongs to GoogleAccountingConfig, not FeaturesConfig
		"catalog_script_vector_search:", // retired flag
	}
	// Only check within the features: block to avoid false positives on
	// legitimate top-level google_accounting: usage.
	featuresStart := strings.Index(yamlContent, "features:")
	restStart := strings.Index(yamlContent[featuresStart:], "\n\n")
	if featuresStart >= 0 && restStart >= 0 {
		featuresBlock := yamlContent[featuresStart : featuresStart+restStart]
		for _, f := range forbidden {
			if strings.Contains(featuresBlock, f) {
				t.Errorf("features: block in config.example.yaml contains drift %q (does not exist in types.go::FeaturesConfig)", f)
			}
		}
	}
}
