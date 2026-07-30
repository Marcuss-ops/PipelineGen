// Package adapters — processor_asset_location_reconciliation_test.go
// covers the canonical reconciliation processor against a stub
// AssetLocationVerifier.
package adapters

import (
	"context"
	"fmt"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// stubVerifier is a test double for script.AssetLocationVerifier.
type stubVerifier struct {
	// results maps (assetID, fileID, link) to a VerifiedLocation and optional error.
	byLink  map[string]*scriptpkg.VerifiedLocation
	byError map[string]error
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
		ID: "scene-0", Index: 0, Text: "narrative", Kind: scriptpkg.SceneClip,
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

// ── Tests ───────────────────────────────────────────────────────────

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

// 6. Stock mancante ma clip valida
func TestAssetLocationReconciliation_StockMissingClipValid(t *testing.T) {
	clipLink := "https://drive.google.com/file/d/clipOk/view"
	stockLink := "https://drive.google.com/file/d/stockGone/view"
	r := newStubVerifier()
	r.stubResult(clipLink, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-1",
		DriveFileID: "clipOk",
		DriveLink:   clipLink,
		State:       scriptpkg.LocationStateVerified,
	})
	r.stubResult(stockLink, &scriptpkg.VerifiedLocation{
		AssetID:     "stock-1",
		DriveFileID: "stockGone",
		State:       scriptpkg.LocationStateMissing,
		ErrorCode:   "NOT_FOUND",
	})

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClipAndStock("clip-1", clipLink, "stock-1", stockLink),
		}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	b := got.UpdatedSpecScene.Scenes[0].Bindings
	if b.Clip.DriveLink != clipLink {
		t.Fatalf("clip should be preserved, got %q", b.Clip.DriveLink)
	}
	if b.Stock.DriveLink != "" {
		t.Fatalf("stock link should be empty, got %q", b.Stock.DriveLink)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for missing stock, got %d", len(got.Warnings))
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

// 8. Transport error → warning (BestEffort contract)
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
	// Link preserved as-is (BestEffort degrades gracefully).
	if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != link {
		t.Fatalf("link should be preserved on transport error, got %q",
			got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning for transport error, got %d: %v", len(got.Warnings), got.Warnings)
	}
	if !strings.Contains(got.Warnings[0], "transport error") {
		t.Fatalf("warning should mention transport error, got %q", got.Warnings[0])
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

// 12. Media binding verified
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

// 13. Nil verifier → hard error
func TestAssetLocationReconciliation_NilVerifierError(t *testing.T) {
	p := &AssetLocationReconciliationProcessor{verifier: nil}
	_, err := p.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{
			sceneWithClip("c", "https://drive.google.com/file/d/x/view"),
		}},
	})
	if err == nil {
		t.Fatal("nil verifier must produce an error")
	}
}

// 14. Empty scenes → no-op
func TestAssetLocationReconciliation_EmptyScenes(t *testing.T) {
	r := newStubVerifier()
	p := NewAssetLocationReconciliationProcessor(r)
	got, err := p.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed {
		t.Fatal("empty scenes should not report changed")
	}
}

// 15. No links to verify → no-op
func TestAssetLocationReconciliation_NoLinks(t *testing.T) {
	r := newStubVerifier()
	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			{ID: "scene-0", Index: 0, Text: "text", Kind: scriptpkg.SceneNarration, Bindings: scriptpkg.SceneBindings{}},
		}},
	}
	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed {
		t.Fatal("no links should not report changed")
	}
}

// 16. 10 lingue — multi-item envelope with all links valid, zero changes.
func TestAssetLocationReconciliation_TenLanguagesNoBrokenLinks(t *testing.T) {
	languages := []string{"en", "it", "fr", "de", "es", "pt", "ja", "ko", "zh", "ar"}
	if len(languages) != 10 {
		t.Fatal("test contract: exactly 10 languages")
	}

	link := "https://drive.google.com/file/d/shared1/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:     "clip-shared",
		DriveFileID: "shared1",
		DriveLink:   link,
		State:       scriptpkg.LocationStateVerified,
	})

	p := NewAssetLocationReconciliationProcessor(r)

	for _, lang := range languages {
		input := ProcessInput{
			SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
				sceneWithClip("clip-shared", link),
			}},
			EffectiveLanguage: lang,
		}

		got, err := p.Process(context.Background(), nil, input)
		if err != nil {
			t.Fatalf("language %s: unexpected error: %v", lang, err)
		}
		if got.Changed {
			t.Fatalf("language %s: Changed should be false for verified link", lang)
		}
		if len(got.Warnings) > 0 {
			t.Fatalf("language %s: unexpected warnings: %v", lang, got.Warnings)
		}
		if got.UpdatedSpecScene.Scenes[0].Bindings.Clip.DriveLink != link {
			t.Fatalf("language %s: link should be preserved", lang)
		}
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

// 19. Document refresh: SpecScene structure preserved after reconciliation
// ensures the UpdatedSpecScene is suitable for downstream document
// rendering with the same doc_id — all binding types survive round-trip.
func TestAssetLocationReconciliation_DocumentRefreshStructurePreserved(t *testing.T) {
	clipLink := "https://drive.google.com/file/d/docClip/view"
	stockLink := "https://drive.google.com/file/d/docStock/view"
	voLink := "https://drive.google.com/file/d/docVO/view"
	mediaLink := "https://drive.google.com/file/d/docMedia/view"

	r := newStubVerifier()
	r.stubResult(clipLink, &scriptpkg.VerifiedLocation{
		AssetID: "clip-doc", DriveFileID: "docClip",
		DriveLink: clipLink, State: scriptpkg.LocationStateVerified,
	})
	r.stubResult(stockLink, &scriptpkg.VerifiedLocation{
		AssetID: "stock-doc", DriveFileID: "docStock",
		DriveLink: stockLink, State: scriptpkg.LocationStateVerified,
	})
	r.stubResult(voLink, &scriptpkg.VerifiedLocation{
		AssetID: "voiceover:scene-doc", DriveFileID: "docVO",
		DriveLink: voLink, State: scriptpkg.LocationStateVerified,
	})
	r.stubResult(mediaLink, &scriptpkg.VerifiedLocation{
		AssetID: "media-doc", DriveFileID: "docMedia",
		DriveLink: mediaLink, State: scriptpkg.LocationStateVerified,
	})

	// Build a scene with all 4 binding types populated.
	scene := scriptpkg.SpecScene{
		ID: "scene-doc", Index: 0, Text: "test scene for doc refresh",
		Kind: scriptpkg.SceneClip,
		Bindings: scriptpkg.SceneBindings{
			Clip:      &scriptpkg.ClipBinding{ClipID: "clip-doc", ClipTitle: "Doc Clip", DriveLink: clipLink},
			Stock:     &scriptpkg.StockBinding{AssetID: "stock-doc", Name: "Doc Stock", DriveLink: stockLink},
			Voiceover: &scriptpkg.VoiceoverBinding{Link: voLink, Status: "completed"},
			Media: []scriptpkg.ResolvedMediaBinding{
				{Slot: "bg", AssetID: "media-doc", DriveLink: mediaLink},
			},
		},
	}

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{scene}},
	}

	got, err := p.Process(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}

	out := got.UpdatedSpecScene.Scenes[0]

	// All links preserved for valid bindings.
	if out.Bindings.Clip == nil || out.Bindings.Clip.DriveLink != clipLink {
		t.Fatal("clip binding must be preserved for document refresh")
	}
	if out.Bindings.Stock == nil || out.Bindings.Stock.DriveLink != stockLink {
		t.Fatal("stock binding must be preserved for document refresh")
	}
	if out.Bindings.Voiceover == nil || out.Bindings.Voiceover.Link != voLink {
		t.Fatal("voiceover binding must be preserved for document refresh")
	}
	if len(out.Bindings.Media) != 1 || out.Bindings.Media[0].DriveLink != mediaLink {
		t.Fatal("media binding must be preserved for document refresh")
	}

	// Structure integrity: same scene ID, text, kind.
	if out.ID != scene.ID {
		t.Fatalf("scene ID changed: %q → %q", scene.ID, out.ID)
	}
	if out.Text != scene.Text {
		t.Fatalf("scene text changed")
	}
	if out.Kind != scene.Kind {
		t.Fatalf("scene kind changed")
	}

	if got.Changed {
		t.Fatal("Changed should be false when all links are verified — safe for document refresh")
	}
	if len(got.Warnings) > 0 {
		t.Fatalf("no warnings expected for fully valid bindings: %v", got.Warnings)
	}
}
