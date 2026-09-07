package renderinggen

import (
	"encoding/json"
	"testing"

	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
)

// TestToScriptArtifactCarriesChrononTimingReference verifies the raw
// deep-profile sidecar reference preserved by the RenderingGen worker reaches
// the script-generation RenderArtifact (and therefore GET /jobs/{id}
// consumers) intact.
func TestToScriptArtifactCarriesChrononTimingReference(t *testing.T) {
	const timingHash = "d95b4617ff8783aa7ed17e000888597f26a0738b1cb55e8269664eac2b4656f1"
	in := &queueclient.Artifact{
		ID:                       "art-1",
		StorageKey:               "sha-video",
		ArtifactURL:              "http://store:9000/objects/sha-video",
		ArtifactHash:             "sha-video",
		SizeBytes:                1000,
		Metrics:                  map[string]float64{"render_ms": 12.5},
		ChrononTimingStorageKey:  timingHash,
		ChrononTimingURL:         "http://store:9000/objects/" + timingHash,
		ChrononTimingSHA256:      timingHash,
		ChrononTimingSizeBytes:   218,
		ChrononTimingContentType: "application/json",
	}
	got := toScriptArtifact(in)
	if got.ChrononTimingStorageKey != timingHash || got.ChrononTimingURL != "http://store:9000/objects/"+timingHash ||
		got.ChrononTimingSHA256 != timingHash || got.ChrononTimingSizeBytes != 218 ||
		got.ChrononTimingContentType != "application/json" {
		t.Fatalf("chronon timing reference not mapped: %+v", got)
	}

	// The wire JSON projection carries the reference keys too (the artifact
	// travels as JSON to document/assembly consumers).
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}
	if doc["chronon_timing_sha256"] != timingHash || doc["chronon_timing_url"] != "http://store:9000/objects/"+timingHash {
		t.Fatalf("chronon timing reference dropped on the wire: %s", raw)
	}
}

// TestToScriptArtifactOmitsChrononTimingWithoutPreservedSidecar pins the
// fail-open contract: an artifact whose sidecar was not preserved carries no
// reference fields at all (never empty strings or a fake object).
func TestToScriptArtifactOmitsChrononTimingWithoutPreservedSidecar(t *testing.T) {
	got := toScriptArtifact(&queueclient.Artifact{ID: "art-1", StorageKey: "k"})
	if got.ChrononTimingStorageKey != "" || got.ChrononTimingURL != "" || got.ChrononTimingSHA256 != "" ||
		got.ChrononTimingSizeBytes != 0 || got.ChrononTimingContentType != "" {
		t.Fatalf("artifact without preserved sidecar must carry no timing reference: %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}
	if _, ok := doc["chronon_timing_sha256"]; ok {
		t.Fatalf("empty timing reference leaked onto the wire: %s", raw)
	}
}
