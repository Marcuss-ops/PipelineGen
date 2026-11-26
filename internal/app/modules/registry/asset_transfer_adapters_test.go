package registry

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	database "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/sqltest"
)

const assetIndexTestSchema = `
CREATE TABLE IF NOT EXISTS asset_index (
	asset_id TEXT PRIMARY KEY,
	asset_type TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT '',
	source_id TEXT NOT NULL DEFAULT '',
	operation_key TEXT NOT NULL DEFAULT '',
	group_name TEXT NOT NULL DEFAULT '',
	subfolder TEXT NOT NULL DEFAULT '',
	local_path TEXT NOT NULL DEFAULT '',
	drive_link TEXT NOT NULL DEFAULT '',
	download_link TEXT NOT NULL DEFAULT '',
	file_hash TEXT NOT NULL DEFAULT '',
	content_hash TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	content_sha256 TEXT NOT NULL DEFAULT ''
);
`

// The adapter, not the capability, performs the read-modify-write, so columns
// the port does not model must survive a status transition. The retired inline
// implementation built a fresh record and could clear them.
func TestWorkerOutputStorePreservesUnmodelledColumns(t *testing.T) {
	db := database.NewTestDBWithSchema(t, assetIndexTestSchema)
	t.Cleanup(func() { _ = db.Close() })
	svc := assetindex.NewService(assetindex.NewRepository(db))
	ctx := context.Background()

	if err := svc.Upsert(ctx, &assetindex.AssetRecord{
		AssetID:      "asset-1",
		AssetType:    "clip",
		Source:       "artlist",
		SourceID:     "clip-1",
		OperationKey: "op-1",
		GroupName:    "group-a",
		Subfolder:    "sub",
		LocalPath:    "/tmp/asset-1.mp4",
		ContentHash:  "content-hash",
		Status:       "uploaded",
		Metadata:     `{"keep":"me"}`,
	}); err != nil {
		t.Fatalf("seed asset_index: %v", err)
	}

	store := NewWorkerOutputStore(svc)
	if err := store.MarkReady(ctx, "asset-1"); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}

	rec, err := svc.GetByID(ctx, "asset-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if rec == nil {
		t.Fatal("asset disappeared after MarkReady")
	}
	if rec.Status != "ready" {
		t.Fatalf("status = %q, want ready", rec.Status)
	}
	for name, got := range map[string]string{
		"operation_key": rec.OperationKey,
		"group_name":    rec.GroupName,
		"subfolder":     rec.Subfolder,
		"local_path":    rec.LocalPath,
		"content_hash":  rec.ContentHash,
		"metadata_json": rec.Metadata,
	} {
		if got == "" || got == "{}" {
			t.Fatalf("%s was cleared by the status transition", name)
		}
	}
}

func TestWorkerOutputStoreCreatesWorkerDefaults(t *testing.T) {
	db := database.NewTestDBWithSchema(t, assetIndexTestSchema)
	t.Cleanup(func() { _ = db.Close() })
	svc := assetindex.NewService(assetindex.NewRepository(db))
	ctx := context.Background()
	store := NewWorkerOutputStore(svc)

	if err := store.MarkUploaded(ctx, "fresh-asset", "/tmp/fresh.mp4"); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	rec, err := svc.GetByID(ctx, "fresh-asset")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if rec == nil {
		t.Fatal("MarkUploaded did not create the projection row")
	}
	if rec.AssetType != "worker-output" || rec.Source != "worker" || rec.SourceID != "fresh-asset" {
		t.Fatalf("worker defaults not applied: %#v", rec)
	}
	if rec.LocalPath != "/tmp/fresh.mp4" || rec.Status != "uploaded" {
		t.Fatalf("upload intent not applied: %#v", rec)
	}
}

func TestWorkerOutputStoreMissingAssetIsAbsence(t *testing.T) {
	db := database.NewTestDBWithSchema(t, assetIndexTestSchema)
	t.Cleanup(func() { _ = db.Close() })
	store := NewWorkerOutputStore(assetindex.NewService(assetindex.NewRepository(db)))

	rec, err := store.GetByID(context.Background(), "absent")
	if err != nil {
		t.Fatalf("GetByID must not fail for an absent row: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected (nil, nil), got %#v", rec)
	}
}
