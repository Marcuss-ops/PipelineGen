package media

import (
	"reflect"
	"testing"
)

func TestSlotKind_IsValid(t *testing.T) {
	cases := []struct {
		name string
		slot SlotKind
		want bool
	}{
		{"primary_video", SlotPrimaryVideo, true},
		{"secondary_image", SlotSecondaryImage, true},
		{"evidence_overlay", SlotEvidenceOverlay, true},
		{"map", SlotMap, true},
		{"portrait", SlotPortrait, true},
		{"document", SlotDocument, true},
		{"background", SlotBackground, true},
		{"empty", "", false},
		{"drift", "primary_audio", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.slot.IsValid(); got != c.want {
				t.Errorf("IsValid(%q) = %v, want %v", c.slot, got, c.want)
			}
		})
	}
}

func TestSlotKind_AllowedMediaTypes(t *testing.T) {
	cases := map[SlotKind][]string{
		SlotPrimaryVideo:    {"video"},
		SlotSecondaryImage:  {"image"},
		SlotPortrait:        {"image"},
		SlotMap:             {"image", "map"},
		SlotDocument:        {"document", "image"},
		SlotEvidenceOverlay: {"video", "image"},
		SlotBackground:      {"video", "image"},
		SlotKind("unknown"): nil,
	}

	for slot, want := range cases {
		got := slot.AllowedMediaTypes()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("AllowedMediaTypes(%q) = %v, want %v", slot, got, want)
		}
	}
}

func TestSlotKind_DefaultLayout(t *testing.T) {
	cases := map[SlotKind]string{
		SlotPrimaryVideo:    "fullscreen",
		SlotSecondaryImage:  "right_panel",
		SlotPortrait:        "right_panel",
		SlotEvidenceOverlay: "fullscreen_fade",
		SlotMap:             "fullscreen_fade",
		SlotBackground:      "fullscreen_fade",
		SlotDocument:        "lower_third",
		SlotKind("unknown"): "",
	}

	for slot, want := range cases {
		if got := slot.DefaultLayout(); got != want {
			t.Errorf("DefaultLayout(%q) = %q, want %q", slot, got, want)
		}
	}
}

func TestSlotKind_MaxLayers(t *testing.T) {
	for _, slot := range []SlotKind{
		SlotPrimaryVideo, SlotSecondaryImage, SlotEvidenceOverlay,
		SlotMap, SlotPortrait, SlotDocument, SlotBackground,
	} {
		if got := slot.MaxLayers(); got != 1 {
			t.Errorf("MaxLayers(%q) = %d, want 1", slot, got)
		}
	}
	if got := SlotKind("unknown").MaxLayers(); got != 0 {
		t.Errorf("MaxLayers(unknown) = %d, want 0", got)
	}
}

func TestSlotKind_SupportsTimeRange(t *testing.T) {
	timeRangeSlots := map[SlotKind]bool{
		SlotPrimaryVideo:    true,
		SlotEvidenceOverlay: true,
		SlotBackground:      true,
		SlotSecondaryImage:  false,
		SlotMap:             false,
		SlotPortrait:        false,
		SlotDocument:        false,
		SlotKind("unknown"): false,
	}

	for slot, want := range timeRangeSlots {
		if got := slot.SupportsTimeRange(); got != want {
			t.Errorf("SupportsTimeRange(%q) = %v, want %v", slot, got, want)
		}
	}
}

func TestIsMediaTypeAllowed(t *testing.T) {
	if !IsMediaTypeAllowed(SlotPrimaryVideo, "video") {
		t.Error("expected video in primary_video")
	}
	if IsMediaTypeAllowed(SlotPrimaryVideo, "image") {
		t.Error("did not expect image in primary_video")
	}
	if IsMediaTypeAllowed(SlotPrimaryVideo, "") {
		t.Error("empty media type should be rejected")
	}
}
