package payloadcheck

// payloadcheck_entity_images_test.go — ITCLOSE-07 regression guard.
//
// `explicit_only` restricts inferred CONTEXTUAL queries only; entity_images is
// a direct operator request and keeps its lane (entityImageLaneEnabled). A rule
// that calls the pair contradictory would send callers back to widening their
// contextual search, which is precisely the silent failure ITCLOSE-07 closed.
// The real contradiction is `image_search_mode: disabled` + entity_images.

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func envelopeWithExtraction(mode string, entitiesEnabled bool) *scriptpkg.GenerationEnvelopeV2 {
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptpkg.PresetCustom,
		Items: []scriptpkg.GenerationItemV2{{
			ID:     "item-1",
			Source: scriptpkg.SourceSpec{Type: "text", Topic: "t"},
		}},
	}
	env.Items[0].MediaPlan.Extraction.ImageSearchMode = mode
	env.Items[0].MediaPlan.Extraction.EntityImages.Enabled = entitiesEnabled
	return env
}

func TestExplicitOnlyKeepsTheEntityImageLane(t *testing.T) {
	findings := SemanticErrors(envelopeWithExtraction(media.ImageSearchModeExplicitOnly, true))
	for _, finding := range findings {
		if strings.Contains(finding, "IMAGE_SEARCH") {
			t.Fatalf("explicit_only + entity_images is a supported combination, got %q", finding)
		}
	}
}

func TestDisabledImageSearchContradictsEntityImages(t *testing.T) {
	findings := SemanticErrors(envelopeWithExtraction(media.ImageSearchModeDisabled, true))
	if len(findings) != 1 || !strings.Contains(findings[0], "IMAGE_SEARCH_DISABLED_WITH_ENTITY_IMAGES") {
		t.Fatalf("findings = %v, want the disabled/entity-images contradiction", findings)
	}
}

func TestDisabledImageSearchAloneIsCoherent(t *testing.T) {
	if findings := SemanticErrors(envelopeWithExtraction(media.ImageSearchModeDisabled, false)); len(findings) != 0 {
		t.Fatalf("findings = %v, want none", findings)
	}
}
