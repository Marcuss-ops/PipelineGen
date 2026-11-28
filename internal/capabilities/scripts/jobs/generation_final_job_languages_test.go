package jobs

import (
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestFinalJobLanguageRequestCreatesIndependentTargetRun(t *testing.T) {
	parent := scriptgen.GenerateRequest{
		SourceLanguage: "it", Languages: []scriptgen.Language{"pt", "pl", "ru", "it", "en"},

		IdempotencyKey: "mike-tyson", Project: "mike-tyson",
		OutputName: "tyson", FinalJob: true,
		Render: scriptgen.GenerateRequest{}.Render, AudioConfig: scriptgen.AudioConfig{VoiceoverLanguages: []scriptgen.Language{"pt", "pl", "ru", "it", "en"}},
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

func TestSourceLanguageChildIsMasterWithoutTranslationTarget(t *testing.T) {
	parent := scriptgen.GenerateRequest{
		SourceLanguage: "en", Languages: []scriptgen.Language{"en", "it"},
		TranslationProvider: "ollama", TranslationModel: "gemma4:e2b",
		AudioConfig: scriptgen.AudioConfig{VoiceoverLanguages: []scriptgen.Language{"en", "it"}},
	}
	child := finalJobLanguageRequest(parent, "en")
	if len(child.Languages) != 0 {
		t.Fatalf("source-language translation targets = %v, want none", child.Languages)
	}
	if child.TranslationProvider != "" || child.TranslationModel != "" {
		t.Fatalf("source-language child retained translator configuration: provider=%q model=%q", child.TranslationProvider, child.TranslationModel)
	}
	if len(child.VoiceoverLanguages) != 1 || child.VoiceoverLanguages[0] != "en" {
		t.Fatalf("source-language voiceover targets = %v, want [en]", child.VoiceoverLanguages)
	}
}

func TestSeedFinalJobLanguageRequestReusesMasterSceneText(t *testing.T) {
	parent := scriptgen.GenerateRequest{SourceLanguage: "en"}
	parent.ScriptParams.Segments = []scriptpkg.ScriptSegment{
		{ID: "scene-a", Topic: "first", SourceText: "instruction one", ClipIDs: []string{"clip-a"}},
		{ID: "scene-b", Topic: "second", SourceText: "instruction two", ClipIDs: []string{"clip-b"}},
	}
	request := finalJobLanguageRequest(parent, "it")
	request.MediaPlan.ForceRefreshExtraction = true
	master := &scriptgen.GenerateResult{Scenes: []scriptgen.Scene{
		{ID: "scene-a", Text: map[scriptgen.Language]string{"en": "Mike Tyson faced prison life."}},
		{ID: "scene-b", Text: map[scriptgen.Language]string{"en": "His fortune vanished at full speed."}},
	}}

	if err := seedFinalJobLanguageRequest(&request, master); err != nil {
		t.Fatalf("seedFinalJobLanguageRequest: %v", err)
	}
	if !request.ScriptParams.SourceTextVerbatim {
		t.Fatal("localized child must consume the master script verbatim")
	}
	if got, want := request.ScriptParams.Segments[0].SourceText, "Mike Tyson faced prison life."; got != want {
		t.Fatalf("first scene source = %q, want %q", got, want)
	}
	if got, want := request.ScriptParams.Segments[1].SourceText, "His fortune vanished at full speed."; got != want {
		t.Fatalf("second scene source = %q, want %q", got, want)
	}
	if request.ScriptParams.Segments[0].ClipIDs[0] != "clip-a" || request.ScriptParams.Segments[1].ClipIDs[0] != "clip-b" {
		t.Fatalf("master text seeding changed clip bindings: %+v", request.ScriptParams.Segments)
	}
	if request.MediaPlan.ForceRefreshExtraction {
		t.Fatal("localized child must reuse extraction from the master script")
	}
	if got, want := request.Source.SourceText, "Mike Tyson faced prison life.\n\nHis fortune vanished at full speed."; got != want {
		t.Fatalf("seeded source text = %q, want %q", got, want)
	}
	if got := request.Languages; len(got) != 1 || got[0] != "it" {
		t.Fatalf("translation target changed during master seeding: %v", got)
	}
}

func TestSourceLanguageFirstMovesMasterAheadWithoutDroppingTargets(t *testing.T) {
	got := sourceLanguageFirst([]string{"it", "es", "en", "fr"}, "en")
	want := []string{"en", "it", "es", "fr"}
	if len(got) != len(want) {
		t.Fatalf("languages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("languages = %v, want %v", got, want)
		}
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
