package wiring

import (
	"database/sql"
	"testing"

	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// TestNewClipRenderMediaResolver_PrefersPostgres pins MEDIA-SSOT P1-5: when the
// media PostgreSQL SSOT is available, the clip.render/localization resolver
// reads it instead of the legacy SQLite detail.Service.
func TestNewClipRenderMediaResolver_PrefersPostgres(t *testing.T) {
	root := &ComposeRoot{MediaPostgres: &sql.DB{}}
	resolver, err := newClipRenderMediaResolver(root, zap.NewNop())
	if err != nil {
		t.Fatalf("newClipRenderMediaResolver: %v", err)
	}
	if _, ok := resolver.(*clipadapters.ClipRenderPGAssetResolver); !ok {
		t.Fatalf("resolver = %T, want *ClipRenderPGAssetResolver", resolver)
	}
}

// TestNewClipRenderMediaResolver_LegacyFallback pins the documented
// graceful-degrade path: media PostgreSQL intentionally disabled falls back to
// the legacy SQLite registry instead of failing boot.
func TestNewClipRenderMediaResolver_LegacyFallback(t *testing.T) {
	root := &ComposeRoot{Repos: &RepoBundle{Assets: &detail.Service{}}}
	resolver, err := newClipRenderMediaResolver(root, zap.NewNop())
	if err != nil {
		t.Fatalf("newClipRenderMediaResolver: %v", err)
	}
	if _, ok := resolver.(*clipadapters.ClipRenderAssetResolver); !ok {
		t.Fatalf("resolver = %T, want *ClipRenderAssetResolver", resolver)
	}
}

// TestNewClipRenderMediaResolver_NoSurfaceFailsClosed pins fail-closed wiring:
// neither the PostgreSQL SSOT nor the legacy registry available is an error.
func TestNewClipRenderMediaResolver_NoSurfaceFailsClosed(t *testing.T) {
	if _, err := newClipRenderMediaResolver(&ComposeRoot{Repos: &RepoBundle{}}, zap.NewNop()); err == nil {
		t.Fatal("expected an error when no media read surface is wired")
	}
	if _, err := newClipRenderMediaResolver(nil, zap.NewNop()); err == nil {
		t.Fatal("expected an error for a nil composition root")
	}
}
