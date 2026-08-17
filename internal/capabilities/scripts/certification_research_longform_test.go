package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

const (
	longFormIntroWords      = 300
	longFormBoxerWords      = 680
	longFormConclusionWords = 200
	longFormTargetWords     = 7_300
)

var longFormRankedBoxers = []string{
	"Tyson Fury",
	"Anthony Joshua",
	"Evander Holyfield",
	"Lennox Lewis",
	"George Foreman",
	"Oscar De La Hoya",
	"Manny Pacquiao",
	"Mike Tyson",
	"Canelo Alvarez",
	"Floyd Mayweather Jr.",
}

// researchOrderedLongFormGenerator is a deterministic writer fixture. It
// accepts only the post-research order embedded in Source.SourceText and emits
// one ordered scene aggregate; it never emits ten independent documents.
type researchOrderedLongFormGenerator struct {
	rankedSubjects []string
	calls          int
}

func (g *researchOrderedLongFormGenerator) GenerateSceneText(_ context.Context, req GenerateRequest) ([]Scene, error) {
	g.calls++
	for i, subject := range g.rankedSubjects {
		marker := fmt.Sprintf("RANKED_%02d=%s", i+1, subject)
		if !strings.Contains(req.Source.SourceText, marker) {
			return nil, fmt.Errorf("research order marker missing: %s", marker)
		}
	}

	scenes := make([]Scene, 0, len(g.rankedSubjects)+2)
	scenes = append(scenes, Scene{
		ID:    "intro",
		Index: 0,
		Text:  map[Language]string{"en": longFormNarration("INTRODUCTION", "This introduction frames the research policy, the evidence, and the ranking method for the documentary.", longFormIntroWords)},
	})
	for i, subject := range g.rankedSubjects {
		rank := len(g.rankedSubjects) - i
		heading := fmt.Sprintf("SCENE_%02d RANK_%02d %s", i+1, rank, subject)
		body := fmt.Sprintf("This section examines %s through career earnings, fight paydays, business activity, endorsements, retained wealth, and documented financial setbacks.", subject)
		scenes = append(scenes, Scene{
			ID:    fmt.Sprintf("boxer-%02d", i+1),
			Index: i + 1,
			Text:  map[Language]string{"en": longFormNarration(heading, body, longFormBoxerWords)},
		})
	}
	scenes = append(scenes, Scene{
		ID:    "conclusion",
		Index: len(scenes),
		Text:  map[Language]string{"en": longFormNarration("CONCLUSION", "The conclusion revisits the evidence, the uncertainty between estimates, and the meaning of wealth beyond boxing purses.", longFormConclusionWords)},
	})
	return scenes, nil
}

// longFormNarration creates stable synthetic prose with an exact word count.
// The fixture is intentionally deterministic: this certification tests the
// pipeline's structural and sizing gates, not an external LLM's wording.
func longFormNarration(heading, body string, words int) string {
	seed := strings.Fields(heading + " " + body + " The documentary follows sourced evidence and preserves context across the narrative.")
	out := make([]string, 0, words)
	for len(out) < words {
		out = append(out, seed[len(out)%len(seed)])
	}
	return strings.Join(out, " ")
}

func TestCertification_ResearchToSingleLongFormScript(t *testing.T) {
	repo := newInMemRunRepository()
	writer := &researchOrderedLongFormGenerator{rankedSubjects: append([]string(nil), longFormRankedBoxers...)}
	runner := NewRunner(
		repo,
		writer,
		newStubTranslator(),
		newStubVoiceoverGenerator(),
		newStubDocumentPublisher(),
	)

	req := GenerateRequest{
		IdempotencyKey: "research-longform-certification-v1",
		Title:          "The 10 Richest Boxers of All Time",
		SourceLanguage: "en",
		Languages:      []Language{"en"},
		Project:        "research-longform-certification",
		Audio:          capabilityaudio.AudioModeNone,
		Docs:           DocumentsConfig{Enabled: false},
		ScriptParams:   scriptpkg.ScriptSpec{MinWords: longFormTargetWords},
		Source: Source{
			Type:  SourceText,
			Topic: "The 10 richest boxers of all time",
		},
	}
	var researchMarkers []string
	for i, subject := range longFormRankedBoxers {
		researchMarkers = append(researchMarkers, fmt.Sprintf("RANKED_%02d=%s", i+1, subject))
	}
	req.Source.SourceText = strings.Join(researchMarkers, "\n")

	const runID = "run-research-longform-certification"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)

	require.Equal(t, RunStatusCompleted, final.Status, "long-form certification failed: %s", final.ErrorMessage)
	require.NotNil(t, final.Result)
	require.Equal(t, 1, writer.calls, "one GenerationRun must invoke one writer")

	result := final.Result
	require.Len(t, result.Scenes, 12, "one intro + ten boxer scenes + one conclusion")
	require.Equal(t, longFormTargetWords, result.WordCount)
	require.Equal(t, longFormTargetWords, result.Output.WordCount)
	require.GreaterOrEqual(t, result.WordCount, 7_000)
	require.LessOrEqual(t, result.WordCount, 7_500)

	// The durable output is one ordered script projection, not ten independent
	// scripts or documents.
	var sceneText []string
	for _, scene := range result.Scenes {
		require.NotEmpty(t, scene.ID)
		require.NotEmpty(t, scene.Text["en"])
		sceneText = append(sceneText, scene.Text["en"])
	}
	require.Equal(t, strings.Join(sceneText, "\n\n"), result.Output.Text)
	require.Empty(t, result.Documents)
	require.Zero(t, result.ScriptID)

	// Canonical structure: the intro and conclusion are outside the ten
	// ranked boxer scenes; rank #10 appears first and rank #1 last.
	require.Equal(t, "intro", result.Scenes[0].ID)
	require.Contains(t, result.Scenes[0].Text["en"], "INTRODUCTION")
	for i, subject := range longFormRankedBoxers {
		scene := result.Scenes[i+1]
		require.Equal(t, fmt.Sprintf("boxer-%02d", i+1), scene.ID)
		require.Equal(t, i+1, scene.Index)
		require.Contains(t, scene.Text["en"], fmt.Sprintf("RANK_%02d", len(longFormRankedBoxers)-i))
		require.Contains(t, scene.Text["en"], subject)
	}
	require.Equal(t, "conclusion", result.Scenes[11].ID)
	require.Contains(t, result.Scenes[11].Text["en"], "CONCLUSION")

	// No duplicated scene identity and no placeholder/empty sections.
	seen := make(map[string]struct{}, len(result.Scenes))
	for _, scene := range result.Scenes {
		_, duplicate := seen[scene.ID]
		require.False(t, duplicate, "scene %q must be unique", scene.ID)
		seen[scene.ID] = struct{}{}
	}
}
