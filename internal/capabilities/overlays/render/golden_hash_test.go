package render

import (
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestGoldenHash_OverlayPlanFingerprint pins the byte-identical migration
// contract for the OVERLAY PLAN fingerprint: FingerprintValue() must produce
// the exact SHA-256 literal computed by the current (pre-migration)
// implementation over the canonical golden workload. If a future refactor
// changes the JSON byte shape or the hash algorithm, this literal fails
// loudly instead of silently rewriting every persisted plan fingerprint.
func TestGoldenHash_OverlayPlanFingerprint(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	got := plan.FingerprintValue()
	// Re-pinned 2026-09-12: commit 40905c51b added the derived
	// `duration_ms` field to capoverlay.OverlayItem.MarshalJSON, deliberately
	// changing the plan JSON byte shape (complete millisecond timing
	// tuple on the wire). Persisted pre-change fingerprints remain
	// valid data — verification re-computes against the current shape.
	const want = "41d77687588f1155c9c23ca4b0daba997efa9ccf0a499739f7090c8cb58fa4bb"
	if got != want {
		t.Fatalf("GoldenOverlayPlanV1().FingerprintValue() = %q, want %q (old hash != new hash)", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("fingerprint length = %d, want 64", len(got))
	}
}

// TestGoldenHash_OverlayIntentFingerprint pins the OVERLAY INTENT
// fingerprint (JSON-flattened intent excluding timing state) to the exact
// SHA-256 literal computed by the current implementation. This is the value
// used for intent_fingerprint persistence, so a silent drift would orphan
// every persisted intent match.
func TestGoldenHash_OverlayIntentFingerprint(t *testing.T) {
	intent := capoverlay.OverlayIntent{
		Version:    capoverlay.OverlayIntentVersion,
		IntentID:   "intent-001",
		SceneID:    "scene-01",
		SceneIndex: 0,
		Entity:     capoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
		TemplateID: "person_default",
		Payload:    capoverlay.IntentPayload{Name: "Tom Hanks"},
	}
	got := intent.Fingerprint()
	const want = "19b7e55e400541518f948a5d2329f35f64675f9933c0585c82da006383cf3783"
	if got != want {
		t.Fatalf("capoverlay.OverlayIntent.Fingerprint() = %q, want %q (old hash != new hash)", got, want)
	}
	// The fingerprint must ignore the mutable timing state (same preimage
	// bytes as the pinned intent above).
	frozen := intent
	frozen.TimingState = capoverlay.TimingStateFrozen
	if frozen.Fingerprint() != got {
		t.Fatalf("timing state must be excluded from the fingerprint: %q vs %q", frozen.Fingerprint(), got)
	}
}

// TestGoldenHash_RenderKey pins the RENDER KEY derivation
// (capoverlay.ComputeRenderKey) to the exact SHA-256 literal computed by the current
// implementation for the canonical fixture. Render keys are persisted and
// used for cache reuse, so a silent drift would break every cached render.
func TestGoldenHash_RenderKey(t *testing.T) {
	p := capoverlay.OverlayPlan{SchemaVersion: capoverlay.SchemaVersionPlan, PlanID: "p", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1}
	i := capoverlay.OverlayItem{ID: "o", TemplateID: "entity-card@1", StartMs: 10, EndMs: 20, Text: "Ada", AssetRefs: []capoverlay.OverlayAssetRef{{SHA256: "ABC"}}}
	got := capoverlay.RenderKey(p, i)
	const want = "aac0029c08ebfdcdf3d8661826575fcb2b02e5a106d8d6180780a7131a0fb9a8"
	if got != want {
		t.Fatalf("capoverlay.RenderKey = %q, want %q (old hash != new hash)", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("render key length = %d, want 64", len(got))
	}
}
