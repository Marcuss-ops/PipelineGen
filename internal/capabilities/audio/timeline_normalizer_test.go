package audio

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeTimelineJSONConvertsLegacyMilliseconds(t *testing.T) {
	payload := []byte(`{"version":"canonical-timeline.v1","duration_ms":1000,"segments":[{"id":"scene-1","index":0,"timeline_start_ms":0,"duration_ms":1000,"audio":{"mode":"VOICEOVER","voiceover_asset_id":"vo-1"}}]}`)
	timeline, report, err := NormalizeTimelineJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	if timeline.Version != TimelineVersion || timeline.DurationUS != 1_000_000 || timeline.Segments[0].DurationUS != 1_000_000 {
		t.Fatalf("unexpected normalized timeline: %+v", timeline)
	}
	if len(report.DeprecatedFields) != 3 {
		t.Fatalf("deprecated field report = %#v, want duration, segment start, and segment duration", report.DeprecatedFields)
	}
	encoded, err := MarshalTimelineV2(timeline)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "_ms") {
		t.Fatalf("v2 payload contains legacy millisecond field: %s", encoded)
	}
}

func TestNormalizeTimelineJSONAcceptsCoherentDuplicateUnits(t *testing.T) {
	payload := []byte(`{"version":"canonical-timeline.v1","duration_ms":1000,"duration_us":1000000,"segments":[{"id":"scene-1","index":0,"timeline_start_ms":0,"timeline_start_us":0,"duration_ms":1000,"duration_us":1000000,"audio":{"mode":"SILENCE"}}]}`)
	if _, report, err := NormalizeTimelineJSON(payload); err != nil {
		t.Fatal(err)
	} else if len(report.DeprecatedFields) == 0 {
		t.Fatal("legacy fields should be reported during the migration window")
	}
}

func TestNormalizeTimelineJSONRejectsAmbiguousOrUnknownSchema(t *testing.T) {
	cases := []string{
		`{"version":"canonical-timeline.v1","duration_ms":1000,"duration_us":2000000,"segments":[]}`,
		`{"version":"canonical-timeline.v9","duration_us":1000000,"segments":[]}`,
		`{"version":"canonical-timeline.v2","duration_us":1000000,"duration_ms":1000,"segments":[]}`,
	}
	for _, payload := range cases {
		t.Run(payload, func(t *testing.T) {
			if _, _, err := NormalizeTimelineJSON([]byte(payload)); err == nil {
				t.Fatal("ambiguous or unsupported timeline payload must be rejected")
			}
		})
	}
}

func TestNormalizeTimelineJSONRejectsInvertedSourceOut(t *testing.T) {
	payload := []byte(`{"version":"canonical-timeline.v1","duration_ms":1000,"segments":[{"id":"scene-1","index":0,"timeline_start_ms":0,"duration_ms":1000,"video":{"asset_id":"clip-1","source_in_ms":500,"source_out_ms":400},"audio":{"mode":"SILENCE"}}]}`)
	if _, _, err := NormalizeTimelineJSON(payload); err == nil {
		t.Fatal("inverted source range must be rejected")
	}
}

func TestCanonicalTimelineDirectUnmarshalRejectsLegacyFields(t *testing.T) {
	payload := []byte(`{"version":"canonical-timeline.v2","duration_us":1000000,"duration_ms":1000,"segments":[]}`)
	var timeline CanonicalTimeline
	if err := json.Unmarshal(payload, &timeline); err == nil {
		t.Fatal("direct canonical unmarshal must reject legacy fields")
	}
}

func TestCanonicalTimelineV2DetectionIgnoresJSONWhitespace(t *testing.T) {
	payload, err := json.Marshal(testTimeline())
	if err != nil {
		t.Fatal(err)
	}
	if !IsCanonicalTimelineV2(payload) {
		t.Fatalf("v2 payload was not detected: %s", payload)
	}
}
