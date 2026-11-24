package rustexec

import (
	"context"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"go.uber.org/zap"
)

func TestSemanticEngineRealBinaryProjectsLinkedEntities(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; set VELOX_RUST_SEMANTIC_ENGINE_PATH")
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "it", []scriptgen.SemanticSceneInput{
		{SceneID: "s1", Text: "Nike ha presentato la nuova collezione sportiva. La statua di Nike è esposta al museo."},
		{SceneID: "s2", Text: "L'ESA ha lanciato un nuovo software per l'analisi dei dati."},
	})
	if err != nil {
		t.Fatalf("real semantic-engine v3 batch failed: %v", err)
	}
	bundle := bundles["s1"]
	if bundle == nil || bundle.SchemaVersion != semanticManifestV3 || strings.TrimSpace(bundle.Analyzers.EntityIndex) == "" {
		t.Fatalf("v3 provenance missing: %+v", bundle)
	}
	var linked bool
	for _, identity := range bundle.Identities {
		if identity.CanonicalID != "" && identity.LinkStatus == "linked" && len(identity.SearchIntents) > 0 {
			linked = true
			break
		}
	}
	if !linked {
		t.Fatalf("v3 linked identities missing: %+v", bundle.Identities)
	}
}

func TestSemanticEngineRealBinaryKeepsPolishInflectedSurfaceGrounded(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine binary not installed; set VELOX_RUST_SEMANTIC_ENGINE_PATH")
	}
	adapter, err := NewSemanticEngineAdapter(NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop()))
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := adapter.SemanticBundles(context.Background(), "pl", []scriptgen.SemanticSceneInput{{
		SceneID: "pokoju",
		Text:    "Proszę zamknąć drzwi w pokoju po zakończeniu nagrania.",
	}})
	if err != nil {
		t.Fatalf("Polish inflected identity projection failed: %v", err)
	}
	bundle := bundles["pokoju"]
	if bundle == nil || len(bundle.Identities) == 0 {
		t.Fatalf("expected Polish scene identities, got %+v", bundle)
	}
	for _, identity := range bundle.Identities {
		grounded := false
		for _, mention := range identity.Mentions {
			if mention.Surface == identity.CanonicalSurface {
				grounded = true
				break
			}
		}
		if !grounded {
			t.Errorf("canonical surface %q has no grounded mention: %+v", identity.CanonicalSurface, identity.Mentions)
		}
	}
}
