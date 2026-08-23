package wiring

import (
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestFinalAudioDocumentSharesSameAsset certifies the document's "Final
// Audio JSON" block projects the certified master's audio_asset_id verbatim.
func TestFinalAudioDocumentSharesSameAsset(t *testing.T) {
	ref := scriptgen.FinalAudioReference{
		AssetID: "final-audio-abc", Path: "/tmp/final_audio_abc.m4a", Container: "m4a",
		AudioContractVersion: capabilityaudio.AudioContractVersion,
		AudioPlanVersion:     capabilityaudio.AudioPlanVersion,
		PlanSHA256:           strings.Repeat("a", 64),
		FinalAudioSHA256:     strings.Repeat("b", 64),
		Codec:                "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
		Bitrate: 128000, DurationUS: 5_000_000, DurationMS: 5000, StartPTS: 0, SizeBytes: 1,
		FinalMix: true, CopyEligible: true,
	}

	// Document surface: the final_audio block must carry the same ID as the
	// certified master.
	model := &scriptpkg.ModelScriptOutputV1{SpecScene: scriptpkg.SpecSceneOutput{Version: 1}}
	docHTML, err := scriptgen.RenderDocument(model, scriptgen.DocumentRenderOptions{
		Language: "it", FinalAudio: &ref,
	})
	require.NoError(t, err)
	docAssetID := finalAudioAssetIDFromHTML(t, docHTML)

	// The document must carry the certified master asset ID.
	require.Equal(t, ref.AssetID, docAssetID)
}

func finalAudioAssetIDFromHTML(t *testing.T, out string) string {
	t.Helper()
	const marker = "<h2>Final Audio JSON</h2><pre><code>"
	pos := strings.Index(out, marker)
	require.NotEqual(t, -1, pos)
	pos += len(marker)
	end := strings.Index(out[pos:], "</code></pre>")
	require.NotEqual(t, -1, end)
	var block map[string]any
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(out[pos:pos+end])), &block))
	id, _ := block["audio_asset_id"].(string)
	require.NotEmpty(t, id)
	return id
}

func TestScriptGenerationDocumentRenderer_EqualsCanonicalRenderer(t *testing.T) {
	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "GLOBAL TEXT MUST NOT REPLACE SCENE TEXT",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID: "scene-0", Index: 0, Text: "CANONICAL SCENE TEXT", Kind: scriptpkg.SceneClip,
					Bindings: scriptpkg.SceneBindings{
						Clip:      &scriptpkg.ClipBinding{ClipID: "CLIP-A", DriveLink: "DRIVE-A"},
						Voiceover: &scriptpkg.VoiceoverBinding{Status: "completed", Links: map[string]string{"it": "VOICE-IT"}},
					},
				},
			},
		},
	}
	opts := scriptgen.DocumentRenderOptions{Title: "Parity", Language: "it", DefaultLanguage: "it"}
	wired, err := (scriptGenerationDocumentRenderer{}).RenderDocument(model, opts)
	require.NoError(t, err)
	direct, err := scriptgen.RenderDocument(model, scriptgen.DocumentRenderOptions{
		Title: opts.Title, Language: opts.Language, DefaultLanguage: opts.DefaultLanguage,
	})
	require.NoError(t, err)
	require.Equal(t, direct, wired)
}
