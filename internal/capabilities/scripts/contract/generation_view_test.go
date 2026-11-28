package contract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	generation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestGenerationViewProjectsRequest(t *testing.T) {
	req := GenerateRequest{
		TranslationProvider:    "deepl",
		TranslationModel:       "quality",
		TranslationConcurrency: 3,
		Model:                  "llama",
		Tone:                   "documentary",
		Style:                  "cinematic",
		ChannelID:              "channel-1",
		ShortPhraseMotion:      "fade",
		PhraseMotionFamily:     "soft",
		MotionSeed:             "seed",
		EditorialBulletStyles:  []scriptpkg.EditorialBulletStyle{{}},
		MaxPhraseOverlays:      7,
		MaxOverlaysPerScene:    2,
		MaxImageOverlays:       5,
		EditorialDensity:       "balanced",
		EntityStyleID:          "style",
		EntityBadgeColor:       "#ffffff",
		DisableChartOverlays:   true,
		EnableCharts:           true,
		EnableEditorialCompositions: true,
		HeavyPhrasePriority:    0.8,
		ScriptParams:           scriptpkg.ScriptSpec{TargetWords: 900},
		SourceLanguage:         "it",
		Languages:              []Language{"en", "es"},
		GenerateTimeline:       true,
		EnforceClipIdentity:    true,
		Title:                  "Titolo",
		Project:                "project",
		OutputName:             "out.mp4",
	}
	got := req.Generation()
	if got.TranslationProvider != req.TranslationProvider ||
		got.TranslationModel != req.TranslationModel ||
		got.TranslationConcurrency != req.TranslationConcurrency ||
		got.Model != req.Model ||
		got.Tone != req.Tone ||
		got.Style != req.Style ||
		got.ChannelID != req.ChannelID ||
		got.ShortPhraseMotion != req.ShortPhraseMotion ||
		got.PhraseMotionFamily != req.PhraseMotionFamily ||
		got.MotionSeed != req.MotionSeed ||
		got.MaxPhraseOverlays != req.MaxPhraseOverlays ||
		got.MaxOverlaysPerScene != req.MaxOverlaysPerScene ||
		got.MaxImageOverlays != req.MaxImageOverlays ||
		got.EditorialDensity != req.EditorialDensity ||
		got.EntityStyleID != req.EntityStyleID ||
		got.EntityBadgeColor != req.EntityBadgeColor ||
		got.DisableChartOverlays != req.DisableChartOverlays ||
		got.EnableCharts != req.EnableCharts ||
		got.EnableEditorialCompositions != req.EnableEditorialCompositions ||
		got.HeavyPhrasePriority != req.HeavyPhrasePriority ||
		got.SourceLanguage != req.SourceLanguage ||
		!reflect.DeepEqual(got.Languages, req.Languages) ||
		got.GenerateTimeline != req.GenerateTimeline ||
		got.EnforceClipIdentity != req.EnforceClipIdentity ||
		got.Title != req.Title ||
		got.Project != req.Project ||
		got.OutputName != req.OutputName {
		t.Fatalf("generation request projection lost a field: got %+v", got)
	}
}

func TestGenerationViewProjectsResult(t *testing.T) {
	res := GenerateResult{
		Output:               GenerateOutput{Text: "story", WordCount: 42},
		Title:                "Titolo",
		SourceLanguage:       "it",
		OutputName:           "out.mp4",
		WordCount:            42,
		VoiceoverGroup:       "group",
		Summary:              "summary",
		BulletPoints:         []string{"b"},
		BinarySHA256:         "abc",
		RustRelease:          "r1",
		RustGitSHA:           "def",
		RustBinarySHA256:     "engine",
		TranslationMetrics:   &TranslationPipelineMetrics{Calls: 2, Concurrency: 1, WallMS: 10},
		RenderFirstStartedAt: time.Now(),
	}
	got := res.Generation()
	if got.Output.Text != "story" || got.Output.WordCount != 42 {
		t.Fatalf("output projection = %+v", got.Output)
	}
	if got.Title != res.Title || got.SourceLanguage != res.SourceLanguage ||
		got.OutputName != res.OutputName || got.WordCount != res.WordCount ||
		got.VoiceoverGroup != res.VoiceoverGroup || got.Summary != res.Summary ||
		got.BinarySHA256 != res.BinarySHA256 || got.RustRelease != res.RustRelease ||
		got.RustGitSHA != res.RustGitSHA || got.RustBinarySHA256 != res.RustBinarySHA256 {
		t.Fatalf("generation result projection lost a field: got %+v", got)
	}
	if got.TranslationMetrics != (generation.TranslationMetrics{Calls: 2, Concurrency: 1, WallMS: 10}) {
		t.Fatalf("translation metrics = %+v", got.TranslationMetrics)
	}
}

func TestGenerationViewJSONRoundTrip(t *testing.T) {
	view := GenerateRequest{
		TranslationProvider: "deepl",
		Model:               "llama",
		Style:               "cinematic",
		MaxPhraseOverlays:   7,
		SourceLanguage:      "it",
		Languages:           []Language{"en"},
		Title:               "Titolo",
	}.Generation()
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded generation.Request
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view, decoded) {
		t.Fatalf("generation request JSON round-trip mismatch:\n got %+v\nwant %+v", decoded, view)
	}
}

func TestGenerateResultJSONExcludesRuntimeOnlyState(t *testing.T) {
	res := GenerateResult{
		Title:                "Titolo",
		RenderFirstStartedAt: time.Now(),
		RenderLastFinishedAt: time.Now().Add(time.Second),
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"RenderFirstStartedAt", "RenderLastFinishedAt", "renderFirstStartedAt", "renderLastFinishedAt"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("runtime-only field %q reached the JSON payload: %s", forbidden, raw)
		}
	}
}
