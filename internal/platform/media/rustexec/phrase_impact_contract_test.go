package rustexec

// Contract-failure certification (R15/R16): the adapter must distinguish a
// dead worker from a lying worker. A dead worker is an error (caller
// continues without editorial); a schema-violating response must never
// become a certified valid product.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContractWorkerDownIsAnError(t *testing.T) {
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact",
		&phraseImpactFakeRunner{runErr: errors.New("no such process")}, nil)
	_, err := analyzer.AnalyzeScenes(context.Background(), "Some scene text here.", "en",
		[]SceneInput{{SceneID: "s1", Text: "Some scene text here."}})
	require.Error(t, err, "R15: dead worker must surface an error, not an empty certified manifest")
}

func TestContractUnknownTitleStatusIsUncertified(t *testing.T) {
	// One unified pass now carries both the legacy products and the scene
	// highlights in a single response (RUST-01).
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"result":{"summary":"s","bullet_points":[],"heavy_sentences":[],"chapter_manifest":{"schema_version":"chapter_manifest.v1","chapters":[]},"scene_highlights":[{"scene_id":"s1","title":"Whatever","title_status":"confident","title_source":"model_guess","bullets":[],"highlights":[],"globally_indexed":true}]}}`,
	}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)
	res, err := analyzer.AnalyzeScenes(context.Background(), "Some scene text here with enough words to pass.", "en",
		[]SceneInput{{SceneID: "s1", Text: "Some scene text here with enough words to pass."}})
	require.NoError(t, err, "contract failure must not fail the call")
	assert.False(t, res.SceneHighlightsCertified, "R16: unknown title_status must not certify")
	assert.Empty(t, res.SceneHighlights, "R16: no valid product may be published from a bad schema")
}

func TestAnalyzeScenesRunsASingleWorkerPass(t *testing.T) {
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"result":{"summary":"s","bullet_points":[],"heavy_sentences":[],"chapter_manifest":{"schema_version":"chapter_manifest.v1","chapters":[]}}}`,
	}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)
	_, err := analyzer.AnalyzeScenes(context.Background(), "Some scene text here with enough words to pass.", "en",
		[]SceneInput{{SceneID: "s1", Text: "Some scene text here with enough words to pass."}})
	require.NoError(t, err)
	// RUST-01: the legacy double pass went split? no — highlights used to run
	// in a second worker call; the unified contract is exactly one call that
	// carries the structured identity.
	assert.Equal(t, 1, runner.requestCount(), "unified AnalyzeScenes must make exactly one worker call")
	var request struct {
		SceneInputs []map[string]any `json:"scene_inputs"`
	}
	lines := strings.Split(string(bytes.TrimRight(runner.requests, "\n")), "\n")
	require.Len(t, lines, 1)
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &request))
	if len(request.SceneInputs) != 1 || request.SceneInputs[0]["scene_id"] != "s1" {
		t.Fatalf("scene_inputs = %#v, want the structured identity in the single call", request.SceneInputs)
	}
}

func TestAnalyzeScenesCarriesGroundedSemanticBulletsInSingleWorkerPass(t *testing.T) {
	transcript := "Opening context. Factory orders declined for the sixth consecutive quarter."
	bullet := "Factory orders declined for the sixth consecutive quarter."
	start := strings.Index(transcript, bullet)
	end := start + len(bullet)
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"result":{"summary":"s","bullet_points":[],"heavy_sentences":[],"chapter_manifest":{"schema_version":"chapter_manifest.v1","chapters":[]}}}`,
	}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)
	_, err := analyzer.AnalyzeScenesWithSemanticBullets(context.Background(), transcript, "en", []SceneInput{{
		SceneID: "s1", Text: bullet, StartByte: &start, EndByte: &end,
	}}, []scriptpkg.SemanticBulletSpan{{StartByte: start, EndByte: end, Text: bullet}})
	require.NoError(t, err)
	require.Equal(t, 1, runner.requestCount())
	var request struct {
		SemanticBullets []scriptpkg.SemanticBulletSpan `json:"semantic_bullet_spans"`
	}
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(runner.requests), &request))
	require.Equal(t, []scriptpkg.SemanticBulletSpan{{StartByte: start, EndByte: end, Text: bullet}}, request.SemanticBullets)
}

func TestContractDuplicateIdentityIsUncertified(t *testing.T) {
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact",
		&phraseImpactFakeRunner{replies: []string{
			`{"ok":true,"result":{"summary":"s","bullet_points":[],"heavy_sentences":[],"chapter_manifest":{"schema_version":"chapter_manifest.v1","chapters":[]}}}`,
		}}, nil)
	_, err := analyzer.AnalyzeScenes(context.Background(), "Text one. Text two.", "en",
		[]SceneInput{
			{SceneID: "s1", Text: "Text one."},
			{SceneID: "s1", Text: "Text two."},
		})
	require.Error(t, err, "client-side duplicate identity must fail closed before the worker")
}
