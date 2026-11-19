package audio

import (
	"encoding/json"
	"testing"
)

func TestCanonicalTimelineDirectUnmarshalRejectsLegacyFields(t *testing.T) {
	payload := []byte(`{"version":"canonical-timeline.v2","duration_us":1000000,"duration_ms":1000,"segments":[]}`)
	var timeline CanonicalTimeline
	if err := json.Unmarshal(payload, &timeline); err == nil {
		t.Fatal("direct canonical unmarshal must reject legacy fields")
	}
}
