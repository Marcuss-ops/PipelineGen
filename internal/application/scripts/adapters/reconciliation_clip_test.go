// Package adapters — reconciliation_clip_test.go
// covers the clip (and clip subtitle) binding reconciliation paths of
// the asset-location reconciliation processor: preserved, replaced,
// cleared and transport-error outcomes for clip drive_links.
package adapters

import (
	"context"
	"fmt"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// 1. Link valido conservato
func TestAssetLocationReconciliation_ValidLinkPreserved(t *testing.T) {
	link := "https://drive.google.com/file/d/abc123/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-1",
		DriveFileID: "abc123",
		DriveLink:   link,
		State:       scriptpkg.LocationStateVerified,
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != link {
		t.Fatalf("link should be preserved, got %q", got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
	if len(got.Warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", got.Warnings)
	}
}

// 2. Link vecchio sostituito con canonico
func TestAssetLocationReconciliation_StaleLinkReplaced(t *testing.T) {
	oldLink := "https://drive.google.com/file/d/OLD_ID/view"
	newLink := "https://drive.google.com/file/d/NEW_ID/view"
	r := newStubVerifier()
	r.stubResult(oldLink, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-1",
		DriveFileID: "NEW_ID",
		DriveLink:   newLink,
		State:       scriptpkg.LocationStateUpdated,
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", oldLink),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != newLink {
		t.Fatalf("link should be replaced, got %q want %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink, newLink)
	}
	if !got.Changed {
		t.Fatal("Changed should be true for replaced link")
	}
}

// 3. File cancellato → link svuotato + warning
func TestAssetLocationReconciliation_MissingLinkCleared(t *testing.T) {
	link := "https://drive.google.com/file/d/ghost123/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-1",
		DriveFileID: "ghost123",
		State:       scriptpkg.LocationStateMissing,
		ErrorCode:   "NOT_FOUND",
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != "" {
		t.Fatalf("link should be empty for missing file, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for missing link, got %d: %v", len(got.Warnings), got.Warnings)
	}
	if !strings.Contains(got.Warnings[0], "MISSING") {
		t.Fatalf("warning should mention MISSING, got %q", got.Warnings[0])
	}
}

// 4. File nel cestino → link svuotato
func TestAssetLocationReconciliation_TrashedLinkCleared(t *testing.T) {
	link := "https://drive.google.com/file/d/trash1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-1",
		DriveFileID: "trash1",
		State:       scriptpkg.LocationStateTrashed,
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != "" {
		t.Fatalf("link should be empty for trashed file, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for trashed link, got %d", len(got.Warnings))
	}
	if !strings.Contains(got.Warnings[0], "TRASHED") {
		t.Fatalf("warning should mention TRASHED, got %q", got.Warnings[0])
	}
}

// 5. Solo subtitle mancante, clip valida
func TestAssetLocationReconciliation_SubtitleMissingClipValid(t *testing.T) {
	clipLink := "https://drive.google.com/file/d/clip123/view"
	subLink := "https://drive.google.com/file/d/sub456/view"
	r := newStubVerifier()
	r.stubResult(clipLink, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-1",
		DriveFileID: "clip123",
		DriveLink:   clipLink,
		State:       scriptpkg.LocationStateVerified,
	})
	r.stubResult(subLink, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-1",
		DriveFileID: "sub456",
		State:       scriptpkg.LocationStateMissing,
		ErrorCode:   "NOT_FOUND",
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClipAndSub("clip-1", clipLink, subLink, "sub456"),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	clip := got.UpdatedSpecScene.Scenes[0].Bindings.Clip
	if clip.DriveLink != clipLink {
		t.Fatalf("clip drive_link should be preserved, got %q", clip.DriveLink)
	}
	if clip.SubtitleLink != "" {
		t.Fatalf("subtitle link should be empty, got %q", clip.SubtitleLink)
	}
	if clip.SubtitleFileID != "" {
		t.Fatalf("subtitle file ID should be empty when link is cleared, got %q", clip.SubtitleFileID)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for missing sub, got %d", len(got.Warnings))
	}
}

// 7. Permission denied → INACCESSIBLE
func TestAssetLocationReconciliation_InaccessibleLinkCleared(t *testing.T) {
	link := "https://drive.google.com/file/d/secret1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-1",
		DriveFileID: "secret1",
		State:       scriptpkg.LocationStateInaccessible,
		ErrorCode:   "PERMISSION_DENIED",
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != "" {
		t.Fatalf("inaccessible link should be cleared, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for inaccessible, got %d", len(got.Warnings))
	}
	if !strings.Contains(got.Warnings[0], "INACCESSIBLE") {
		t.Fatalf("warning should mention INACCESSIBLE, got %q", got.Warnings[0])
	}
}

// 8. Transport error → warning and cleared link (BestEffort, fail-closed publication contract)
func TestAssetLocationReconciliation_TransportErrorWarning(t *testing.T) {
	link := "https://drive.google.com/file/d/netfail/view"
	r := newStubVerifier()
	r.stubError(link, fmt.Errorf("drive: network timeout"))

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatalf("BestEffort: transport error must NOT be a hard error, got %v", err)
	}
	// Generation remains best-effort, but publication fails closed:
	// an unverified link must not reach downstream outputs.
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != "" {
		t.Fatalf("link should be cleared on transport error, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
	if !got.Changed {
		t.Fatal("clearing an unverified link must report Changed")
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for transport error, got %d: %v", len(got.Warnings), got.Warnings)
	}
	if !strings.Contains(got.Warnings[0], "transport error") || !strings.Contains(got.Warnings[0], "link cleared") {
		t.Fatalf("warning should mention transport error and link clearing, got %q", got.Warnings[0])
	}
}

// 9. Idempotenza: secondo repair con zero modifiche
func TestAssetLocationReconciliation_IdempotentSecondRepair(t *testing.T) {
	link := "https://drive.google.com/file/d/stable1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-1",
		DriveFileID: "stable1",
		DriveLink:   link,
		State:       scriptpkg.LocationStateVerified,
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	}

	// First pass: link verified, no changes.
	got1, err1 := p.Process(context.Background(), nil, input)
	if err1 != nil {
		t.Fatal(err1)
	}
	if got1.Changed {
		t.Fatal("first pass should not report changes for verified link")
	}

	// Second pass: same input, same resolver — no changes.
	got2, err2 := p.Process(context.Background(), nil, input)
	if err2 != nil {
		t.Fatal(err2)
	}
	if got2.Changed {
		t.Fatal("second pass should be idempotent (no changes)")
	}
	if len(got2.Warnings) > 0 {
		t.Fatalf("second pass should have no warnings, got %v", got2.Warnings)
	}
}

// 10. Malformed link → cleared
func TestAssetLocationReconciliation_MalformedLinkCleared(t *testing.T) {
	link := "not-a-drive-link"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:   "clip-1",
		State:     scriptpkg.LocationStateMalformed,
		ErrorCode: "MALFORMED_LINK",
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != "" {
		t.Fatalf("malformed link should be cleared, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
}

// 17. Orphan Drive file → link cleared + warning
func TestAssetLocationReconciliation_OrphanDriveFileCleared(t *testing.T) {
	link := "https://drive.google.com/file/d/orphan1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-orphan",
		DriveFileID: "orphan1",
		State:       scriptpkg.LocationStateOrphanDriveFile,
		ErrorCode:   "ASSET_NOT_IN_SQLITE",
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-orphan", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != "" {
		t.Fatalf("orphan link should be cleared, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
	if !got.Changed {
		t.Fatal("Changed should be true for cleared orphan link")
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for orphan link, got %d: %v", len(got.Warnings), got.Warnings)
	}
	if !strings.Contains(got.Warnings[0], "ORPHAN") {
		t.Fatalf("warning should mention ORPHAN, got %q", got.Warnings[0])
	}
}

// 18. Duplicate file_id → link cleared + warning
func TestAssetLocationReconciliation_DuplicateFileIDCleared(t *testing.T) {
	link := "https://drive.google.com/file/d/dup1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-dup",
		DriveFileID: "dup1",
		State:       scriptpkg.LocationStateDuplicate,
		ErrorCode:   "DUPLICATE_FILE_ID",
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-dup", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != "" {
		t.Fatalf("duplicate link should be cleared, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
	if !got.Changed {
		t.Fatal("Changed should be true for cleared duplicate link")
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for duplicate link, got %d: %v", len(got.Warnings), got.Warnings)
	}
	if !strings.Contains(got.Warnings[0], "DUPLICATE") {
		t.Fatalf("warning should mention DUPLICATE, got %q", got.Warnings[0])
	}
}

// 20. Broken asset location → link cleared + warning
func TestAssetLocationReconciliation_BrokenAssetLocationCleared(t *testing.T) {
	link := "https://drive.google.com/file/d/broken1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-broken",
		DriveFileID: "broken1",
		State:       scriptpkg.LocationStateBrokenAssetLocation,
		ErrorCode:   "DRIVE_FILE_NOT_FOUND",
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-broken", link),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != "" {
		t.Fatalf("broken location link should be cleared, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
	if !got.Changed {
		t.Fatal("Changed should be true for cleared broken location link")
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for broken location, got %d: %v", len(got.Warnings), got.Warnings)
	}
	if !strings.Contains(got.Warnings[0], "BROKEN") {
		t.Fatalf("warning should mention BROKEN, got %q", got.Warnings[0])
	}
}
