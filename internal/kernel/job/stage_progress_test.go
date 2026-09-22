package job

import (
	"encoding/json"
	"testing"
)

func TestAggregateStageProgressByStage(t *testing.T) {
	statuses := []StageLanguageStatus{
		{Stage: StageScript, Language: "en", Status: StageCompleted, JobID: "script-en"},
		{Stage: StageTranslation, Language: "it", Status: StageCompleted, JobID: "translation-it"},
		{Stage: StageTranslation, Language: "en", Status: StageRunning, JobID: "translation-en"},
		{Stage: StageVoiceover, Language: "it", Status: StageFailed, JobID: "voiceover-it", Error: "tts failed"},
		{Stage: StageUpload, Language: "it", Status: StageQueued, JobID: "upload-it"},
		{Stage: StagePersistence, Language: "it", Status: StageCompleted, JobID: "persist-it"},
	}

	got := AggregateStageProgressByStage(statuses)
	if got[string(StageTranslation)].Completed != 1 || got[string(StageTranslation)].Total != 2 {
		t.Fatalf("translation progress = %+v, want completed=1 total=2", got[string(StageTranslation)])
	}
	if got[string(StageVoiceover)].Completed != 0 || got[string(StageVoiceover)].Total != 1 {
		t.Fatalf("voiceover progress = %+v, want completed=0 total=1", got[string(StageVoiceover)])
	}
	if got[string(StagePersistence)].Completed != 1 || got[string(StagePersistence)].Total != 1 {
		t.Fatalf("persistence progress = %+v, want completed=1 total=1", got[string(StagePersistence)])
	}
}

// TestCanonicalStageOrderIsThePinnedSequence pins the exact ordered list. The
// order is the fact the parent's progress projection depends on, so a new
// stage added to the enum must be placed here deliberately (and only when a
// producer reports it).
func TestCanonicalStageOrderIsThePinnedSequence(t *testing.T) {
	want := []StageName{
		StageScript,
		StageClips,
		StageStock,
		StageTranslation,
		StageVoiceover,
		StageOverlay,
		StageRender,
		StageUpload,
		StagePersistence,
	}
	got := CanonicalStageOrder()
	if len(got) != len(want) {
		t.Fatalf("CanonicalStageOrder = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CanonicalStageOrder[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCanonicalStageOrderHasNoDuplicates pins that the single ordered stage
// declaration cannot list the same workflow stage twice.
func TestCanonicalStageOrderHasNoDuplicates(t *testing.T) {
	seen := make(map[StageName]int, len(CanonicalStageOrder()))
	for i, stage := range CanonicalStageOrder() {
		if stage == "" {
			t.Fatalf("CanonicalStageOrder[%d] is empty", i)
		}
		if prev, dup := seen[stage]; dup {
			t.Fatalf("stage %q listed twice (indexes %d and %d)", stage, prev, i)
		}
		seen[stage] = i
	}
}

// TestCanonicalStageOrderIsImmutable pins that the returned order is a copy, so
// a caller sorting its own progress cannot silently reorder the declaration for
// the next caller.
func TestCanonicalStageOrderIsImmutable(t *testing.T) {
	order := CanonicalStageOrder()
	order[0] = "mutated"
	if CanonicalStageOrder()[0] == "mutated" {
		t.Fatal("CanonicalStageOrder returned the backing slice, not a copy")
	}
}

// TestMergeStageProgressKeysOnUnit pins the identity of a non-language unit:
// two scenes of the same stage must stay two observations, while a repeat of
// the same unit is an upsert. Without the unit in the identity, an item's
// per-scene clip/stock/overlay observations collapse into one entry.
func TestMergeStageProgressKeysOnUnit(t *testing.T) {
	dst := MergeStageProgress(nil, AggregateStageProgressByStage([]StageLanguageStatus{
		{Stage: StageClips, Unit: "scene-a", Status: StageCompleted},
		{Stage: StageClips, Unit: "scene-b", Status: StageCompleted},
	}))
	if got := dst[string(StageClips)].Total; got != 2 {
		t.Fatalf("clips total = %d, want 2 (one per scene)", got)
	}

	// Same unit observed again: upsert, not a third entry.
	dst = MergeStageProgress(dst, AggregateStageProgressByStage([]StageLanguageStatus{
		{Stage: StageClips, Unit: "scene-a", Status: StageFailed, Error: "boom"},
	}))
	clips := dst[string(StageClips)]
	if clips.Total != 2 {
		t.Fatalf("clips total = %d, want 2 after upsert", clips.Total)
	}
	if clips.Completed != 1 {
		t.Fatalf("clips completed = %d, want 1 (scene-b only)", clips.Completed)
	}
	for _, item := range clips.Languages {
		if item.Unit == "scene-a" && item.Status != StageFailed {
			t.Fatalf("scene-a status = %q, want failed", item.Status)
		}
	}
}

// TestStageLanguageStatusOmitsEmptyUnit pins the wire back-compat: rows for the
// language-scoped stages (translation/voiceover/upload/persistence) carry no
// unit key, so consumers of the existing shape see no change.
func TestStageLanguageStatusOmitsEmptyUnit(t *testing.T) {
	encoded, err := json.Marshal(StageLanguageStatus{
		Stage: StageVoiceover, Language: "it", Status: StageCompleted,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `{"stage":"voiceover","language":"it","status":"completed"}` {
		t.Fatalf("language-scoped observation shape changed: %s", encoded)
	}
}

func TestFlattenStageProgressUsesCanonicalStageOrder(t *testing.T) {
	progress := AggregateStageProgressByStage([]StageLanguageStatus{
		{Stage: StagePersistence, Language: "it", Status: StageCompleted},
		{Stage: StageUpload, Language: "it", Status: StageCompleted},
		{Stage: StageScript, Language: "en", Status: StageCompleted},
	})
	got := FlattenStageProgress(progress)
	if len(got) != 3 || got[0].Stage != StageScript || got[1].Stage != StageUpload || got[2].Stage != StagePersistence {
		t.Fatalf("flattened progress = %+v, want script then upload then persistence", got)
	}
}
