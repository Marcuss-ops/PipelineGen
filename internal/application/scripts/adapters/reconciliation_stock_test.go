// Package adapters — reconciliation_stock_test.go
// covers the stock binding reconciliation paths of the asset-location
// reconciliation processor: asset-ID fallback, canonical URL parsing,
// folder-URL rejection and clip/stock coexistence.
package adapters

import (
	"context"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestAssetLocationReconciliationStockMissingLinkUsesAssetID(t *testing.T) {
	assetID := "1TwVU-11JCggSBuHtavhKMevMZna-xr51"
	r := &recordingVerifier{result: &scriptpkg.VerifiedLocation{
		AssetID: assetID, DriveFileID: assetID,
		DriveLink: "https://drive.google.com/file/d/" + assetID + "/view",
		State:     scriptpkg.LocationStateVerified,
	}}
	got, err := NewAssetLocationReconciliationProcessor(r).Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{sceneWithStock(assetID, "")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.args) != 1 || r.args[0].fileID != assetID || r.args[0].link != "https://drive.google.com/file/d/"+assetID+"/view" {
		t.Fatalf("verifier args = %+v", r.args)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Stock.DriveLink != r.result.DriveLink {
		t.Fatalf("drive_link = %q, want %q", got.UpdatedSpecScene.Scenes[0].Bindings.Stock.DriveLink, r.result.DriveLink)
	}
}

func TestAssetLocationReconciliationStockCanonicalURLAndQuery(t *testing.T) {
	assetID := "1TwVU-11JCggSBuHtavhKMevMZna-xr51"
	link := "https://drive.google.com/file/d/" + assetID + "/view?usp=drivesdk"
	r := &recordingVerifier{result: &scriptpkg.VerifiedLocation{
		AssetID: assetID, DriveFileID: assetID, DriveLink: link, State: scriptpkg.LocationStateVerified,
	}}
	_, err := NewAssetLocationReconciliationProcessor(r).Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{sceneWithStock(assetID, link)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.args) != 1 || r.args[0].fileID != assetID || r.args[0].link != link {
		t.Fatalf("verifier args = %+v", r.args)
	}
}

func TestAssetLocationReconciliationStockRejectsFolderURL(t *testing.T) {
	assetID := "1TwVU-11JCggSBuHtavhKMevMZna-xr51"
	r := &recordingVerifier{result: &scriptpkg.VerifiedLocation{State: scriptpkg.LocationStateMalformed}}
	got, err := NewAssetLocationReconciliationProcessor(r).Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{sceneWithStock(assetID, "https://drive.google.com/drive/folders/folder-boxe")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "MALFORMED") {
		t.Fatalf("warnings = %v", got.Warnings)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Stock.DriveLink != "" {
		t.Fatal("folder URL must not remain as a file link")
	}
}

func TestAssetLocationReconciliationStockKeepsFolderID(t *testing.T) {
	scene := sceneWithStock("1TwVU-11JCggSBuHtavhKMevMZna-xr51", "")
	scene.Bindings.Stock.FolderID = "folder-boxe"
	r := &recordingVerifier{result: &scriptpkg.VerifiedLocation{
		DriveFileID: "1TwVU-11JCggSBuHtavhKMevMZna-xr51",
		DriveLink:   "https://drive.google.com/file/d/1TwVU-11JCggSBuHtavhKMevMZna-xr51/view",
		State:       scriptpkg.LocationStateVerified,
	}}
	got, err := NewAssetLocationReconciliationProcessor(r).Process(context.Background(), nil, ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{scene}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Stock.FolderID != "folder-boxe" {
		t.Fatal("folder_id was not preserved")
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
