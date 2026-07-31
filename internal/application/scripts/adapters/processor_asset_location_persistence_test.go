package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestAssetLocationReconciliation_PersistedSpecSceneAndManifestContainNoUnusableLinks(t *testing.T) {
	const (
		staleLink        = "https://drive.google.com/file/d/OLD_ID/view"
		canonicalLink    = "https://drive.google.com/file/d/CANONICAL_ID/view"
		deletedLink      = "https://drive.google.com/file/d/DELETED_ID/view"
		trashedLink      = "https://drive.google.com/file/d/TRASHED_ID/view"
		inaccessibleLink = "https://drive.google.com/file/d/INACCESSIBLE_ID/view"
		unverifiedLink   = "https://drive.google.com/file/d/UNVERIFIED_ID/view"
		subtitleLink     = "https://drive.google.com/file/d/SUBTITLE_OLD_ID/view"
	)

	verifier := newStubVerifier()
	verifier.stubResult(staleLink, &scriptpkg.VerifiedLocation{
		AssetID: "clip-stale", DriveFileID: "CANONICAL_ID", DriveLink: canonicalLink,
		State: scriptpkg.LocationStateUpdated,
	})
	verifier.stubResult(deletedLink, &scriptpkg.VerifiedLocation{
		AssetID: "stock-deleted", DriveFileID: "DELETED_ID",
		State: scriptpkg.LocationStateMissing,
	})
	verifier.stubResult(trashedLink, &scriptpkg.VerifiedLocation{
		AssetID: "media-trashed", DriveFileID: "TRASHED_ID",
		State: scriptpkg.LocationStateTrashed,
	})
	verifier.stubResult(inaccessibleLink, &scriptpkg.VerifiedLocation{
		AssetID: "image-inaccessible", DriveFileID: "INACCESSIBLE_ID",
		State: scriptpkg.LocationStateInaccessible,
	})
	verifier.stubError(unverifiedLink, errors.New("Drive verification unavailable"))
	verifier.stubResult(subtitleLink, &scriptpkg.VerifiedLocation{
		AssetID: "clip-stale", DriveFileID: "SUBTITLE_OLD_ID",
		State: scriptpkg.LocationStateMissing,
	})

	// Verification-only mode deliberately remains BestEffort: it clears
	// every unverified output link, allowing persistence to serialize only
	// the fail-closed scene. Durable mode would correctly stop on the
	// transport error before persistence.
	reconciler := NewAssetLocationReconciliationProcessor(verifier)
	reconciled, err := reconciler.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{{
				ID: "scene-links", Index: 0, Text: "link audit", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip:      &scriptpkg.ClipBinding{ClipID: "clip-stale", DriveLink: staleLink, SubtitleLink: subtitleLink, SubtitleFileID: "SUBTITLE_OLD_ID"},
					Stock:     &scriptpkg.StockBinding{AssetID: "stock-deleted", DriveLink: deletedLink},
					Image:     &scriptpkg.ImageBinding{ImageID: "image-inaccessible", URL: inaccessibleLink, Status: string(scriptpkg.ImageStatusGenerated)},
					Voiceover: &scriptpkg.VoiceoverBinding{Link: unverifiedLink, Status: "completed"},
					Media:     []scriptpkg.ResolvedMediaBinding{{Slot: "background", AssetID: "media-trashed", DriveLink: trashedLink}},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("reconciliation: %v", err)
	}
	if reconciled == nil {
		t.Fatal("reconciliation returned nil result")
	}

	input := baseProcessInput()
	input.Text = "link audit"
	input.WordCount = 2
	input.SpecScene = reconciled.UpdatedSpecScene

	repo := &idemFakeRepo{}
	plan := planWithPostprocessors(basePlanForIdem(), ProcessorVoiceover, ProcessorImages)
	persistence := NewPersistenceProcessor(repo, zap.NewNop())
	if _, err := persistence.Process(context.Background(), plan, input); err != nil {
		t.Fatalf("persistence: %v", err)
	}
	if repo.lastRec == nil {
		t.Fatal("persistence did not capture a script record")
	}
	if got := repo.saveManifestCalls.Load(); got != 1 {
		t.Fatalf("SaveManifestV2 calls = %d, want 1", got)
	}
	if repo.lastManifestScriptID != 1234 {
		t.Fatalf("manifest script ID = %d, want 1234", repo.lastManifestScriptID)
	}
	if len(repo.lastManifest) == 0 {
		t.Fatal("persistence emitted an empty manifest")
	}
	var persistedManifest scriptpkg.ManifestV2
	if err := json.Unmarshal(repo.lastManifest, &persistedManifest); err != nil {
		t.Fatalf("decode persisted manifest: %v", err)
	}
	if !persistedManifest.IsCanonicalMode() {
		t.Fatal("persisted manifest is not canonical no-inline-assets mode")
	}
	for _, item := range persistedManifest.Items {
		if item.ItemRef != plan.ID {
			t.Fatalf("manifest item_ref = %q, want %q", item.ItemRef, plan.ID)
		}
	}

	var persisted scriptpkg.SpecSceneOutput
	if err := json.Unmarshal([]byte(repo.lastRec.SpecScene), &persisted); err != nil {
		t.Fatalf("decode persisted SpecScene: %v", err)
	}
	if len(persisted.Scenes) != 1 {
		t.Fatalf("persisted scenes = %d, want 1", len(persisted.Scenes))
	}
	scene := persisted.Scenes[0]
	if got := scene.Bindings.Clip.DriveLink; got != canonicalLink {
		t.Fatalf("persisted stale clip link = %q, want canonical %q", got, canonicalLink)
	}
	if got := scene.Bindings.Clip.SubtitleLink; got != "" {
		t.Fatalf("persisted missing subtitle link = %q, want empty", got)
	}
	if got := scene.Bindings.Clip.SubtitleFileID; got != "" {
		t.Fatalf("persisted missing subtitle file ID = %q, want empty", got)
	}
	if got := scene.Bindings.Stock.DriveLink; got != "" {
		t.Fatalf("persisted deleted stock link = %q, want empty", got)
	}
	if got := scene.Bindings.Image.URL; got != "" {
		t.Fatalf("persisted inaccessible image URL = %q, want empty", got)
	}
	if got := scene.Bindings.Voiceover.Link; got != "" {
		t.Fatalf("persisted unverified voiceover link = %q, want empty", got)
	}
	if got := scene.Bindings.Media[0].DriveLink; got != "" {
		t.Fatalf("persisted trashed media link = %q, want empty", got)
	}

	for _, forbidden := range []string{"OLD_ID", "SUBTITLE_OLD_ID", "DELETED_ID", "TRASHED_ID", "INACCESSIBLE_ID", "UNVERIFIED_ID"} {
		if strings.Contains(repo.lastRec.SpecScene, forbidden) {
			t.Fatalf("persisted SpecScene contains forbidden Drive token %q: %s", forbidden, repo.lastRec.SpecScene)
		}
		if strings.Contains(string(repo.lastManifest), forbidden) {
			t.Fatalf("persisted manifest contains forbidden Drive token %q: %s", forbidden, repo.lastManifest)
		}
	}
	if !strings.Contains(repo.lastRec.SpecScene, "CANONICAL_ID") {
		t.Fatalf("persisted SpecScene lost canonical replacement link: %s", repo.lastRec.SpecScene)
	}
	if strings.Contains(string(repo.lastManifest), "drive.google.com") {
		t.Fatalf("persisted manifest unexpectedly contains a Drive URL: %s", repo.lastManifest)
	}
}
