package jobs

import (
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func TestFinalJobLanguageRequestCreatesIndependentTargetRun(t *testing.T) {
	parent := scriptgen.GenerateRequest{
		SourceLanguage: "it", Languages: []scriptgen.Language{"pt", "pl", "ru", "it", "en"},
		VoiceoverLanguages: []scriptgen.Language{"pt", "pl", "ru", "it", "en"},
		IdempotencyKey:     "mike-tyson", Project: "mike-tyson",
		OutputName: "tyson", FinalJob: true,
		Render: scriptgen.GenerateRequest{}.Render,
	}
	parent.Render.DriveSubfolderName = "tyson"
	parent.MediaPlan.Cache = media.MediaCachePolicy{Read: false, Write: false}
	parent.ForceRefresh = true
	parent.Source.ForceRefresh = true
	parent.ScriptParams.ForceRefresh = true
	parent.MediaPlan.ForceRefreshAssets = true
	parent.MediaPlan.ForceRefreshBindings = true
	parent.MediaPlan.ForceRefreshExtraction = false

	child := finalJobLanguageRequest(parent, "pl")
	if child.SourceLanguage != "it" {
		t.Fatalf("source language = %q, want original input language it", child.SourceLanguage)
	}
	if len(child.Languages) != 1 || child.Languages[0] != "pl" {
		t.Fatalf("translation targets = %v, want [pl]", child.Languages)
	}
	if len(child.VoiceoverLanguages) != 1 || child.VoiceoverLanguages[0] != "pl" {
		t.Fatalf("voiceover targets = %v, want [pl]", child.VoiceoverLanguages)
	}
	if !child.FinalJob || child.IdempotencyKey != "mike-tyson:language:pl" {
		t.Fatalf("child final-job identity = (%t, %q)", child.FinalJob, child.IdempotencyKey)
	}
	if child.Render.DriveSubfolderName != "tyson-pl" || child.OutputName != "tyson-pl" {
		t.Fatalf("child output routing = (%q, %q), want distinct pl outputs", child.Render.DriveSubfolderName, child.OutputName)
	}
	if !child.MediaPlan.Cache.Read || !child.MediaPlan.Cache.Write {
		t.Fatalf("shared media cache policy = %+v, want read/write enabled", child.MediaPlan.Cache)
	}
	if child.ForceRefresh || child.Source.ForceRefresh || child.ScriptParams.ForceRefresh || child.MediaPlan.ForceRefreshAssets || child.MediaPlan.ForceRefreshBindings {
		t.Fatal("language child should reuse source assets and bindings from shared cache")
	}
	if !child.MediaPlan.ForceRefreshExtraction {
		t.Fatal("language child must rerun entity extraction")
	}
	if len(parent.Languages) != 5 || len(parent.VoiceoverLanguages) != 5 {
		t.Fatal("building one language run mutated the parent request")
	}
}

func TestUniqueTargetLanguagesPreservesOrderAndDeduplicates(t *testing.T) {
	got := uniqueTargetLanguages([]scriptgen.Language{"pt", "pl", "PT", "ru", "it", "en"})
	want := []string{"pt", "pl", "ru", "it", "en"}
	if len(got) != len(want) {
		t.Fatalf("languages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("languages = %v, want %v", got, want)
		}
	}
}
