package entities

import (
	"testing"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestOverlayResolverEndMsIsCeilProjection pins the millisecond projection the
// overlay plan validator enforces (floor start, ceil end). The 2026-10-01
// Milton mixed run failed COMPILING_AUDIO with "end_ms 307664 diverges from
// start_us+duration_us 307664500": a truncated (floor) end_ms for a window
// whose µs duration carried a 500 µs remainder.
func TestOverlayResolverEndMsIsCeilProjection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		startUS    int64
		durationUS int64
		wantEndMS  int64
	}{
		{"half-ms remainder", 307664000, 500, 307665},
		{"whole ms", 1000000, 2000000, 3000},
		{"one us remainder", 250000, 100001, 351},
		{"sub-ms window", 999999, 400, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			startMS := tc.startUS / 1000
			startUS := startMS * 1000
			endMS := overlayEndMs(startUS, tc.durationUS)
			require.Equal(t, tc.wantEndMS, endMS, "end_ms is the ceiling projection")
			plan := capabilityoverlay.OverlayPlan{
				SchemaVersion: capabilityoverlay.SchemaVersionPlan,
				PlanID:        "plan-end-ms",
				VideoID:       "video-end-ms",
				Width:         1920,
				Height:        1080,
				FPSNum:        30,
				FPSDen:        1,
				Items: []capabilityoverlay.OverlayItem{{
					ID:         "overlay-scene-0-sao-paulo",
					TemplateID: "person_default",
					StartMs:    startMS,
					EndMs:      endMS,
					StartUS:    startUS,
					DurationUS: tc.durationUS,
				}},
			}
			require.NoError(t, plan.Validate(), "the real overlay plan validator must accept the ceiling projection")
			item := plan.Items[0]
			require.Equal(t, startMS, item.StartMs)
			require.Equal(t, tc.wantEndMS, item.EndMs)
			require.Equal(t, (item.StartUS+item.DurationUS+999)/1000, item.EndMs,
				"end_ms must ceil the authoritative microsecond endpoint")
			require.Equal(t, capabilityoverlay.SchemaVersionPlan, plan.SchemaVersion)
		})
	}

	// Exercise the production resolver too: its emitted plan is validated and
	// sealed before it reaches compilation, so the item projection and render
	// fingerprint must agree with the same ceiling equation.
	timeline := EntityTimeline{
		Version:    EntityTimelineVersion,
		DurationUS: 310_000_000,
		Scenes: []SceneEntityTimeline{{
			SceneID: "scene-2", SceneIndex: 2, TimelineStartUS: 0,
			Entities: []EntityOccurrence{{
				EntityID:   StableEntityID("GPE", "São Paulo"),
				Name:       "São Paulo",
				Type:       "GPE",
				SceneID:    "scene-2",
				SceneIndex: 2,
				TextStart:  0, TextEnd: len("São Paulo"), WordStart: 0, WordEnd: 1,
				LocalStartUS: 307_664_500, LocalEndUS: 307_665_000,
				AudioStartUS: 307_664_500, AudioEndUS: 307_665_000,
				Confidence: 0.9,
			}},
		}},
	}
	resolved, err := ResolveEntityOverlayPlan(timeline, "plan-resolver-end-ms", "video-resolver-end-ms", "", 1920, 1080, 30, 1)
	require.NoError(t, err, "production resolver must emit a plan accepted by OverlayPlan.Validate")
	require.NoError(t, resolved.Validate())
	require.Len(t, resolved.Items, 1)
	item := resolved.Items[0]
	require.Equal(t, item.StartUS/1000, item.StartMs)
	require.Equal(t, overlayEndMs(item.StartUS, item.DurationUS), item.EndMs)
	require.Equal(t, (item.StartUS+item.DurationUS+999)/1000, item.EndMs)
}
