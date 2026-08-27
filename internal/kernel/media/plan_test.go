package media

import (
	"encoding/json"
	"testing"
)

func TestMediaPlanLegacyProvidersNormalizeToPolicy(t *testing.T) {
	var plan MediaPlanSpec
	if err := json.Unmarshal([]byte(`{"providers":{"artlist":true,"internet_images":"enabled"}}`), &plan); err != nil {
		t.Fatalf("unmarshal legacy providers: %v", err)
	}
	if !plan.ProviderPolicy.Artlist.AsBool() || !plan.ProviderPolicy.InternetImages.AsBool() {
		t.Fatalf("legacy providers did not normalize: %#v", plan.ProviderPolicy)
	}
}

func TestMediaPlanSourcesRoundTrip(t *testing.T) {
	input := `{"mode":"hybrid","provider_policy":{"youtube":"enabled"},"sources":[{"segment_id":"segment-001","slot":"primary_video","provider":"youtube","source_url":"https://youtube.com/watch?v=AAA","query":"German invasion Poland September 1939","mode":"required","priority":2}]}`
	var plan MediaPlanSpec
	if err := json.Unmarshal([]byte(input), &plan); err != nil {
		t.Fatalf("unmarshal sources: %v", err)
	}
	if !plan.ProviderPolicy.YouTube.AsBool() || len(plan.Sources) != 1 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	source := plan.Sources[0]
	if source.Mode != SegmentMediaSourceModeRequired || source.Provider != "youtube" || source.Priority != 2 {
		t.Fatalf("unexpected source: %#v", source)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal sources: %v", err)
	}
	var roundTrip MediaPlanSpec
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("round-trip sources: %v", err)
	}
	if len(roundTrip.Sources) != 1 || roundTrip.Sources[0].SourceURL != source.SourceURL {
		t.Fatalf("source lost during round-trip: %#v", roundTrip.Sources)
	}
}

func TestSegmentMediaSourceValidateAcceptsAssetIDOrSourceURL(t *testing.T) {
	base := SegmentMediaSource{SegmentID: "segment-001", Slot: SlotPrimaryVideo, Provider: "youtube", Mode: SegmentMediaSourceModeSuggested}
	for _, source := range []SegmentMediaSource{
		{SegmentID: base.SegmentID, Slot: base.Slot, Provider: base.Provider, AssetID: "yt-stock-001", Mode: base.Mode},
		{SegmentID: base.SegmentID, Slot: base.Slot, Provider: base.Provider, SourceURL: "https://youtube.com/watch?v=AAA", Mode: base.Mode},
	} {
		if err := source.Validate(); err != nil {
			t.Fatalf("valid source rejected: %v", err)
		}
	}
}

func TestSegmentMediaSourceValidateRejectsMissingOrConflictingReferences(t *testing.T) {
	base := SegmentMediaSource{SegmentID: "segment-001", Slot: SlotPrimaryVideo, Provider: "youtube"}
	for name, source := range map[string]SegmentMediaSource{
		"missing references": base,
		"both references":    {SegmentID: base.SegmentID, Slot: base.Slot, Provider: base.Provider, AssetID: "asset-1", SourceURL: "https://example.com/video"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := source.Validate(); err == nil {
				t.Fatal("expected source validation error")
			}
		})
	}
}

func TestSegmentMediaSourceValidateRejectsMissingIdentity(t *testing.T) {
	source := SegmentMediaSource{AssetID: "asset-1", Provider: "youtube", Slot: SlotPrimaryVideo}
	if err := source.Validate(); err == nil {
		t.Fatal("expected missing segment ID error")
	}
}

func TestSegmentMediaSourceModeValidation(t *testing.T) {
	for _, mode := range []SegmentMediaSourceMode{"", SegmentMediaSourceModeSuggested, SegmentMediaSourceModeRequired} {
		if !IsValidSegmentMediaSourceMode(mode) {
			t.Fatalf("mode %q should be valid", mode)
		}
	}
	if IsValidSegmentMediaSourceMode("selected") {
		t.Fatal("selected should not be a source mode")
	}
}

func TestMediaToggleMarshalRejectsInvalidValue(t *testing.T) {
	if _, err := json.Marshal(MediaToggle("corrupt")); err == nil {
		t.Fatal("invalid media toggle serialized successfully")
	}
}
