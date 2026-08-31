package reconcile

import (
	"testing"

	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

func TestParseReconcileQdrantArgs_DefaultsToProductionCollection(t *testing.T) {
	deps, err := parseReconcileQdrantArgs(nil)
	if err != nil {
		t.Fatalf("parse default args: %v", err)
	}
	if deps.Collection != "" {
		t.Fatalf("collection override must not be stored by default, got %q", deps.Collection)
	}
	if qdrantschema.ProductionCollection != "media_assets" {
		t.Fatalf("production collection changed unexpectedly: %q", qdrantschema.ProductionCollection)
	}
}

func TestParseReconcileQdrantArgs_AllowsOnlyProductionCollectionCompatibilityValue(t *testing.T) {
	deps, err := parseReconcileQdrantArgs([]string{"--collection=media_assets"})
	if err != nil {
		t.Fatalf("--collection=media_assets should remain compatible: %v", err)
	}
	if deps.Collection != qdrantschema.ProductionCollection {
		t.Fatalf("collection=%q, want %q", deps.Collection, qdrantschema.ProductionCollection)
	}
}

func TestParseReconcileQdrantArgs_RejectsArbitraryCollectionOverride(t *testing.T) {
	for _, collection := range []string{
		"media_assets_v3",
		"media_assets_v4_recovery_20260817_1712",
		"synthetic_assets_test_v3",
		"other_collection",
	} {
		t.Run(collection, func(t *testing.T) {
			_, err := parseReconcileQdrantArgs([]string{"--collection=" + collection})
			if err == nil {
				t.Fatalf("expected arbitrary collection %q to be rejected", collection)
			}
		})
	}
}

func TestReconcileQdrantDeps_ProductionCollectionIsCanonical(t *testing.T) {
	if err := qdrantschema.ValidateRuntimeCollection(qdrantschema.ProductionCollection); err != nil {
		t.Fatalf("production collection must pass runtime validation: %v", err)
	}
}
