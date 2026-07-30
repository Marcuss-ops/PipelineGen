// Package adapters — processor_asset_location_reconciliation_test.go
// covers the canonical reconciliation processor against a stub
// AssetLocationResolver.
package adapters

import (
	"context"
	"fmt"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// stubResolver is a test double for script.AssetLocationResolver.
type stubResolver struct {
	// results maps (assetID, fileID, link) to a VerifiedLocation and optional error.
	byLink  map[string]*scriptpkg.VerifiedLocation
	byError map[string]error
}

func newStubResolver() *stubResolver {
	return &stubResolver{
		byLink:  make(map[string]*scriptpkg.VerifiedLocation),
		byError: make(map[string]error),
	}
}

func (s *stubResolver) stubResult(link string, result *scriptpkg.VerifiedLocation) {
	s.byLink[link] = result
}

func (s *stubResolver) stubError(link string, err error) {
	s.byError[link] = err
}

func (s *stubResolver) ResolveAndVerify(
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
	r := newStubResolver()
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
	r := newStubResolver()
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
	r := newStubResolver()
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
	r := newStubResolver()
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
	r := newStubResolver()
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
	r := newStubResolver()
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
	r := newStubResolver()
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

// 8. Transport error → fail-closed (Required processor contract)
func TestAssetLocationReconciliation_TransportErrorFailClosed(t *testing.T) {
	link := "https://drive.google.com/file/d/netfail/view"
	r := newStubResolver()
	r.stubError(link, fmt.Errorf("drive: network timeout"))

	p := NewAssetLocationReconciliationProcessor(r)
	input := ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithClip("clip-1", link),
		}},
	}

	_, err := p.Process(context.Background(), nil, input)
	if err == nil {
		t.Fatal("transport error must be propagated as Go error (fail-closed)")
	}
}

// 9. Idempotenza: secondo repair con zero modifiche
func TestAssetLocationReconciliation_IdempotentSecondRepair(t *testing.T) {
	link := "https://drive.google.com/file/d/stable1/view"
	r := newStubResolver()
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
	r := newStubResolver()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:  "clip-1",
		State:    scriptpkg.LocationStateMalformed,
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
	r := newStubResolver()
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
	r := newStubResolver()
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

// 13. Nil resolver → hard error
func TestAssetLocationReconciliation_NilResolverError(t *testing.T) {
	p := &AssetLocationReconciliationProcessor{resolver: nil}
	_, err := p.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{
			sceneWithClip("c", "https://drive.google.com/file/d/x/view"),
		}},
	})
	if err == nil {
		t.Fatal("nil resolver must produce an error")
	}
}

// 14. Empty scenes → no-op
func TestAssetLocationReconciliation_EmptyScenes(t *testing.T) {
	r := newStubResolver()
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
	r := newStubResolver()
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
	r := newStubResolver()
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
