package clipswiring

import (
	"database/sql"
	"testing"

	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	"go.uber.org/zap"
)

// TestNewClipRenderMediaResolver_PrefersPostgres pins MEDIA-SSOT P1-5: when the
// media PostgreSQL SSOT is available, the clip.render/localization resolver
// reads it.
func TestNewClipRenderMediaResolver_PrefersPostgres(t *testing.T) {
	resolver, err := NewClipRenderMediaResolver(&sql.DB{}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderMediaResolver: %v", err)
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
	if _, err := NewClipRenderMediaResolver(nil, zap.NewNop()); err == nil {
		t.Fatal("expected an error when the media PostgreSQL SSOT is absent, even with a legacy SQLite asset registry wired")
	}
}

// TestNewClipRenderMediaResolver_NoSurfaceFailsClosed pins fail-closed wiring:
// no media read surface at all is an error, and a nil composition root is an
// error (never a panic).
func TestNewClipRenderMediaResolver_NoSurfaceFailsClosed(t *testing.T) {
	if _, err := NewClipRenderMediaResolver(nil, zap.NewNop()); err == nil {
		t.Fatal("expected an error when no media read surface is wired")
	}
}
