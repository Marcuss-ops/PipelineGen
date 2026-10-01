// Package scriptgeneration — map_projection_test.go certifies that a grounded
// place travels from the scene annotations into the planner's per-scene input
// as a map candidate keyed by the content-addressed stable entity id and
// bounded by the grounded occurrence's audio window — and that an ungrounded
// or non-WGS84 place produces nothing at all rather than a fabricated map.
package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// groundedPlaceScene is a scene whose only semantic content is a spoken GPE
// with (optionally) geocoded coordinates.
func groundedPlaceScene(geo *scriptpkg.GeoCoordinate) Scene {
	return Scene{
		ID:   "scene-0",
		Text: map[Language]string{"en": "We drove into New Orleans today."},
		Annotations: &scriptpkg.SceneAnnotations{
			Version: 1, Language: "en", Status: "completed",
			PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				ID: "e-new-orleans", CanonicalName: "New Orleans", Type: "GPE",
				Confidence: 0.93, Geo: geo,
			}},
		},
	}
}

func groundedPlaceTiming() capabilityaudio.SpeechTimingArtifact {
	return capabilityaudio.SpeechTimingArtifact{
		Version: capabilityaudio.SpeechTimingVersion, Provider: "edge_tts",
		BoundaryMode: capabilityaudio.BoundaryWord, Language: "en",
		TextSHA256: "text-hash", AudioSHA256: "audio-hash", DurationUS: 900_000,
		Words: []capabilityaudio.SpeechWordTiming{
			{Index: 0, Text: "We", StartUS: 0, EndUS: 100_000},
			{Index: 1, Text: "drove", StartUS: 100_000, EndUS: 300_000},
			{Index: 2, Text: "into", StartUS: 300_000, EndUS: 500_000},
			{Index: 3, Text: "New", StartUS: 500_000, EndUS: 700_000},
			{Index: 4, Text: "Orleans", StartUS: 700_000, EndUS: 900_000},
		},
	}
}

func groundedPlaceOccurrence() capabilityentities.EntityOccurrence {
	return capabilityentities.EntityOccurrence{
		EntityID: capabilityentities.StableEntityID("GPE", "New Orleans"),
		Name:     "New Orleans", Type: "GPE", SceneID: "scene-0",
		TextStart: 17, TextEnd: 28, WordStart: 3, WordEnd: 4,
		LocalStartUS: 500_000, LocalEndUS: 900_000,
		TimelineStartUS: 0, AudioStartUS: 500_000, AudioEndUS: 900_000,
		Confidence: 0.93,
	}
}

// TestOverlaySceneInput_GroundedPlaceProducesMapCandidate certifies the
// grounding trace: validated WGS84 plus a grounded occurrence becomes exactly
// one candidate carrying the StableEntityID and the occurrence's audio window.
func TestOverlaySceneInput_GroundedPlaceProducesMapCandidate(t *testing.T) {
	scene := groundedPlaceScene(&scriptpkg.GeoCoordinate{
		Latitude: 29.9511, Longitude: -90.0715, DisplayName: "New Orleans, Louisiana, United States",
	})
	input, err := overlaySceneInput(scene, "en", "en", groundedPlaceTiming(), 0,
		[]capabilityentities.EntityOccurrence{groundedPlaceOccurrence()}, nil)
	require.NoError(t, err)
	require.NotNil(t, input)
	require.Len(t, input.Maps, 1, "a grounded place must produce exactly one map candidate")

	candidate := input.Maps[0]
	require.Equal(t, capabilityentities.StableEntityID("GPE", "New Orleans"), candidate.EntityID,
		"the candidate must join on the content-addressed stable entity id")
	require.Equal(t, "New Orleans", candidate.Label)
	require.Equal(t, 29.9511, candidate.Latitude)
	require.Equal(t, -90.0715, candidate.Longitude)
	require.Equal(t, int64(500_000), candidate.StartUS)
	require.Equal(t, int64(400_000), candidate.DurationUS)
	require.Equal(t, int64(500), candidate.StartMs)
	require.Equal(t, int64(900), candidate.EndMs)
}

// TestOverlaySceneInput_UngroundedOrNonWGS84PlaceProducesNoMap certifies the
// fail-closed admission: without coordinates, without an occurrence, or with a
// point outside WGS84, the scene contributes no map at all.
func TestOverlaySceneInput_UngroundedOrNonWGS84PlaceProducesNoMap(t *testing.T) {
	occurrence := groundedPlaceOccurrence()
	timing := groundedPlaceTiming()

	cases := []struct {
		name       string
		geo        *scriptpkg.GeoCoordinate
		occurrence []capabilityentities.EntityOccurrence
	}{
		{"no geocoded coordinates", nil, []capabilityentities.EntityOccurrence{occurrence}},
		{"no grounded occurrence", &scriptpkg.GeoCoordinate{Latitude: 29.9511, Longitude: -90.0715}, nil},
		{"latitude outside WGS84", &scriptpkg.GeoCoordinate{Latitude: 999, Longitude: -90.0715}, []capabilityentities.EntityOccurrence{occurrence}},
		{"longitude outside WGS84", &scriptpkg.GeoCoordinate{Latitude: 29.9511, Longitude: 500}, []capabilityentities.EntityOccurrence{occurrence}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, err := overlaySceneInput(groundedPlaceScene(tc.geo), "en", "en", timing, 0, tc.occurrence, nil)
			require.NoError(t, err)
			if input != nil {
				require.Empty(t, input.Maps, "no map may be fabricated for an ungrounded or non-WGS84 place")
			}
		})
	}
}
