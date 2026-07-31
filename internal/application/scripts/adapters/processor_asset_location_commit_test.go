package adapters

import (
	"context"
	"errors"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type recordingAssetLocationCommitter struct {
	changes []scriptpkg.AssetLocationChange
	err     error
}

func (c *recordingAssetLocationCommitter) CommitAssetLocations(_ context.Context, changes []scriptpkg.AssetLocationChange) error {
	c.changes = append([]scriptpkg.AssetLocationChange(nil), changes...)
	return c.err
}

func TestAssetLocationReconciliation_CommitsSortedDeduplicatedChanges(t *testing.T) {
	oldClip := "https://drive.google.com/file/d/old-clip/view"
	newClip := "https://drive.google.com/file/d/new-clip/view"
	stockLink := "https://drive.google.com/file/d/gone-stock/view"
	voiceoverLink := "https://drive.google.com/file/d/vo/view"

	verifier := newStubVerifier()
	verifier.stubResult(oldClip, &scriptpkg.VerifiedLocation{
		AssetID: "clip-1", DriveFileID: "new-clip", DriveLink: newClip,
		State: scriptpkg.LocationStateUpdated,
	})
	verifier.stubResult(stockLink, &scriptpkg.VerifiedLocation{
		AssetID: "stock-1", DriveFileID: "gone-stock",
		State: scriptpkg.LocationStateMissing,
	})
	verifier.stubResult(voiceoverLink, &scriptpkg.VerifiedLocation{
		AssetID: "voiceover:scene-0", DriveFileID: "vo",
		DriveLink: voiceoverLink, State: scriptpkg.LocationStateVerified,
	})

	committer := &recordingAssetLocationCommitter{}
	processor := NewDurableAssetLocationReconciliationProcessor(verifier, committer)
	result, err := processor.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			{
				ID: "scene-0", Index: 0, Text: "text", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip:      &scriptpkg.ClipBinding{ClipID: "clip-1", DriveLink: oldClip},
					Stock:     &scriptpkg.StockBinding{AssetID: "stock-1", DriveLink: stockLink},
					Voiceover: &scriptpkg.VoiceoverBinding{Link: voiceoverLink},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(committer.changes) != 2 {
		t.Fatalf("committed changes = %d, want 2: %#v", len(committer.changes), committer.changes)
	}
	if committer.changes[0].AssetID != "clip-1" || committer.changes[0].DriveFileID != "new-clip" || committer.changes[0].DriveLink != newClip {
		t.Fatalf("first committed change = %#v", committer.changes[0])
	}
	if committer.changes[1].AssetID != "stock-1" || committer.changes[1].DriveFileID != "" || committer.changes[1].DriveLink != "" {
		t.Fatalf("second committed change = %#v", committer.changes[1])
	}
	if got := result.UpdatedSpecScene.Scenes[0].Bindings.Voiceover.Link; got != voiceoverLink {
		t.Fatalf("voiceover link should remain unchanged, got %q", got)
	}
}

func TestAssetLocationReconciliation_PolicyReflectsCommitter(t *testing.T) {
	verifier := newStubVerifier()
	withoutCommitter := NewAssetLocationReconciliationProcessor(verifier)
	if got := withoutCommitter.Policy(nil); got != ProcessorBestEffort {
		t.Fatalf("verification-only policy = %q, want %q", got, ProcessorBestEffort)
	}
	withCommitter := NewDurableAssetLocationReconciliationProcessor(verifier, &recordingAssetLocationCommitter{})
	if got := withCommitter.Policy(nil); got != ProcessorRequired {
		t.Fatalf("durable policy = %q, want %q", got, ProcessorRequired)
	}
}

func TestAssetLocationReconciliation_NoChangesDoesNotCommit(t *testing.T) {
	link := "https://drive.google.com/file/d/stable/view"
	verifier := newStubVerifier()
	verifier.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID: "clip-1", DriveFileID: "stable", DriveLink: link,
		State: scriptpkg.LocationStateVerified,
	})
	committer := &recordingAssetLocationCommitter{}
	processor := NewDurableAssetLocationReconciliationProcessor(verifier, committer)
	if _, err := processor.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if committer.changes != nil {
		t.Fatalf("verified scene should not commit changes: %#v", committer.changes)
	}
}

func TestAssetLocationReconciliation_InvalidLinkClearsFileIDAndSkipsSubtitle(t *testing.T) {
	clipLink := "https://drive.google.com/file/d/clip/view"
	subtitleLink := "https://drive.google.com/file/d/subtitle/view"
	verifier := newStubVerifier()
	verifier.stubResult(clipLink, &scriptpkg.VerifiedLocation{
		AssetID: "clip-1", DriveFileID: "clip", DriveLink: clipLink,
		State: scriptpkg.LocationStateVerified,
	})
	verifier.stubResult(subtitleLink, &scriptpkg.VerifiedLocation{
		AssetID: "clip-1", DriveFileID: "subtitle",
		State: scriptpkg.LocationStateMissing,
	})
	committer := &recordingAssetLocationCommitter{}
	processor := NewDurableAssetLocationReconciliationProcessor(verifier, committer)
	if _, err := processor.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClipAndSub("clip-1", clipLink, subtitleLink, "subtitle"),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(committer.changes) != 0 {
		t.Fatalf("subtitle-only invalidation must not mutate clip asset: %#v", committer.changes)
	}
}

func TestAssetLocationReconciliation_CommitFailureIsRequired(t *testing.T) {
	link := "https://drive.google.com/file/d/changed/view"
	verifier := newStubVerifier()
	verifier.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID: "clip-1", DriveFileID: "changed", DriveLink: "https://drive.google.com/file/d/canonical/view",
		State: scriptpkg.LocationStateUpdated,
	})
	committer := &recordingAssetLocationCommitter{err: errors.New("sqlite unavailable")}

	processor := NewDurableAssetLocationReconciliationProcessor(verifier, committer)
	result, err := processor.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	})
	if err == nil || !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Fatalf("expected typed required commit error, got %v", err)
	}
	if result == nil || result.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink == link {
		t.Fatalf("expected reconciled fail-closed result, got %#v", result)
	}
}
