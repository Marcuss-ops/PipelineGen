package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func manifestForModeTest() OverlayManifest {
	return OverlayManifest{
		SchemaVersion: SchemaVersionManifest,
		PlanID:        "plan-mode",
		VideoID:       "video-mode",
		Entries: []OverlayManifestEntry{{
			ItemID: "item-1", InUS: 0, OutUS: 1_000_000, ContentHash: strings.Repeat("a", 64),
		}},
	}
}

func TestOverlayManifestModeAndStreamTransport(t *testing.T) {
	for _, mode := range []string{"", OverlayModeReplace, OverlayModeComposite} {
		doc := manifestForModeTest()
		doc.Mode = mode
		if err := doc.Validate(); err != nil {
			t.Fatalf("mode %q must validate: %v", mode, err)
		}
		want := mode
		if want == "" {
			want = OverlayModeReplace
		}
		if got := doc.OverlayMode(); got != want {
			t.Fatalf("mode %q effective = %q, want %q", mode, got, want)
		}
	}
	for _, mode := range []string{"overlay", "REPLACE ", "compose"} {
		doc := manifestForModeTest()
		doc.Mode = mode
		if err := doc.Validate(); err == nil {
			t.Fatalf("mode %q must be rejected", mode)
		}
	}

	doc := manifestForModeTest()
	doc.Stream = &OverlayManifestStream{
		ProfileID: "VELOX_OVERLAY_VIDEO_ONLY_V3", ExtradataSHA256: strings.Repeat("b", 64),
		TimeBaseNum: 1, TimeBaseDen: 90000, EncoderRevision: "velox-avc1-high40-ref1-bt709-v1",
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("canonical stream must validate: %v", err)
	}
	for name, mutate := range map[string]func(*OverlayManifestStream){
		"missing profile":  func(s *OverlayManifestStream) { s.ProfileID = " " },
		"missing avcC":     func(s *OverlayManifestStream) { s.ExtradataSHA256 = "" },
		"zero time base":   func(s *OverlayManifestStream) { s.TimeBaseDen = 0 },
		"missing revision": func(s *OverlayManifestStream) { s.EncoderRevision = "" },
	} {
		bad := *doc.Stream
		mutate(&bad)
		doc.Stream = &bad
		if err := doc.Validate(); err == nil {
			t.Fatalf("%s: stream identity must be rejected", name)
		}
	}
}

// TestOverlayManifestModeAndStreamSurviveJSON is the transport gate: the
// custom marshaler used to emit only schema/plan/video/timeline/entries, so a
// mode=composite manifest and its certified stream identity were silently
// dropped on the wire. Round-tripping must preserve both.
func TestOverlayManifestModeAndStreamSurviveJSON(t *testing.T) {
	doc := manifestForModeTest()
	doc.Mode = OverlayModeComposite
	doc.Stream = &OverlayManifestStream{
		ProfileID: "VELOX_OVERLAY_VIDEO_ONLY_V3", ExtradataSHA256: strings.Repeat("b", 64),
		TimeBaseNum: 1, TimeBaseDen: 90000, EncoderRevision: "velox-avc1-high40-ref1-bt709-v1",
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded OverlayManifest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Mode != OverlayModeComposite {
		t.Fatalf("mode = %q, want %q", decoded.Mode, OverlayModeComposite)
	}
	if decoded.Stream == nil {
		t.Fatal("stream identity was dropped by the wire")
	}
	if decoded.Stream.ProfileID != doc.Stream.ProfileID || decoded.Stream.TimeBaseDen != 90000 ||
		decoded.Stream.ExtradataSHA256 != doc.Stream.ExtradataSHA256 {
		t.Fatalf("stream round-trip = %+v", decoded.Stream)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("round-trip manifest rejected: %v", err)
	}
}

// TestOverlayManifestDigestSeparatesModeAndStream keeps the composition cache
// honest. Replace and composite draw different pixels, so they must never share
// a cached artifact; an explicit "replace" must still alias the historical
// empty mode so old cache rows remain reachable.
func TestOverlayManifestDigestSeparatesModeAndStream(t *testing.T) {
	base := manifestForModeTest()
	explicitReplace := base
	explicitReplace.Mode = OverlayModeReplace
	if base.Digest() != explicitReplace.Digest() {
		t.Fatal("explicit replace must share the legacy empty-mode identity")
	}
	composite := base
	composite.Mode = OverlayModeComposite
	if base.Digest() == composite.Digest() {
		t.Fatal("replace and composite must not share a composition identity")
	}
	streamed := base
	streamed.Stream = &OverlayManifestStream{
		ProfileID: "VELOX_OVERLAY_VIDEO_ONLY_V3", ExtradataSHA256: strings.Repeat("b", 64),
		TimeBaseNum: 1, TimeBaseDen: 90000, EncoderRevision: "velox-avc1-high40-ref1-bt709-v1",
	}
	if base.Digest() == streamed.Digest() {
		t.Fatal("a certified stream identity must not alias an uncertified artifact")
	}
}
