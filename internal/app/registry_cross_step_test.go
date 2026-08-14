package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
)

// TestRegistryCrossStepState_IsExplicit protects the composition graph
// boundary introduced for search and idempotency capabilities. Cross-step
// values must travel through registryCrossStepState; RegistryWiring is the
// returned graph result and must not become a temporal scratchpad again.
func TestRegistryCrossStepState_IsExplicit(t *testing.T) {
	root := "."
	files, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(body)
		for _, forbidden := range []string{
			"wiring.searchFanOut",
			"wiring.searchBackends",
			"wiring.searchAgg",
			"wiring.idempotencyHandler",
			"regWiring.searchFanOut",
			"regWiring.searchBackends",
			"regWiring.searchAgg",
			"regWiring.idempotencyHandler",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s reintroduces temporal RegistryWiring field %q; pass registryCrossStepState explicitly", file, forbidden)
			}
		}
	}

	searchSource, err := os.ReadFile("registry_search.go")
	if err != nil {
		t.Fatal(err)
	}
	searchText := string(searchSource)
	start := strings.Index(searchText, "func registerSearchBackend(")
	if start < 0 {
		t.Fatal("registerSearchBackend declaration not found")
	}
	end := strings.IndexByte(searchText[start:], '\n')
	if end < 0 {
		end = len(searchText) - start
	}
	if strings.Contains(searchText[start:start+end], "RegistryWiring") {
		t.Fatal("registerSearchBackend must return capability values, not mutate RegistryWiring")
	}

	if _, _, _, err := registerSearchBackend(nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("registerSearchBackend must reject a nil provider registry")
	}
	providerRegistry := providers.NewRegistry()
	if _, _, _, err := registerSearchBackend(nil, providerRegistry, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("registerSearchBackend must reject an unfrozen provider registry")
	}
	providerRegistry.Freeze()
	if _, _, _, err := registerSearchBackend(nil, providerRegistry, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("frozen empty provider registry should compose an empty search graph: %v", err)
	}

	registrySource, err := os.ReadFile("registry.go")
	if err != nil {
		t.Fatal(err)
	}
	// The exported SearchFanOut accessor is intentional compatibility
	// surface and is populated from the returned state; only the former
	// unexported scratch field is forbidden.
	if strings.Contains(string(registrySource), "\tsearchFanOut search.SearchFanOut") {
		t.Fatal("RegistryWiring must not own unexported cross-step search state")
	}
}
