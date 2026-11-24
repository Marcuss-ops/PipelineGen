// Package script owns the script-generation application ports that depend on
// the generate request/result contract. The root scripts package keeps aliases
// so external call sites do not change; the declarations live here, beside the
// other port packages.
package script

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type TextGenerator interface {
	GenerateSceneText(ctx context.Context, request contract.GenerateRequest) ([]contract.Scene, error)
}

type SceneTextTraceGenerator interface {
	TextGenerator
	GenerateSceneTextWithTrace(context.Context, contract.GenerateRequest) ([]contract.Scene, scriptpkg.SourceTrace, error)
}

type SceneTextStreamer interface {
	GenerateSceneTextStream(ctx context.Context, request contract.GenerateRequest, emit func(contract.Scene) error) error
}

type SceneTextTraceStreamer interface {
	SceneTextStreamer
	GenerateSceneTextStreamWithTrace(context.Context, contract.GenerateRequest, func(contract.Scene) error) (scriptpkg.SourceTrace, error)
}

type ScriptPersistenceInput struct {
	RunID   string
	Request contract.GenerateRequest
	Result  *contract.GenerateResult
}

type ScriptPersistence interface {
	Persist(context.Context, ScriptPersistenceInput) (int64, error)
}

type TranslationInput struct {
	SceneID        string
	SourceLanguage contract.Language
	TargetLanguage contract.Language
	SourceText     string
	Provider       string
	Model          string
}

type Translator interface {
	Translate(ctx context.Context, input TranslationInput) (string, error)
}

type OverlayBackgroundAsset struct {
	AssetID   string
	LocalPath string
	URL       string
	SHA256    string
	MediaType string
}

type OverlayBackgroundSource interface {
	ResolveOverlayBackground(context.Context, string) (OverlayBackgroundAsset, error)
}

type VoiceoverInput struct {
	SceneID           string
	RunID             string
	Language          contract.Language
	Text              string
	Project           string
	VoiceoverFolderID string
	TTSRate           string
	Timing            *audio.TimingRequest
	ForceSynthesis    bool
}

type VoiceoverGenerator interface {
	Generate(ctx context.Context, input VoiceoverInput) (contract.AudioReference, error)
}
