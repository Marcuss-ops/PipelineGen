package templates

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// errStubDBOffline is a package-level sentinel used by the stub renderer
// to simulate a DB-lookup failure. Shared between the stub and the
// TestRenderScene_WebViewLinkError_Propagated assertion so errors.Is
// can traverse the %w chain correctly.
var errStubDBOffline = errors.New("asset_locations: database offline")

// stubRenderer implements SceneRenderer for testing.
type stubRenderer struct {
	links map[string]string
	err   error
}

func (s *stubRenderer) WebViewLink(assetID string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if s.links != nil {
		if link, ok := s.links[assetID]; ok {
			return link, nil
		}
	}
	return "", nil
}

func makeTestAsset(id, name string, state asset.LifecycleState) *asset.Asset {
	a := &asset.Asset{
		ID:             id,
		Name:           name,
		Filename:       name + ".png",
		LifecycleState: state,
		SearchTerms:    []string{"test", "scene"},
		Metadata:       make(map[string]any),
	}
	return a
}

// ── Test 1: Happy path — finalized asset rendered ─────────────────────

func TestRenderScene_FinalizedAsset(t *testing.T) {
	a := makeTestAsset("asset-001", "Sunset Scene", asset.StateActive)
	a.SetMetadataString("scene_description", "A beautiful orange sunset over the ocean.")
	a.SetMetadataString("narrative", "The sun dips below the horizon as waves crash gently.")

	renderer := &stubRenderer{
		links: map[string]string{
			"asset-001": "https://drive.google.com/file/d/drive-abc123/view",
		},
	}

	scene, err := RenderScene([]*asset.Asset{a}, renderer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if scene.AssetID != "asset-001" {
		t.Errorf("expected AssetID='asset-001', got %q", scene.AssetID)
	}
	if scene.Title != "Sunset Scene" {
		t.Errorf("expected Title='Sunset Scene', got %q", scene.Title)
	}
	if scene.ImageURL != "https://drive.google.com/file/d/drive-abc123/view" {
		t.Errorf("expected ImageURL with drive link, got %q", scene.ImageURL)
	}

	// Description should contain both metadata fields.
	if len(scene.Description) == 0 {
		t.Error("expected non-empty Description")
	}

	// godlike/07 audit-pin: NO local filesystem paths in output.
	if containsLocalPath(scene.ImageURL) {
		t.Errorf("ImageURL contains local filesystem path: %q", scene.ImageURL)
	}
	if containsLocalPath(scene.Description) {
		t.Errorf("Description contains local filesystem path: %q", scene.Description)
	}
}

// ── Test 2: Non-finalized asset → typed error ─────────────────────────

func TestRenderScene_NonFinalizedAsset_Rejected(t *testing.T) {
	states := []asset.LifecycleState{
		asset.StateStaging,
		asset.StateProcessing,
		asset.StatePreparing,
		asset.StatePublished,
		asset.StateError,
	}

	for _, state := range states {
		a := makeTestAsset("asset-002", "Test", state)
		_, err := RenderScene([]*asset.Asset{a}, &stubRenderer{})
		if err == nil {
			t.Errorf("expected error for LifecycleState=%s, got nil", state)
		}
		if !errors.Is(err, ErrImageNotFinalized) {
			t.Errorf("expected ErrImageNotFinalized for LifecycleState=%s, got %v", state, err)
		}
	}
}

// ── Test 3: Multiple assets — first is primary, all validated ─────────

func TestRenderScene_MultipleAssets(t *testing.T) {
	a1 := makeTestAsset("asset-001", "Primary Scene", asset.StateActive)
	a1.SetMetadataString("scene_description", "Primary description.")

	a2 := makeTestAsset("asset-002", "Secondary Overlay", asset.StateActive)
	a2.SetMetadataString("narrative", "Secondary narrative text.")

	renderer := &stubRenderer{
		links: map[string]string{
			"asset-001": "https://drive.google.com/file/d/drive-primary/view",
		},
	}

	scene, err := RenderScene([]*asset.Asset{a1, a2}, renderer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Primary asset drives the scene identity.
	if scene.AssetID != "asset-001" {
		t.Errorf("expected AssetID='asset-001' (primary), got %q", scene.AssetID)
	}
	if scene.Title != "Primary Scene" {
		t.Errorf("expected Title from primary asset, got %q", scene.Title)
	}

	// Both descriptions should be included.
	if len(scene.Description) == 0 {
		t.Error("expected non-empty Description from both assets")
	}
}

// ── Test 4: Empty input → error ───────────────────────────────────────

func TestRenderScene_EmptyInput(t *testing.T) {
	_, err := RenderScene(nil, &stubRenderer{})
	if err == nil {
		t.Fatal("expected error for nil input")
	}
	_, err = RenderScene([]*asset.Asset{}, &stubRenderer{})
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

// ── Test 5: Nil asset in slice → typed error ──────────────────────────

func TestRenderScene_NilAssetInSlice(t *testing.T) {
	a := makeTestAsset("asset-001", "Test", asset.StateActive)
	_, err := RenderScene([]*asset.Asset{a, nil}, &stubRenderer{})
	if err == nil {
		t.Fatal("expected error for nil asset in slice")
	}
	if !errors.Is(err, ErrImageNotFinalized) {
		t.Errorf("expected ErrImageNotFinalized, got %v", err)
	}
}

// ── Test 6: Title precedence (scene_title metadata → Name → Filename) ─

func TestRenderScene_TitlePrecedence(t *testing.T) {
	// scene_title metadata wins.
	a := makeTestAsset("asset-001", "Fallback Name", asset.StateActive)
	a.Filename = "fallback.png"
	a.SetMetadataString("scene_title", "Custom Scene Title")

	scene, err := RenderScene([]*asset.Asset{a}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scene.Title != "Custom Scene Title" {
		t.Errorf("expected Title='Custom Scene Title' (metadata), got %q", scene.Title)
	}

	// Name wins when scene_title is absent.
	a2 := makeTestAsset("asset-002", "Named Asset", asset.StateActive)
	a2.Filename = "named_asset.png"
	scene2, err := RenderScene([]*asset.Asset{a2}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scene2.Title != "Named Asset" {
		t.Errorf("expected Title='Named Asset' (Name), got %q", scene2.Title)
	}

	// Filename wins when both scene_title and Name are empty.
	a3 := makeTestAsset("asset-003", "", asset.StateActive)
	a3.Filename = "only_filename.png"
	scene3, err := RenderScene([]*asset.Asset{a3}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scene3.Title != "only_filename.png" {
		t.Errorf("expected Title='only_filename.png' (Filename), got %q", scene3.Title)
	}
}

// ── Test 7: WebViewLink error propagation ─────────────────────────────

func TestRenderScene_WebViewLinkError_Propagated(t *testing.T) {
	a := makeTestAsset("asset-001", "Test Scene", asset.StateActive)
	renderer := &stubRenderer{
		err: errStubDBOffline,
	}

	_, err := RenderScene([]*asset.Asset{a}, renderer)
	if err == nil {
		t.Fatal("expected error from WebViewLink lookup failure")
	}

	// The error should wrap the original error, NOT the sentinel.
	if errors.Is(err, ErrImageNotFinalized) {
		t.Error("expected WebViewLink lookup error, NOT ErrImageNotFinalized")
	}

	// The original error should be reachable via errors.Is.
	if !errors.Is(err, errStubDBOffline) {
		t.Errorf("expected wrapped error to contain 'asset_locations: database offline', got: %v", err)
	}
}

// ── Test 8: RenderScenes batch ────────────────────────────────────────

func TestRenderScenes_Batch(t *testing.T) {
	a1 := makeTestAsset("asset-001", "Scene One", asset.StateActive)
	a1.SetMetadataString("scene_description", "First scene.")
	a2 := makeTestAsset("asset-002", "Scene Two", asset.StateActive)
	a2.SetMetadataString("scene_description", "Second scene.")

	renderer := &stubRenderer{
		links: map[string]string{
			"asset-001": "https://drive.google.com/file/d/drive-001/view",
			"asset-002": "https://drive.google.com/file/d/drive-002/view",
		},
	}

	scenes, err := RenderScenes([][]*asset.Asset{{a1}, {a2}}, renderer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scenes) != 2 {
		t.Fatalf("expected 2 scenes, got %d", len(scenes))
	}
	if scenes[0].AssetID != "asset-001" || scenes[1].AssetID != "asset-002" {
		t.Errorf("unexpected scene order: [%s, %s]", scenes[0].AssetID, scenes[1].AssetID)
	}
}

// ── Helper ─────────────────────────────────────────────────────────────

func containsLocalPath(s string) bool {
	// Detect local filesystem paths (Windows + Unix).
	if len(s) > 2 && s[1] == ':' && s[2] == '\\' {
		return true // C:\...
	}
	if len(s) > 0 && s[0] == '/' {
		return true // /home/...
	}
	if len(s) > 1 && s[0] == '.' && s[1] == '/' {
		return true // ./...
	}
	return false
}
