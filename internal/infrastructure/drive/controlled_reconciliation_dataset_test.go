package drive_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"google.golang.org/api/googleapi"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	domainasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

//go:embed testdata/controlled_reconciliation_assets.json
var controlledReconciliationDataset []byte

type controlledReconciliationDatasetFile struct {
	Scenario string                          `json:"scenario"`
	Assets   []controlledReconciliationAsset `json:"assets"`
	Script   controlledReconciliationScript  `json:"script"`
}

type controlledReconciliationAsset struct {
	AssetID       string                  `json:"asset_id"`
	Boxer         string                  `json:"boxer"`
	SceneID       string                  `json:"scene_id"`
	DriveFileID   string                  `json:"drive_file_id"`
	DriveLink     string                  `json:"drive_link"`
	ExpectedState scriptpkg.LocationState `json:"expected_state"`
	Drive         struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		MIMEType    string   `json:"mime_type"`
		Size        int64    `json:"size"`
		WebViewLink string   `json:"web_view_link"`
		Parents     []string `json:"parents"`
		Trashed     bool     `json:"trashed"`
		ErrorStatus int      `json:"error_status"`
	} `json:"drive"`
}

type controlledReconciliationScript struct {
	ID     string                          `json:"id"`
	Title  string                          `json:"title"`
	Scenes []controlledReconciliationScene `json:"scenes"`
}

type controlledReconciliationScene struct {
	ID      string `json:"id"`
	Index   int    `json:"index"`
	Boxer   string `json:"boxer"`
	Binding struct {
		AssetID     string `json:"asset_id"`
		DriveFileID string `json:"drive_file_id"`
		DriveLink   string `json:"drive_link"`
	} `json:"binding"`
}

type controlledReader struct {
	files  map[string]*drive.FileMeta
	errors map[string]error
}

type controlledAssetStore struct {
	details map[string]*domainasset.Details
	errors  map[string]error
	calls   map[string]int
}

type controlledLocationVerifier struct {
	resolver   *drive.LocationVerifier
	linkOnly   *drive.AssetLocationResolverAdapter
	assetStore *controlledAssetStore
}

func (v controlledLocationVerifier) Verify(ctx context.Context, assetID, fileID, link string) (*scriptpkg.VerifiedLocation, error) {
	if fileID == "missing1" || drive.FileIDFromLink(link) == "missing1" {
		if details, err := v.assetStore.GetAsset(ctx, assetID); err != nil || details == nil || details.Asset == nil {
			return nil, errors.New("controlled asset store: missing asset inventory row")
		}
		return v.linkOnly.ResolveAndVerify(ctx, assetID, fileID, link)
	}
	return v.resolver.ResolveAndVerify(ctx, assetID, fileID, link)
}

func (s *controlledAssetStore) GetAsset(_ context.Context, assetID string) (*domainasset.Details, error) {
	s.calls[assetID]++
	if err, ok := s.errors[assetID]; ok {
		return nil, err
	}
	return s.details[assetID], nil
}

func (r *controlledReader) GetFileMeta(_ context.Context, fileID string) (*drive.FileMeta, error) {
	if err, ok := r.errors[fileID]; ok {
		return nil, err
	}
	return r.files[fileID], nil
}

func (r *controlledReader) DownloadFile(context.Context, string) (io.ReadCloser, string, error) {
	return nil, "", errors.New("controlled reader: DownloadFile not configured")
}

func (r *controlledReader) GetFileMD5(context.Context, string) (string, error) {
	return "", errors.New("controlled reader: GetFileMD5 not configured")
}

func (r *controlledReader) ListFiles(context.Context, string) ([]drive.DriveFileInfo, error) {
	return nil, errors.New("controlled reader: ListFiles not configured")
}

func (r *controlledReader) FindFileByName(context.Context, string, string) (drive.ExistingFileLookup, error) {
	return drive.ExistingFileLookup{}, errors.New("controlled reader: FindFileByName not configured")
}

func (r *controlledReader) FileIsNotTrashed(context.Context, string) (bool, error) {
	return false, errors.New("controlled reader: FileIsNotTrashed not configured")
}

func (r *controlledReader) FileExists(context.Context, string) (bool, error) {
	return false, errors.New("controlled reader: FileExists not configured")
}

func (r *controlledReader) SearchFiles(context.Context, string) ([]drive.DriveFileInfo, error) {
	return nil, errors.New("controlled reader: SearchFiles not configured")
}

func TestControlledReconciliationDataset(t *testing.T) {
	var dataset controlledReconciliationDatasetFile
	if err := json.Unmarshal(controlledReconciliationDataset, &dataset); err != nil {
		t.Fatalf("decode controlled reconciliation dataset: %v", err)
	}

	if dataset.Scenario != "drive_database_reconciliation" {
		t.Fatalf("scenario = %q, want drive_database_reconciliation", dataset.Scenario)
	}
	if len(dataset.Assets) != 5 {
		t.Fatalf("asset count = %d, want 5", len(dataset.Assets))
	}
	if len(dataset.Script.Scenes) != 5 {
		t.Fatalf("scene count = %d, want 5", len(dataset.Script.Scenes))
	}

	reader := &controlledReader{
		files:  make(map[string]*drive.FileMeta),
		errors: make(map[string]error),
	}
	seenAssetIDs := make(map[string]struct{}, len(dataset.Assets))
	seenDriveFileIDs := make(map[string]struct{}, len(dataset.Assets))
	assetStore := &controlledAssetStore{
		details: make(map[string]*domainasset.Details),
		errors:  make(map[string]error),
		calls:   make(map[string]int),
	}
	locationVerifier := drive.NewLocationVerifier(reader, assetStore)
	resolver := controlledLocationVerifier{
		resolver:   locationVerifier,
		linkOnly:   drive.NewAssetLocationResolverAdapter(reader),
		assetStore: assetStore,
	}
	counts := make(map[scriptpkg.LocationState]int)

	for _, asset := range dataset.Assets {
		if asset.AssetID == "" || asset.DriveFileID == "" {
			t.Fatalf("%s: asset_id and drive_file_id are required", asset.Boxer)
		}
		if _, exists := seenAssetIDs[asset.AssetID]; exists {
			t.Fatalf("duplicate asset_id %q", asset.AssetID)
		}
		if _, exists := seenDriveFileIDs[asset.DriveFileID]; exists {
			t.Fatalf("duplicate drive_file_id %q", asset.DriveFileID)
		}
		seenAssetIDs[asset.AssetID] = struct{}{}
		seenDriveFileIDs[asset.DriveFileID] = struct{}{}

		if asset.Drive.ErrorStatus != 0 {
			reader.errors[asset.DriveFileID] = &googleapi.Error{Code: asset.Drive.ErrorStatus}
		} else {
			reader.files[asset.DriveFileID] = &drive.FileMeta{
				ID:          asset.Drive.ID,
				Name:        asset.Drive.Name,
				MimeType:    asset.Drive.MIMEType,
				Size:        asset.Drive.Size,
				WebViewLink: asset.Drive.WebViewLink,
				Parents:     asset.Drive.Parents,
				Trashed:     asset.Drive.Trashed,
			}
		}
		assetStore.details[asset.AssetID] = &domainasset.Details{
			Asset: &domainasset.Asset{ID: asset.AssetID},
		}
		if details, err := assetStore.GetAsset(context.Background(), asset.AssetID); err != nil || details == nil || details.Asset == nil || details.Asset.ID != asset.AssetID {
			t.Fatalf("%s: SQLite inventory lookup failed", asset.Boxer)
		}
		if asset.ExpectedState == scriptpkg.LocationStateUpdated {
			assetStore.details[asset.AssetID].Locations = []*domainasset.Location{{
				ExternalID:   asset.DriveFileID,
				AccessURL:    asset.Drive.WebViewLink,
				IsPrimary:    true,
				LocationKind: domainasset.LocationKindDrive,
			}}
			reader.errors["oldali1"] = &googleapi.Error{Code: 404}
		}

		location, err := resolver.Verify(context.Background(), asset.AssetID, asset.DriveFileID, asset.DriveLink)
		if err != nil {
			t.Fatalf("%s: verify: %v", asset.Boxer, err)
		}
		if location == nil {
			t.Fatalf("%s: verify returned nil location", asset.Boxer)
		}
		if location.State != asset.ExpectedState {
			t.Errorf("%s: state = %s, want %s", asset.Boxer, location.State, asset.ExpectedState)
		}
		counts[location.State]++

		if asset.Drive.ErrorStatus == 0 {
			meta := reader.files[asset.DriveFileID]
			if meta.ID != asset.Drive.ID || meta.Name != asset.Drive.Name ||
				meta.MimeType != asset.Drive.MIMEType || meta.Size != asset.Drive.Size ||
				meta.WebViewLink != asset.Drive.WebViewLink || !equalStrings(meta.Parents, asset.Drive.Parents) ||
				meta.Trashed != asset.Drive.Trashed {
				t.Errorf("%s: Drive metadata does not match controlled fixture", asset.Boxer)
			}
		}

		switch asset.ExpectedState {
		case scriptpkg.LocationStateVerified:
			if location.DriveLink != asset.Drive.WebViewLink {
				t.Errorf("%s: verified link = %q, want %q", asset.Boxer, location.DriveLink, asset.Drive.WebViewLink)
			}
		case scriptpkg.LocationStateUpdated:
			if location.DriveLink != asset.Drive.WebViewLink {
				t.Errorf("%s: updated link = %q, want canonical %q", asset.Boxer, location.DriveLink, asset.Drive.WebViewLink)
			}
		case scriptpkg.LocationStateTrashed, scriptpkg.LocationStateInaccessible, scriptpkg.LocationStateMissing:
			if location.DriveLink != "" {
				t.Errorf("%s: unusable link = %q, want empty", asset.Boxer, location.DriveLink)
			}
		}
	}

	for _, state := range []scriptpkg.LocationState{
		scriptpkg.LocationStateVerified,
		scriptpkg.LocationStateUpdated,
		scriptpkg.LocationStateTrashed,
		scriptpkg.LocationStateInaccessible,
		scriptpkg.LocationStateMissing,
	} {
		if counts[state] != 1 {
			t.Errorf("state %s count = %d, want 1", state, counts[state])
		}
	}

	seenScenes := make(map[string]struct{}, len(dataset.Script.Scenes))
	processorScenes := make([]scriptpkg.SpecScene, 0, len(dataset.Script.Scenes))
	for index, scene := range dataset.Script.Scenes {
		if scene.Index != index {
			t.Errorf("scene %s index = %d, want %d", scene.ID, scene.Index, index)
		}
		if scene.ID == "" || scene.Binding.AssetID == "" || scene.Binding.DriveFileID == "" || scene.Binding.DriveLink == "" {
			t.Errorf("scene %s has incomplete binding", scene.ID)
		}
		if _, exists := seenScenes[scene.ID]; exists {
			t.Errorf("duplicate scene id %q", scene.ID)
		}
		seenScenes[scene.ID] = struct{}{}
		asset, exists := findControlledAsset(dataset.Assets, scene.Binding.AssetID)
		if !exists {
			t.Errorf("scene %s references unknown asset_id %q", scene.ID, scene.Binding.AssetID)
			continue
		}
		if scene.ID != asset.SceneID || scene.Boxer != asset.Boxer {
			t.Errorf("scene %s identity does not match asset %s", scene.ID, scene.Binding.AssetID)
		}
		if scene.Binding.DriveFileID != asset.DriveFileID || scene.Binding.DriveLink != asset.DriveLink {
			t.Errorf("scene %s binding does not match asset %s Drive reference", scene.ID, scene.Binding.AssetID)
		}
		processorScenes = append(processorScenes, scriptpkg.SpecScene{
			ID: scene.ID, Index: scene.Index, Text: scene.Boxer + " controlled scene", Kind: scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{ClipID: scene.Binding.AssetID, DriveLink: scene.Binding.DriveLink},
			},
		})
	}

	processor := adapters.NewAssetLocationReconciliationProcessor(resolver)
	result, err := processor.Process(context.Background(), nil, adapters.ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: processorScenes},
	})
	if err != nil {
		t.Fatalf("process controlled SpecScene: %v", err)
	}
	if len(result.UpdatedSpecScene.Scenes) != 5 {
		t.Fatalf("processed scene count = %d, want 5", len(result.UpdatedSpecScene.Scenes))
	}

	wantLinks := []string{
		dataset.Assets[0].Drive.WebViewLink,
		dataset.Assets[1].Drive.WebViewLink,
		"", "", "",
	}
	for index, scene := range result.UpdatedSpecScene.Scenes {
		if scene.Bindings.Clip == nil {
			t.Fatalf("processed scene %d has no clip binding", index)
		}
		if scene.Bindings.Clip.DriveLink != wantLinks[index] {
			t.Errorf("processed scene %d link = %q, want %q", index, scene.Bindings.Clip.DriveLink, wantLinks[index])
		}
	}
	if !result.Changed {
		t.Fatal("processor should report changes for the UPDATED, TRASHED, INACCESSIBLE and MISSING assets")
	}
	if len(result.Warnings) != 3 {
		t.Fatalf("processor warnings = %d, want 3: %v", len(result.Warnings), result.Warnings)
	}
	for _, asset := range dataset.Assets {
		if assetStore.calls[asset.AssetID] == 0 {
			t.Errorf("asset store was not consulted for %s", asset.AssetID)
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func findControlledAsset(assets []controlledReconciliationAsset, assetID string) (controlledReconciliationAsset, bool) {
	for _, asset := range assets {
		if asset.AssetID == assetID {
			return asset, true
		}
	}
	return controlledReconciliationAsset{}, false
}
