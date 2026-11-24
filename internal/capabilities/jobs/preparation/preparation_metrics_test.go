package preparation

import (
	"context"
	"testing"
)

type metricsStore struct{ events int }

func (s *metricsStore) AddEvent(context.Context, string, string, string, map[string]any) error {
	s.events++
	return nil
}

func TestPreparationMetrics_RecordAdoptionHitAndWastedWork(t *testing.T) {
	store := &metricsStore{}
	metrics := &PreparationMetrics{store: store}
	err := metrics.RecordAdoption(context.Background(), PreparationAdoptionEvent{JobID: "job-1", UnitID: "tts-1", Fingerprint: "fp", Kind: "TTS", PreparedBeforeClaim: true, Outcome: "adopted", EstimatedSavedMS: 250, SpeculativeWorkWasted: false})
	if err != nil || store.events != 1 {
		t.Fatalf("RecordAdoption hit: err=%v events=%d", err, store.events)
	}
	err = metrics.RecordAdoption(context.Background(), PreparationAdoptionEvent{JobID: "job-1", UnitID: "render-1", Fingerprint: "fp2", Kind: "RENDER", PreparedBeforeClaim: false, Outcome: "stale", SpeculativeWorkWasted: true})
	if err != nil || store.events != 2 {
		t.Fatalf("RecordAdoption wasted: err=%v events=%d", err, store.events)
	}
}
