package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestMattDamonFiveClipsRenderContract pins the canonical visual contract of
// the Matt Damon 5-clip verification job: the Pale Olive Classic plate
// (`classic1`) behind an 85%-scaled foreground with a top-right text watermark
// and burned subtitles.
//
// It is deliberately a REQUEST-level gate (the manifest is what an operator
// submits), so it fails the moment any of the three verified facts drifts:
//
//   - background.asset_id == "classic1"  → the Pale Olive layer is really there
//   - foreground_scale_percent == 85     → the plate is actually VISIBLE (100%
//     paints the source over the whole canvas and hides the background)
//   - watermark.position == "top_right"  → the watermark is where it was verified
//
// A live run must additionally pixel-probe the produced clip; this test only
// guarantees the submitted intent cannot regress silently.
func TestMattDamonFiveClipsRenderContract(t *testing.T) {
	path := filepath.Join("..", "..", "..", "ops", "jobs", "matt_damon_5_clips.generate.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var envelope scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("manifest violates GenerationEnvelopeV2: %v", err)
	}

	item := envelope.Items[0]
	if len(item.Source.ClipIDs) != 5 {
		t.Fatalf("clip_ids = %d, want 5 (the 5-clip verification contract)", len(item.Source.ClipIDs))
	}

	render := item.Output.Render
	if !render.Enabled {
		t.Fatal("output.render.enabled must be true: the job is a render verification")
	}

	// ── Pale Olive Classic background plate ──────────────────────────
	if render.Background == nil {
		t.Fatal("output.render.background is missing: no background layer would be composited")
	}
	if render.Background.Mode != "asset" {
		t.Errorf("background.mode = %q, want %q", render.Background.Mode, "asset")
	}
	if render.Background.AssetID != "classic1" {
		t.Errorf("background.asset_id = %q, want %q (Pale Olive Classic plate)", render.Background.AssetID, "classic1")
	}

	// ── Foreground scale: the knob that keeps the plate visible ──────
	if render.ForegroundScalePercent != 85 {
		t.Errorf("foreground_scale_percent = %d, want 85 (100 hides the Pale Olive plate)", render.ForegroundScalePercent)
	}

	// ── Top-right text watermark ─────────────────────────────────────
	if render.Watermark == nil {
		t.Fatal("output.render.watermark is missing")
	}
	if !render.Watermark.Enabled {
		t.Error("watermark.enabled must be true")
	}
	if render.Watermark.Position != "top_right" {
		t.Errorf("watermark.position = %q, want %q", render.Watermark.Position, "top_right")
	}
	if render.Watermark.Text == "" {
		t.Error("watermark.text must be non-empty (an asset watermark needs asset_id instead)")
	}

	// ── Burned subtitles ─────────────────────────────────────────────
	if render.Subtitles == nil {
		t.Fatal("output.render.subtitles is missing")
	}
	if !render.Subtitles.Enabled {
		t.Error("subtitles.enabled must be true")
	}
	if render.Subtitles.Mode != "burn" {
		t.Errorf("subtitles.mode = %q, want %q", render.Subtitles.Mode, "burn")
	}

	// Normalize is the runtime path: it must be a NO-OP on this explicit
	// contract (85 and top_right survive), never a silent override back to
	// the 100% / default-position behavior this test exists to prevent.
	render.Normalize()
	if render.ForegroundScalePercent != 85 {
		t.Errorf("Normalize changed foreground_scale_percent to %d, want 85 preserved", render.ForegroundScalePercent)
	}
	if render.Watermark.Position != "top_right" {
		t.Errorf("Normalize changed watermark.position to %q, want top_right preserved", render.Watermark.Position)
	}
}
