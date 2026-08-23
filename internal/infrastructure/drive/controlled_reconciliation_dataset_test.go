package drive_test

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/api/googleapi"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	AssetID               string                     `json:"asset_id"`
	Boxer                 string                     `json:"boxer"`
	SceneID               string                     `json:"scene_id"`
	DriveFileID           string                     `json:"drive_file_id"`
	DriveLink             string                     `json:"drive_link"`
	InitialLifecycleState domainasset.LifecycleState `json:"initial_lifecycle_state"`
	ExpectedState         scriptpkg.LocationState    `json:"expected_state"`
	Drive                 struct {
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

func openControlledInventoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open controlled SQLite inventory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY NOT NULL,
			drive_file_id TEXT NOT NULL UNIQUE,
			drive_link TEXT NOT NULL,
			lifecycle_state TEXT NOT NULL,
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')
	`)
	if err != nil {
		t.Fatalf("create controlled SQLite inventory: %v", err)
	}
	return db
}

func assertControlledSQLiteInventory(t *testing.T, db *sql.DB, wantRows int) {
	t.Helper()

	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets`).Scan(&rowCount); err != nil {
		t.Fatalf("count SQLite inventory rows: %v", err)
	}
	if rowCount != wantRows {
		t.Fatalf("SQLite inventory row count = %d, want %d", rowCount, wantRows)
	}

	var emptyFields int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM media_assets
		WHERE TRIM(COALESCE(id, '')) = ''
		   OR TRIM(COALESCE(drive_file_id, '')) = ''
		   OR TRIM(COALESCE(drive_link, '')) = ''
		   OR TRIM(COALESCE(lifecycle_state, '')) = ''
	`).Scan(&emptyFields); err != nil {
		t.Fatalf("check empty SQLite inventory fields: %v", err)
	}
	if emptyFields != 0 {
		t.Fatalf("SQLite inventory has %d rows with empty required fields", emptyFields)
	}

	var nonActiveRows int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM media_assets WHERE lifecycle_state <> ?`,
		domainasset.StateActive,
	).Scan(&nonActiveRows); err != nil {
		t.Fatalf("check initial SQLite lifecycle states: %v", err)
	}
	if nonActiveRows != 0 {
		t.Fatalf("SQLite inventory has %d non-ACTIVE initial rows", nonActiveRows)
	}

	for _, column := range []string{"id", "drive_file_id", "drive_link"} {
		var duplicateRows int
		query := fmt.Sprintf(
			`SELECT COUNT(*) FROM (SELECT %s FROM media_assets GROUP BY %s HAVING COUNT(*) > 1)`,
			column, column,
		)
		if err := db.QueryRow(query).Scan(&duplicateRows); err != nil {
			t.Fatalf("check duplicate SQLite inventory %s values: %v", column, err)
		}
		if duplicateRows != 0 {
			t.Fatalf("SQLite inventory has %d duplicate %s values", duplicateRows, column)
		}
	}
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

	db := openControlledInventoryDB(t)
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
		if _, err := db.Exec(
			`INSERT INTO media_assets (id, drive_file_id, drive_link, lifecycle_state) VALUES (?, ?, ?, ?)`,
			asset.AssetID, asset.DriveFileID, asset.DriveLink, asset.InitialLifecycleState,
		); err != nil {
			t.Fatalf("%s: insert initial SQLite inventory row: %v", asset.Boxer, err)
		}

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
		if asset.InitialLifecycleState == "" || !asset.InitialLifecycleState.Valid() {
			t.Fatalf("%s: initial lifecycle state %q is not canonical", asset.Boxer, asset.InitialLifecycleState)
		}
		if asset.InitialLifecycleState != domainasset.StateActive {
			t.Fatalf("%s: initial lifecycle state = %q, want %q", asset.Boxer, asset.InitialLifecycleState, domainasset.StateActive)
		}
		assetStore.details[asset.AssetID] = &domainasset.Details{
			Asset: &domainasset.Asset{
				ID:             asset.AssetID,
				LifecycleState: asset.InitialLifecycleState,
			},
		}
		if details, err := assetStore.GetAsset(context.Background(), asset.AssetID); err != nil || details == nil || details.Asset == nil || details.Asset.ID != asset.AssetID {
			t.Fatalf("%s: SQLite inventory lookup failed", asset.Boxer)
		}
		if details := assetStore.details[asset.AssetID]; details.Asset.LifecycleState != domainasset.StateActive {
			t.Fatalf("%s: persisted initial lifecycle state = %q, want %q", asset.Boxer, details.Asset.LifecycleState, domainasset.StateActive)
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

	assertControlledSQLiteInventory(t, db, len(dataset.Assets))
	if _, err := db.Exec(`INSERT INTO media_assets (id, drive_file_id, drive_link, lifecycle_state) VALUES (?, ?, ?, ?)`,
		assetIDForTest(dataset.Assets, 0), "duplicate-id-drive", "https://drive.google.com/file/d/duplicateid/view", "ACTIVE"); err == nil {
		t.Fatal("SQLite inventory accepted duplicate asset_id")
	}
	if _, err := db.Exec(`INSERT INTO media_assets (id, drive_file_id, drive_link, lifecycle_state) VALUES (?, ?, ?, ?)`,
		"duplicate-drive-id", dataset.Assets[0].DriveFileID, "https://drive.google.com/file/d/duplicatedrive/view", "ACTIVE"); err == nil {
		t.Fatal("SQLite inventory accepted duplicate drive_file_id")
	}
	if len(assetStore.details) != len(dataset.Assets) ||
		len(seenAssetIDs) != len(dataset.Assets) ||
		len(seenDriveFileIDs) != len(dataset.Assets) {
		t.Fatalf("SQLite inventory uniqueness counts: details=%d asset_id=%d drive_file_id=%d, want %d each",
			len(assetStore.details), len(seenAssetIDs), len(seenDriveFileIDs), len(dataset.Assets))
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
	sceneAssetRefs := make(map[string]int, len(dataset.Script.Scenes))
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
		sceneAssetRefs[scene.Binding.AssetID]++
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
		var dbDriveFileID, dbDriveLink, dbLifecycleState string
		if err := db.QueryRow(`SELECT drive_file_id, drive_link, lifecycle_state FROM media_assets WHERE id = ?`, scene.Binding.AssetID).
			Scan(&dbDriveFileID, &dbDriveLink, &dbLifecycleState); err != nil {
			t.Fatalf("scene %s: query SQLite inventory row: %v", scene.ID, err)
		}
		if dbDriveFileID != scene.Binding.DriveFileID || dbDriveLink != scene.Binding.DriveLink || dbLifecycleState != string(asset.InitialLifecycleState) {
			t.Errorf("scene %s does not match SQLite inventory row: got (%q, %q, %q), want (%q, %q, %q)",
				scene.ID, dbDriveFileID, dbDriveLink, dbLifecycleState,
				scene.Binding.DriveFileID, scene.Binding.DriveLink, asset.InitialLifecycleState)
		}
		processorScenes = append(processorScenes, scriptpkg.SpecScene{
			ID: scene.ID, Index: scene.Index, Text: scene.Boxer + " controlled scene", Kind: scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{ClipID: scene.Binding.AssetID, DriveLink: scene.Binding.DriveLink},
			},
		})
	}
	if len(sceneAssetRefs) != 5 {
		t.Fatalf("scene asset reference count = %d, want 5", len(sceneAssetRefs))
	}
	for assetID := range seenAssetIDs {
		if sceneAssetRefs[assetID] != 1 {
			t.Fatalf("asset %q referenced by %d scenes, want exactly 1", assetID, sceneAssetRefs[assetID])
		}
	}

	for assetID := range assetStore.calls {
		assetStore.calls[assetID] = 0
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
		calls := assetStore.calls[asset.AssetID]
		mustConsultSQLite := asset.ExpectedState == scriptpkg.LocationStateVerified ||
			asset.ExpectedState == scriptpkg.LocationStateUpdated ||
			asset.ExpectedState == scriptpkg.LocationStateMissing
		if mustConsultSQLite && calls == 0 {
			t.Errorf("asset store was not consulted for %s", asset.AssetID)
		}
		if !mustConsultSQLite && calls != 0 {
			t.Errorf("asset store was unexpectedly consulted for early-classified %s (calls=%d)", asset.AssetID, calls)
		}
	}
}

func TestControlledReconciliation_ApplicationDriveAdapter(t *testing.T) {
	var dataset controlledReconciliationDatasetFile
	if err := json.Unmarshal(controlledReconciliationDataset, &dataset); err != nil {
		t.Fatalf("decode controlled reconciliation dataset: %v", err)
	}

	reader := &controlledReader{
		files:  make(map[string]*drive.FileMeta),
		errors: make(map[string]error),
	}
	for _, asset := range dataset.Assets {
		if asset.Drive.ErrorStatus != 0 {
			reader.errors[asset.DriveFileID] = &googleapi.Error{Code: asset.Drive.ErrorStatus}
			continue
		}
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

	adapter := drive.NewAssetLocationResolverAdapter(reader)
	counts := make(map[scriptpkg.LocationState]int)
	for _, asset := range dataset.Assets {
		t.Run(asset.Boxer, func(t *testing.T) {
			location, err := adapter.ResolveAndVerify(
				context.Background(), asset.AssetID, asset.DriveFileID, asset.DriveLink,
			)
			if err != nil {
				t.Fatalf("ResolveAndVerify: %v", err)
			}
			if location == nil {
				t.Fatal("ResolveAndVerify returned nil location")
			}
			if location.AssetID != asset.AssetID {
				t.Errorf("asset_id = %q, want %q", location.AssetID, asset.AssetID)
			}
			if location.DriveFileID != asset.DriveFileID {
				t.Errorf("drive_file_id = %q, want %q", location.DriveFileID, asset.DriveFileID)
			}
			if location.State != asset.ExpectedState {
				t.Fatalf("state = %s, want %s", location.State, asset.ExpectedState)
			}
			if asset.ExpectedState == scriptpkg.LocationStateUpdated && asset.DriveLink == asset.Drive.WebViewLink {
				t.Fatal("UPDATED fixture must have a stale link distinct from the canonical webViewLink")
			}
			counts[location.State]++

			if asset.Drive.ErrorStatus != 0 {
				if location.DriveLink != "" {
					t.Errorf("unavailable Drive link = %q, want empty", location.DriveLink)
				}
				wantCode := map[int]string{403: "PERMISSION_DENIED", 404: "NOT_FOUND"}[asset.Drive.ErrorStatus]
				if location.ErrorCode != wantCode {
					t.Errorf("error code = %q, want %q", location.ErrorCode, wantCode)
				}
				return
			}

			meta := reader.files[asset.DriveFileID]
			if meta.ID != asset.DriveFileID {
				t.Errorf("Drive metadata id = %q, want requested id %q", meta.ID, asset.DriveFileID)
			}
			if meta.MimeType == "" || meta.MimeType != "video/mp4" {
				t.Errorf("Drive mimeType = %q, want non-empty video/mp4", meta.MimeType)
			}
			if meta.Size <= 0 {
				t.Errorf("Drive size = %d, want positive size", meta.Size)
			}
			if meta.Trashed != (asset.ExpectedState == scriptpkg.LocationStateTrashed) {
				t.Errorf("Drive trashed = %t, expected %t for state %s", meta.Trashed, asset.ExpectedState == scriptpkg.LocationStateTrashed, asset.ExpectedState)
			}

			switch asset.ExpectedState {
			case scriptpkg.LocationStateVerified, scriptpkg.LocationStateUpdated:
				if location.DriveLink != asset.Drive.WebViewLink {
					t.Errorf("canonical drive_link = %q, want %q", location.DriveLink, asset.Drive.WebViewLink)
				}
			case scriptpkg.LocationStateTrashed:
				if location.DriveLink != "" {
					t.Errorf("trashed Drive link = %q, want empty", location.DriveLink)
				}
			}
		})
	}

	for _, state := range []scriptpkg.LocationState{
		scriptpkg.LocationStateVerified,
		scriptpkg.LocationStateUpdated,
		scriptpkg.LocationStateTrashed,
		scriptpkg.LocationStateInaccessible,
		scriptpkg.LocationStateMissing,
	} {
		if counts[state] != 1 {
			t.Errorf("application adapter state %s count = %d, want 1", state, counts[state])
		}
	}
}

func TestControlledReconciliation_LocationVerifierRejectsInvalidMetadata(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		size     int64
	}{
		{name: "empty mime type", mimeType: "", size: 1024},
		{name: "zero size video", mimeType: "video/mp4", size: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const fileID = "invalid-metadata-file"
			reader := &controlledReader{
				files: map[string]*drive.FileMeta{
					fileID: {
						ID:          fileID,
						Name:        "invalid.mp4",
						MimeType:    test.mimeType,
						Size:        test.size,
						WebViewLink: "https://drive.google.com/file/d/" + fileID + "/view",
						Trashed:     false,
					},
				},
				errors: make(map[string]error),
			}
			verifier := drive.NewLocationVerifier(reader, nil)
			location, err := verifier.Verify(
				context.Background(), "asset-invalid-metadata", fileID,
				"https://drive.google.com/file/d/"+fileID+"/view",
			)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if location == nil {
				t.Fatal("Verify returned nil location")
			}
			if location.State != scriptpkg.LocationStateMissing {
				t.Fatalf("state = %s, want MISSING", location.State)
			}
			if location.DriveLink != "" {
				t.Fatalf("drive_link = %q, want empty for invalid metadata", location.DriveLink)
			}
		})
	}
}

func assetIDForTest(assets []controlledReconciliationAsset, index int) string {
	if index < 0 || index >= len(assets) {
		return ""
	}
	return assets[index].AssetID
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
