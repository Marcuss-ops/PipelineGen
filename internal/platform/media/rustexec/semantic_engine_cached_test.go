package rustexec

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestSemanticEngineReportsAPersistentCacheHit(t *testing.T) {
	cached := strings.Replace(semanticFixture, `"ok": true,`, `"ok": true, "cached": true,`, 1)
	executor := executorForSemantic(&semanticRunner{output: []byte(cached)})
	core, logs := observer.New(zapcore.DebugLevel)
	executor.log = zap.New(core)
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes()); err != nil {
		t.Fatalf("batch analysis failed: %v", err)
	}
	entries := logs.FilterMessage("semantic-engine: manifest served from the persistent cache").All()
	if len(entries) != 1 {
		t.Fatalf("cache hits reported = %d, want 1", len(entries))
	}
}
