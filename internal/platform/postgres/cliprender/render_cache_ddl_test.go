package cliprender

import (
	"strings"
	"testing"
)

// TestRenderCacheDDLOwnsOnlyTheWholeClipTable pins CR-19-16: the boot schema
// creates the whole-clip cache (plus its asset index) and nothing else. The
// segmented chunk table had no producer and no reader, so recreating it at
// every boot was pure drift.
//
// RA-13 moved the DDL out of capabilities/cliprender; this test moved with it,
// because platform/postgres/cliprender is now the owner of the schema literal.
func TestRenderCacheDDLOwnsOnlyTheWholeClipTable(t *testing.T) {
	if !strings.Contains(renderCacheDDL, "clip_render_cache (") {
		t.Fatal("DDL must create clip_render_cache")
	}
	if !strings.Contains(renderCacheDDL, "idx_clip_render_cache_asset") {
		t.Fatal("DDL must create the clip_render_cache asset index")
	}
	if strings.Contains(renderCacheDDL, "chunk") {
		t.Fatalf("DDL must not mention the removed chunk cache:\n%s", renderCacheDDL)
	}
}
