package scriptgeneration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func documentSpecSceneSHA256(model *scriptpkg.ModelScriptOutputV1) string {
	if model == nil {
		return ""
	}
	raw, err := json.Marshal(model.SpecScene)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// modelScriptOutputForDocument adapts the runner's durable Scene aggregate to
// the canonical SpecScene envelope consumed by the shared document renderer.
// It is a data adapter only: it does not format HTML and does not alter the
// GenerateResult used by the render pipeline.
func modelScriptOutputForDocument(result *GenerateResult, language Language) *scriptpkg.ModelScriptOutputV1 {
	if result == nil {
		return nil
	}

	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  make([]scriptpkg.SpecScene, 0, len(result.Scenes)),
	}
	var allText []string
	for _, scene := range result.Scenes {
		text := strings.TrimSpace(scene.Text[language])
		if text != "" {
			allText = append(allText, text)
		}

		converted := scriptpkg.SpecScene{
			ID:       scene.ID,
			Index:    scene.Index,
			Text:     text,
			Kind:     scriptpkg.SceneNarration,
			Bindings: scriptpkg.SceneBindings{},
		}
		if scene.Clip != nil || len(scene.Clips) > 0 {
			converted.Kind = scriptpkg.SceneClip
		}
		for _, clip := range scene.Clips {
			if clip == nil {
				continue
			}
			converted.Bindings.Clips = append(converted.Bindings.Clips, clipBindingFromReference(clip))
		}
		if scene.Clip != nil {
			clipBinding := clipBindingFromReference(scene.Clip)
			converted.Bindings.Clip = &clipBinding
			if len(converted.Bindings.Clips) == 0 {
				converted.Bindings.Clips = append(converted.Bindings.Clips, *converted.Bindings.Clip)
			}
		}

		voiceover := &scriptpkg.VoiceoverBinding{}
		for lang, audio := range scene.Voiceover {
			if strings.TrimSpace(audio.URL) == "" {
				continue
			}
			if voiceover.Links == nil {
				voiceover.Links = make(map[string]string)
			}
			voiceover.Links[string(lang)] = strings.TrimSpace(audio.URL)
		}
		if link := voiceover.Links[string(language)]; link != "" {
			voiceover.Link = link
			voiceover.Status = "completed"
		}
		if len(voiceover.Links) > 0 {
			converted.Bindings.Voiceover = voiceover
		}
		spec.Scenes = append(spec.Scenes, converted)
	}

	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          strings.Join(allText, "\n\n"),
		SpecScene:     spec,
	}
}

func clipBindingFromReference(clip *ClipReference) scriptpkg.ClipBinding {
	if clip == nil {
		return scriptpkg.ClipBinding{}
	}
	return scriptpkg.ClipBinding{
		ClipID:    clip.ID,
		ClipTitle: clip.Title,
		DriveLink: clip.DriveLink,
		StartMs:   clip.SourceInMS,
		EndMs:     clip.SourceOutMS,
	}
}
