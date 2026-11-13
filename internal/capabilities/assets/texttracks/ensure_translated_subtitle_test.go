package texttracks

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func TestEnsureTranslatedSubtitleTrack_ReusesReadyTargetWithoutAssetLister(t *testing.T) {
	svc := &BackfillService{repo: &subtitleTrackRepoStub{ready: map[string][]detail.TimedCue{
		"it": {{StartMs: 0, EndMs: 1000, Text: "ciao"}},
	}}}

	if err := svc.EnsureTranslatedSubtitleTrack(context.Background(), "asset-1", "es", "it"); err != nil {
		t.Fatalf("EnsureTranslatedSubtitleTrack with a READY target: %v", err)
	}
}

func TestEnsureTranslatedSubtitleTrack_RequiresAssetListerForMissingTarget(t *testing.T) {
	svc := &BackfillService{repo: &subtitleTrackRepoStub{ready: map[string][]detail.TimedCue{
		"es": {{StartMs: 0, EndMs: 1000, Text: "hola"}},
	}}}

	err := svc.EnsureTranslatedSubtitleTrack(context.Background(), "asset-1", "es", "it")
	if err == nil || !strings.Contains(err.Error(), "asset lister") {
		t.Fatalf("error = %v, want a missing asset-lister error", err)
	}
}

func TestEnsureTranslatedSubtitleTrack_RejectsSourceAsTarget(t *testing.T) {
	svc := &BackfillService{repo: &subtitleTrackRepoStub{ready: map[string][]detail.TimedCue{}}}

	err := svc.EnsureTranslatedSubtitleTrack(context.Background(), "asset-1", "es", "es")
	if err == nil || !strings.Contains(err.Error(), "equals the source language") {
		t.Fatalf("error = %v, want the source-as-target rejection", err)
	}
}

func TestEnsureTranslatedSubtitleTrack_TextOnlyTargetIsNotReady(t *testing.T) {
	svc := &BackfillService{repo: &subtitleTrackRepoStub{
		ready: map[string][]detail.TimedCue{"it": nil},
		text:  map[string]string{"it": "ciao"},
	}}

	err := svc.EnsureTranslatedSubtitleTrack(context.Background(), "asset-1", "es", "it")
	if err == nil {
		t.Fatal("a track with text but no timed cues must not be treated as subtitle-ready")
	}
}
