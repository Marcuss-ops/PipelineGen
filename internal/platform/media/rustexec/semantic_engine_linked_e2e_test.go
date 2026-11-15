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
