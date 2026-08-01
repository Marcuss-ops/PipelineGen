// Package adapters — reconciliation_voiceover_test.go
// covers the voiceover and media binding reconciliation paths of the
// asset-location reconciliation processor: verified links are
// preserved for both binding types.
package adapters

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// 11. Voiceover link verified
func TestAssetLocationReconciliation_VoiceoverLink(t *testing.T) {
	link := "https://drive.google.com/file/d/vo1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "voiceover:scene-0",
		DriveFileID: "vo1",
		DriveLink:   link,
		State:       scriptpkg.LocationStateVerified,
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithVoiceover(link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Voiceover.Link != link {
		t.Fatalf("voiceover link should be preserved, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Voiceover.Link)
	}
}

// 14. Media binding verified
func TestAssetLocationReconciliation_MediaBinding(t *testing.T) {
	link := "https://drive.google.com/file/d/media1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "asset-m1",
		DriveFileID: "media1",
		DriveLink:   link,
		State:       scriptpkg.LocationStateVerified,
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithMedia("asset-m1", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Media[0].DriveLink != link {
		t.Fatalf("media drive_link should be preserved, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Media[0].DriveLink)
	}
}
