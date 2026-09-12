package cliprender

// chronon_timing_result_test.go — the raw deep-profile timing sidecar
// reference must survive onto the clip.render job result as a small
// content-addressed block (storage key/url/sha/size), and must be ABSENT when
// the renderer did not preserve a sidecar (never an empty object).

import (
	"testing"
)

func TestRenderedResultCarriesChrononTimingReference(t *testing.T) {
	outcome := &RenderOutcome{
		OutputPath:               "/work/rendered-clip.mp4",
		SizeBytes:                5649534,
		DurationSec:              19,
		Backend:                  BackendChrononVulkan,
		Metrics:                  NewRenderMetricsV2(),
		ChrononTimingStorageKey:  "d95b4617ff8783aa7ed17e000888597f26a0738b1cb55e8269664eac2b4656f1",
		ChrononTimingURL:         "http://store:9000/objects/d95b4617ff8783aa7ed17e000888597f26a0738b1cb55e8269664eac2b4656f1",
		ChrononTimingSHA256:      "d95b4617ff8783aa7ed17e000888597f26a0738b1cb55e8269664eac2b4656f1",
		ChrononTimingSizeBytes:   218,
		ChrononTimingContentType: "application/json",
	}
	result := renderedResult(nil, nil, nil, ClipRenderPlanV1{}, nil, outcome, nil)
	renderBlock, ok := result["render"].(map[string]any)
	if !ok {
		t.Fatalf("render block missing from result: %+v", result)
	}
	ref, ok := renderBlock["chronon_timing"].(map[string]any)
	if !ok {
		t.Fatalf("chronon_timing reference missing from render block: %+v", renderBlock)
	}
	if ref["storage_key"] != outcome.ChrononTimingStorageKey || ref["url"] != outcome.ChrononTimingURL ||
		ref["sha256"] != outcome.ChrononTimingSHA256 || ref["size_bytes"] != int64(218) ||
		ref["content_type"] != "application/json" {
		t.Fatalf("chronon_timing reference corrupted: %+v", ref)
	}
	// The per-frame array itself must never be inlined into the job result.
	if _, ok := ref["frame_times_ms"]; ok {
		t.Fatalf("per-frame array must never be inlined into the job result: %+v", ref)
	}
}

func TestRenderedResultOmitsChrononTimingWithoutPreservedSidecar(t *testing.T) {
	outcome := &RenderOutcome{
		OutputPath: "/work/rendered-clip.mp4",
		SizeBytes:  5649534,
		Backend:    BackendChrononVulkan,
		Metrics:    NewRenderMetricsV2(),
	}
	result := renderedResult(nil, nil, nil, ClipRenderPlanV1{}, nil, outcome, nil)
	renderBlock, ok := result["render"].(map[string]any)
	if !ok {
		t.Fatalf("render block missing from result: %+v", result)
	}
	if _, ok := renderBlock["chronon_timing"]; ok {
		t.Fatalf("chronon_timing present without a preserved sidecar: %+v", renderBlock)
	}
}
