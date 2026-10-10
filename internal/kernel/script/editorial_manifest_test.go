package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEditorialFingerprintDiffersOnTopics(t *testing.T) {
	base := EditorialFingerprintInput{
		Transcript: "Germany lost jobs. Industry shrank.", SceneIDs: []string{"s1"},
		SceneTexts: []string{"Germany lost jobs."}, SceneOffsets: [][2]int64{{0, 19}},
		SceneTopics: []string{"jobs"}, Language: "en", ProfileVersion: "highlights.v1",
		RankingWeights: map[string]float64{"lexical": 1}, TokenizerVersion: "rust-splitter.v1",
		EmbeddingModel: "lexical", EmbeddingVersion: "v1", AlgorithmVersion: "phrase-impact.scene-highlights.v1",
	}
	a, err := ComputeEditorialFingerprint(base)
	require.NoError(t, err)
	base.SceneTopics = []string{"automotive"}
	b, err := ComputeEditorialFingerprint(base)
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "same transcript with different topics must not share fingerprint")
}

func TestEditorialValidateRejectsDuplicates(t *testing.T) {
	m := &EditorialManifest{SchemaVersion: EditorialManifestVersion, Scenes: []EditorialScene{
		{SceneID: "s1", TitleStatus: "unavailable"},
		{SceneID: "s1", TitleStatus: "unavailable"},
	}}
	assert.Error(t, m.Validate())
}

func TestEditorialValidateRejectsUnavailableWithTitle(t *testing.T) {
	title := "Scene 03"
	m := &EditorialManifest{SchemaVersion: EditorialManifestVersion, Scenes: []EditorialScene{
		{SceneID: "s1", Title: &title, TitleStatus: "unavailable"},
	}}
	assert.Error(t, m.Validate(), "unavailable title must carry no text (no Scene 03 fallback)")
}

func TestEditorialValidateRejectsUnknownSchema(t *testing.T) {
	m := &EditorialManifest{SchemaVersion: "editorial.v99"}
	assert.Error(t, m.Validate())
}
