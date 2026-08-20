package overlays

import "testing"

// TestPresetCertificationMatrixCompiles certifies the compile-level half of the
// 16-preset matrix: every cell compiles to a chronon.render-plan.v1 document
// that transports the cell's exact preset (and its text/asset). This is the
// deterministic "Render" gate at the plan→document boundary; the pixel and
// Drive columns require the live mini-render harness and are exercised
// separately once the renderer is unblocked.
func TestPresetCertificationMatrixCompiles(t *testing.T) {
	cells := PresetCertificationMatrix()
	if len(cells) != 16 {
		t.Fatalf("matrix has %d cells, want 16", len(cells))
	}

	familyCounts := map[string]int{}
	for _, cell := range cells {
		familyCounts[cell.Family]++
		plan := BuildPresetCertificationPlan(cell, "preset-cert-"+cell.Family+"-"+cell.Preset)
		compiled, err := CompileChrononPlan(plan)
		if err != nil {
			t.Errorf("%s/%s: compile: %v", cell.Family, cell.Preset, err)
			continue
		}
		// The overlay layer is the only non-background layer.
		var layer *ChrononLayer
		for i := range compiled.Plan.Layers {
			if compiled.Plan.Layers[i].ID != "background_video" {
				layer = &compiled.Plan.Layers[i]
				break
			}
		}
		if layer == nil {
			t.Errorf("%s/%s: no overlay layer compiled", cell.Family, cell.Preset)
			continue
		}
		if layer.Preset != cell.Preset {
			t.Errorf("%s/%s: compiled preset = %q, want %q", cell.Family, cell.Preset, layer.Preset, cell.Preset)
		}
		if cell.Template == "IMAGE_OVERLAY" {
			if layer.Asset != "assets/overlay_globe.png" {
				t.Errorf("%s/%s: asset = %q, want assets/overlay_globe.png", cell.Family, cell.Preset, layer.Asset)
			}
		} else if layer.Text != cell.Text {
			t.Errorf("%s/%s: text = %q, want %q", cell.Family, cell.Preset, layer.Text, cell.Text)
		}
	}

	wantFamilies := map[string]int{"name": 3, "phrase": 5, "word": 3, "image": 5}
	for fam, want := range wantFamilies {
		if familyCounts[fam] != want {
			t.Errorf("family %s has %d cells, want %d", fam, familyCounts[fam], want)
		}
	}
}
