// Package adapters — reconciliation_test_helpers_test.go
// shared test doubles and scene builders for the asset-location
// reconciliation processor tests. The per-binding-area tests live in
// reconciliation_{clip,stock,image,voiceover,general}_test.go,
// mirroring the reconciliation_process.go / reconciliation_verify.go
// split of the processor under test.
package adapters

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// stubVerifier is a test double for script.AssetLocationVerifier.
type stubVerifier struct {
	// results maps (assetID, fileID, link) to a VerifiedLocation and optional error.
	byLink  map[string]*scriptpkg.VerifiedLocation
	byError map[string]error
}

type recordingVerifier struct {
	args   []struct{ assetID, fileID, link string }
	result *scriptpkg.VerifiedLocation
}

func (s *recordingVerifier) Verify(_ context.Context, assetID, fileID, link string) (*scriptpkg.VerifiedLocation, error) {
	s.args = append(s.args, struct{ assetID, fileID, link string }{assetID, fileID, link})
	return s.result, nil
}

func newStubVerifier() *stubVerifier {
	return &stubVerifier{
		byLink:  make(map[string]*scriptpkg.VerifiedLocation),
		byError: make(map[string]error),
	}
}

func (s *stubVerifier) stubResult(link string, result *scriptpkg.VerifiedLocation) {
	s.byLink[link] = result
}

func (s *stubVerifier) stubError(link string, err error) {
	s.byError[link] = err
}

func (s *stubVerifier) Verify(
	_ context.Context, assetID, currentFileID, currentLink string,
) (*scriptpkg.VerifiedLocation, error) {
	if err, ok := s.byError[currentLink]; ok {
		return nil, err
	}
	if loc, ok := s.byLink[currentLink]; ok {
		return loc, nil
	}
	return nil, nil
}

// helpers

func sceneWithClip(id, driveLink string) scriptpkg.SpecScene {
	return scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "narrative", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip: &scriptpkg.ClipBinding{ClipID: id, DriveLink: driveLink},
		},
	}
}

func sceneWithClipAndSub(id, driveLink, subLink, subFileID string) scriptpkg.SpecScene {
	sc := sceneWithClip(id, driveLink)
	sc.Bindings.Clip.SubtitleLink = subLink
	sc.Bindings.Clip.SubtitleFileID = subFileID
	return sc
}

func sceneWithStock(stockID, driveLink string) scriptpkg.SpecScene {
	return scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "narrative", Kind: scriptpkg.SceneStock,
		Bindings: scriptpkg.SceneBindings{
			Stock: &scriptpkg.StockBinding{AssetID: stockID, DriveLink: driveLink},
		},
	}
}

func sceneWithClipAndStock(clipID, clipLink, stockID, stockLink string) scriptpkg.SpecScene {
	sc := sceneWithClip(clipID, clipLink)
	sc.Bindings.Stock = &scriptpkg.StockBinding{AssetID: stockID, DriveLink: stockLink}
	return sc
}

func sceneWithVoiceover(link string) scriptpkg.SpecScene {
	return scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "narrative", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Voiceover: &scriptpkg.VoiceoverBinding{Link: link, Status: "completed"},
		},
	}
}

func sceneWithMedia(assetID, driveLink string) scriptpkg.SpecScene {
	return scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "narrative", Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Media: []scriptpkg.ResolvedMediaBinding{
				{Slot: "bg", AssetID: assetID, DriveLink: driveLink},
			},
		},
	}
}

func sceneWithImage(imageID, url string) scriptpkg.SpecScene {
	return scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Text: "narrative", Kind: scriptpkg.SceneImage,
		Bindings: scriptpkg.SceneBindings{
			Image: &scriptpkg.ImageBinding{ImageID: imageID, URL: url, Status: string(scriptpkg.ImageStatusGenerated)},
		},
	}
}

type reconciliationDownstreamRecorder struct {
	calls int
}

func (p *reconciliationDownstreamRecorder) Name() ProcessorName {
	return ProcessorDocument
}

func (p *reconciliationDownstreamRecorder) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *reconciliationDownstreamRecorder) Process(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, _ ProcessInput) (*PostProcessResult, error) {
	p.calls++
	return &PostProcessResult{Changed: true}, nil
}
