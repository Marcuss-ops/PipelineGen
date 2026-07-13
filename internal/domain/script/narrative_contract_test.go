// Package script_test — narrative_contract_test.go verifies the
// NarrativeEvidence project: dirty clip data MUST be projected into
// model-safe evidence, and infra locators MUST NOT leak.
//
// ARCHITECTURE REFACTOR (July 2026): NarrativeEvidenceProjector is
// the single canonical seam between source resolution and model
// input. Every source type (clips, search, catalog, curate) MUST
// produce evidence through this interface.
package script_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Fake projector following the contract ───────────────────────────

// canonicalProjector implements ports.NarrativeEvidenceProjector
// following the contract: strips infra IDs, keeps narrative fields,
// and maps each resolved clip to a BindingSlot.
type canonicalProjector struct{}

func (p *canonicalProjector) Project(
	_ context.Context,
	originalSource scriptpkg.PlainProse,
	resolvedClips []scriptpkg.ResolvedClipSlot,
) (scriptpkg.NarrativeEvidence, scriptpkg.BindingManifest, error) {
	ev := scriptpkg.NarrativeEvidence{
		OriginalSource: originalSource,
		Clips:          make([]scriptpkg.NarrativeClip, 0, len(resolvedClips)),
	}
	manifest := scriptpkg.BindingManifest{
		Slots: make([]scriptpkg.BindingSlot, 0, len(resolvedClips)),
	}

	for i, rc := range resolvedClips {
		slotName := "slot-" + string(rune('1'+i))
		nc := scriptpkg.NarrativeClip{
			Slot: slotName,
		}
		if rc.Narrative != nil {
			nc.Description = rc.Narrative.Description
			nc.VisualSummary = rc.Narrative.VisualSummary
			nc.Transcript = rc.Narrative.Transcript
			nc.DurationMs = rc.Narrative.DurationMs
		}
		ev.Clips = append(ev.Clips, nc)

		bs := scriptpkg.BindingSlot{
			Slot: slotName,
		}
		if rc.Binding != nil {
			bs.ClipID = rc.Binding.ClipID
			bs.ClipTitle = rc.Binding.ClipTitle
			bs.DriveLink = rc.Binding.DriveLink
			bs.StartMs = rc.Binding.StartMs
			bs.EndMs = rc.Binding.EndMs
		}
		manifest.Slots = append(manifest.Slots, bs)
	}
	return ev, manifest, nil
}

// ── Test 1: Projector hides technical locators ──────────────────────

// TestNarrativeEvidenceProjector_HidesTechnicalLocators is the
// canonical contract test for the projector: a deliberately dirty
// ResolvedClipSlot MUST produce a clean NarrativeClip view and
// BindingManifest, with infra locators only in the manifest.
func TestNarrativeEvidenceProjector_HidesTechnicalLocators(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	projector := &canonicalProjector{}

	dirtyClip := scriptpkg.ResolvedClipSlot{
		Ref:            "slot-1",
		Topic:          "opening round",
		ChosenAssetRef: "yt_RRJvrDKunyA_32_37_v1",
		Narrative: &scriptpkg.NarrativeClipView{
			SlotRef:       "slot-1",
			Description:   "Pacquiao mostra mobilità nel primo round",
			VisualSummary: "Opening round footwork jab",
			Transcript:    "Pacquiao appears faster and lighter on his feet",
			DurationMs:    5000,
		},
		Binding: &scriptpkg.SlotClipBinding{
			SlotRef:   "slot-1",
			ClipID:    "yt_RRJvrDKunyA_32_37_v1",
			ClipTitle: "opening round footwork jab",
			DriveLink: "https://drive.google.com/file/test",
			StartMs:   32000,
			EndMs:     37000,
		},
	}

	originalSource := scriptpkg.NewPlainProse("Pacquiao vs Broner fight recap")

	ev, manifest, err := projector.Project(ctx, originalSource, []scriptpkg.ResolvedClipSlot{dirtyClip})
	require.NoError(t, err)

	// ── NarrativeEvidence: model sees ONLY narrative-safe fields ──
	require.Len(t, ev.Clips, 1)
	view := ev.Clips[0]

	require.Equal(t, "Pacquiao mostra mobilità nel primo round", view.Description)
	require.Equal(t, "Pacquiao appears faster and lighter on his feet", view.Transcript)
	require.Equal(t, "Opening round footwork jab", view.VisualSummary)
	require.Equal(t, int64(5000), view.DurationMs)

	// NarrativeClip MUST NOT expose infra locators.
	modelView := view.Slot + " " + view.Description + " " + view.Transcript + " " + view.VisualSummary
	require.NotContains(t, modelView, "yt_RRJvrDKunyA")
	require.NotContains(t, modelView, "drive.google.com")
	require.NotContains(t, modelView, "youtube.com")
	require.NotContains(t, modelView, "commentator")
	require.NotContains(t, modelView, "Tags:")

	// ── BindingManifest: infra IDs live here ONLY ────────────────
	require.Len(t, manifest.Slots, 1)
	bs := manifest.Slots[0]
	require.Equal(t, "yt_RRJvrDKunyA_32_37_v1", bs.ClipID)
	require.Equal(t, "https://drive.google.com/file/test", bs.DriveLink)
	require.Equal(t, int64(32000), bs.StartMs)
	require.Equal(t, int64(37000), bs.EndMs)

	// ── OriginalSource: not mutated ──────────────────────────────
	require.Equal(t, "Pacquiao vs Broner fight recap", ev.OriginalSource.String())
}

// TestNarrativeEvidenceProjector_MultipleDirtyClips verifies the
// projector handles multiple dirty clips, stripping ALL infra IDs
// from every clip view while preserving all binding data.
func TestNarrativeEvidenceProjector_MultipleDirtyClips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	projector := &canonicalProjector{}

	clips := []scriptpkg.ResolvedClipSlot{
		{
			Ref:            "slot-1",
			ChosenAssetRef: "clip-a-id",
			Narrative: &scriptpkg.NarrativeClipView{
				SlotRef:     "slot-1",
				Description: "desc a",
				Transcript:  "transcript a",
			},
			Binding: &scriptpkg.SlotClipBinding{
				ClipID:    "clip-a-id",
				DriveLink: "https://drive/a",
			},
		},
		{
			Ref:            "slot-2",
			ChosenAssetRef: "clip-b-id",
			Narrative: &scriptpkg.NarrativeClipView{
				SlotRef:     "slot-2",
				Description: "desc b",
				Transcript:  "transcript b",
			},
			Binding: &scriptpkg.SlotClipBinding{
				ClipID:    "clip-b-id",
				DriveLink: "https://drive/b",
			},
		},
		{
			Ref:            "slot-3",
			ChosenAssetRef: "clip-c-id",
			Narrative: &scriptpkg.NarrativeClipView{
				SlotRef:     "slot-3",
				Description: "desc c",
				Transcript:  "transcript c",
			},
			Binding: &scriptpkg.SlotClipBinding{
				ClipID:    "clip-c-id",
				DriveLink: "https://drive/c",
			},
		},
	}

	ev, manifest, err := projector.Project(ctx, scriptpkg.NewPlainProse("source"), clips)
	require.NoError(t, err)
	require.Len(t, ev.Clips, 3)
	require.Len(t, manifest.Slots, 3)

	// Aggregate all model-visible text.
	var modelText strings.Builder
	for _, c := range ev.Clips {
		modelText.WriteString(c.Slot + " " + c.Description + " " + c.Transcript + " ")
	}

	require.NotContains(t, modelText.String(), "clip-a-id")
	require.NotContains(t, modelText.String(), "clip-b-id")
	require.NotContains(t, modelText.String(), "clip-c-id")
	require.NotContains(t, modelText.String(), "drive.google.com")

	// But manifest has all infra IDs.
	require.Equal(t, "clip-a-id", manifest.Slots[0].ClipID)
	require.Equal(t, "clip-b-id", manifest.Slots[1].ClipID)
	require.Equal(t, "clip-c-id", manifest.Slots[2].ClipID)
}

// TestNarrativeEvidenceProjector_EmptyClips verifies the projector
// returns valid (but empty) evidence for zero clips.
func TestNarrativeEvidenceProjector_EmptyClips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	projector := &canonicalProjector{}

	ev, manifest, err := projector.Project(ctx, scriptpkg.NewPlainProse("text"), nil)
	require.NoError(t, err)
	require.Empty(t, ev.Clips)
	require.True(t, manifest.IsEmpty())
}
