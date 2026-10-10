package rustexec

// Contract-failure certification (R15/R16): the adapter must distinguish a
// dead worker from a lying worker. A dead worker is an error (caller
// continues without editorial); a schema-violating response must never
// become a certified valid product.

import (
	"context"
	"errors"
	"testing"

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
	runner := &phraseImpactFakeRunner{replies: []string{
		`{"ok":true,"result":{"summary":"s","bullet_points":[],"heavy_sentences":[],"chapter_manifest":{"schema_version":"chapter_manifest.v1","chapters":[]}}}`,
		`{"ok":true,"result":{"summary":"s","bullet_points":[],"heavy_sentences":[],"chapter_manifest":{"schema_version":"chapter_manifest.v1","chapters":[]},"scene_highlights":[{"scene_id":"s1","title":"Whatever","title_status":"confident","title_source":"model_guess","bullets":[],"highlights":[],"globally_indexed":true}]}}`,
	}}
	analyzer := NewPhraseImpactAnalyzer("bin/phrase_impact", runner, nil)
	res, err := analyzer.AnalyzeScenes(context.Background(), "Some scene text here with enough words to pass.", "en",
		[]SceneInput{{SceneID: "s1", Text: "Some scene text here with enough words to pass."}})
	require.NoError(t, err, "contract failure must not fail the call")
	assert.False(t, res.SceneHighlightsCertified, "R16: unknown title_status must not certify")
	assert.Empty(t, res.SceneHighlights, "R16: no valid product may be published from a bad schema")
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
