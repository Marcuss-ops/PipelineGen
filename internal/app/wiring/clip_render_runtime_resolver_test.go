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
// reads it.
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

// TestNewClipRenderMediaResolver_RequiresPostgres pins the demolition of the
// legacy graceful-degrade path: a legacy SQLite asset registry is NOT a valid
// media read surface any more, so a wiring that offers one while the media
// PostgreSQL SSOT is absent must fail closed instead of silently reading a
// second catalog.
func TestNewClipRenderMediaResolver_RequiresPostgres(t *testing.T) {
	root := &ComposeRoot{Repos: &RepoBundle{Assets: &detail.Service{}}}
	if _, err := newClipRenderMediaResolver(root, zap.NewNop()); err == nil {
		t.Fatal("expected an error when the media PostgreSQL SSOT is absent, even with a legacy SQLite asset registry wired")
	}
}

// TestNewClipRenderMediaResolver_NoSurfaceFailsClosed pins fail-closed wiring:
// no media read surface at all is an error, and a nil composition root is an
// error (never a panic).
func TestNewClipRenderMediaResolver_NoSurfaceFailsClosed(t *testing.T) {
	if _, err := newClipRenderMediaResolver(&ComposeRoot{Repos: &RepoBundle{}}, zap.NewNop()); err == nil {
		t.Fatal("expected an error when no media read surface is wired")
	}
	if _, err := newClipRenderMediaResolver(nil, zap.NewNop()); err == nil {
		t.Fatal("expected an error for a nil composition root")
	}
}
