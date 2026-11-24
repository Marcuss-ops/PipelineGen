package postgres

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

func TestJSONFactsNilStaysNull(t *testing.T) {
	got, err := jsonFacts(nil)
	if err != nil || got != nil {
		t.Fatalf("jsonFacts(nil) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestJSONFactsSerializesCertification(t *testing.T) {
	got, err := jsonFacts(&model.OutputFacts{Container: "mp4", Width: 1920})
	if err != nil {
		t.Fatalf("jsonFacts: %v", err)
	}
	raw, ok := got.(string)
	if !ok {
		t.Fatalf("jsonFacts returned %T, want string", got)
	}
	var decoded model.OutputFacts
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("jsonFacts returned invalid JSON: %v", err)
	}
	if decoded.Container != "mp4" || decoded.Width != 1920 {
		t.Fatalf("jsonFacts round trip = %+v", decoded)
	}
}

func TestJSONProvenanceRejectsInvalidTelemetry(t *testing.T) {
	_, err := jsonProvenance(&model.ArtifactProvenance{
		SchemaVersion:     "renderinggen.artifact-provenance.v1",
		ProducerTelemetry: json.RawMessage(`{not-json`),
	})
	if err == nil {
		t.Fatal("jsonProvenance accepted an invalid ProducerTelemetry; a failed marshal must not become NULL")
	}
}
