package rendering

import (
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func TestValidateRenderedArtifactPublishGate(t *testing.T) {
	valid := capmodel.OverlayProbeResult{
		SizeBytes:  1024,
		DurationUS: 4_000_000,
		SHA256:     strings.Repeat("a", 64),
	}
	if err := validateRenderedArtifact(valid); err != nil {
		t.Fatalf("valid artifact must pass: %v", err)
	}
	for name, probed := range map[string]capmodel.OverlayProbeResult{
		"zero bytes":      {DurationUS: 4_000_000, SHA256: valid.SHA256},
		"zero duration":   {SizeBytes: 1024, SHA256: valid.SHA256},
		"missing sha":     {SizeBytes: 1024, DurationUS: 4_000_000},
		"short sha":       {SizeBytes: 1024, DurationUS: 4_000_000, SHA256: "abc"},
		"zero everything": {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateRenderedArtifact(probed); err == nil {
				t.Fatal("invalid artifact must fail closed")
			}
		})
	}
}
