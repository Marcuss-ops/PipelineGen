package storage

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

func TestCanonicalMediaDBPath_DerivesFromDataDir(t *testing.T) {
	got := CanonicalMediaDBPath("/var/lib/pipelinegen")
	want := filepath.Join("/var/lib/pipelinegen", MediaDBDirectory, MediaDBFilename)
	if got != want {
		t.Fatalf("CanonicalMediaDBPath = %q, want %q", got, want)
	}
	if !strings.HasSuffix(got, "/media/media.db.sqlite") {
		t.Fatalf("CanonicalMediaDBPath %q must end with media/media.db.sqlite", got)
	}
}

func TestCanonicalMediaDBPath_DefaultsEmptyDataDir(t *testing.T) {
	got := CanonicalMediaDBPath("")
	if !strings.HasSuffix(got, "data/media/media.db.sqlite") {
		t.Fatalf("empty DataDir must default to ./data, got %q", got)
	}
}

func TestCanonicalStorageTopology_PinsQdrantIdentity(t *testing.T) {
	top := CanonicalStorageTopology("/data")
	if top.QdrantCollection != schema.ProductionCollection {
		t.Fatalf("QdrantCollection = %q, want %q", top.QdrantCollection, schema.ProductionCollection)
	}
	if top.QdrantAlias != schema.CanonicalRuntimeAlias {
		t.Fatalf("QdrantAlias = %q, want %q", top.QdrantAlias, schema.CanonicalRuntimeAlias)
	}
	if top.MediaDBPath == "" {
		t.Fatal("MediaDBPath must not be empty")
	}
}

func TestRequireRuntimeCollection_RejectsNonProduction(t *testing.T) {
	for _, name := range []string{
		"",
		"media_assets_v3_e5_768_siglip_768",
		"media_assets_v4_recovery_20260817_1712",
		"candidate-foo",
		"recovery-bar",
		schema.CanonicalRuntimeAlias,
		"arbitrary",
	} {
		if err := RequireRuntimeCollection(name); err == nil {
			t.Fatalf("RequireRuntimeCollection(%q) must fail closed", name)
		}
	}
}

func TestRequireRuntimeCollection_AcceptsProductionOnly(t *testing.T) {
	if err := RequireRuntimeCollection(schema.ProductionCollection); err != nil {
		t.Fatalf("RequireRuntimeCollection(%q) must pass: %v", schema.ProductionCollection, err)
	}
}

func TestRequireRuntimeAlias_RejectsNonCanonical(t *testing.T) {
	for _, name := range []string{
		"",
		"media_assets",
		"media_assets_v3_e5_768_siglip_768",
		"arbitrary",
	} {
		if err := RequireRuntimeAlias(name); err == nil {
			t.Fatalf("RequireRuntimeAlias(%q) must fail closed", name)
		}
	}
}

func TestRequireRuntimeAlias_AcceptsCanonicalOnly(t *testing.T) {
	if err := RequireRuntimeAlias(schema.CanonicalRuntimeAlias); err != nil {
		t.Fatalf("RequireRuntimeAlias(%q) must pass: %v", schema.CanonicalRuntimeAlias, err)
	}
}
