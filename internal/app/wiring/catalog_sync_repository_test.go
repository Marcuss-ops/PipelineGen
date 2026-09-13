package wiring

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	imagesregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// noopFolderProjection is the folder-projection stub used to prove the router
// flips folder reads to PostgreSQL only once a projection is attached.
type noopFolderProjection struct{}

func (noopFolderProjection) UpsertFolder(context.Context, *detail.ClipFolder) error { return nil }
func (noopFolderProjection) InsertFolderIfAbsent(context.Context, *detail.ClipFolder) error {
	return nil
}
func (noopFolderProjection) DeleteFolder(context.Context, string) error { return nil }

type fakeCatalogMediaReader struct {
	rec *pgmedia.MediaAssetRecord
	err error
	got string
}

func (f *fakeCatalogMediaReader) GetAsset(_ context.Context, assetID string) (*pgmedia.MediaAssetRecord, error) {
	f.got = assetID
	if f.err != nil {
		return nil, f.err
	}
	return f.rec, nil
}

func TestPostgresCatalogRepository_GetClip_MapsRecord(t *testing.T) {
	reader := &fakeCatalogMediaReader{rec: &pgmedia.MediaAssetRecord{
		ID:             "clip-1",
		Source:         "youtube",
		Name:           "n",
		Filename:       "n.mp4",
		MediaType:      "video",
		Category:       "training",
		DurationMS:     9000,
		LocalPath:      "/media/n.mp4",
		SHA256:         "sha256:abc",
		DriveFileID:    "drive-1",
		FolderID:       "folder-1",
		ParentFolderID: "parent-1",
		FolderPath:     "/a/b",
		LifecycleState: "ACTIVE",
		Tags:           []string{"x"},
		CreatedAt:      "2026-09-13T10:00:00Z",
		MetadataJSON:   `{"custom":"v"}`,
	}}
	repo := &postgresCatalogRepository{media: reader}

	got, err := repo.GetClip(context.Background(), "clip-1")
	if err != nil {
		t.Fatalf("GetClip: %v", err)
	}
	if got == nil {
		t.Fatal("GetClip returned nil")
	}
	if reader.got != "clip-1" || got.ID != "clip-1" || got.Source != asset.Source("youtube") {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.LegacyFileMD5() != "sha256:abc" || got.LocalPath() != "/media/n.mp4" {
		t.Fatalf("locator accessors not populated: md5=%q path=%q", got.LegacyFileMD5(), got.LocalPath())
	}
	if got.FolderID() != "folder-1" || got.ParentFolderID() != "parent-1" || got.FolderPath() != "/a/b" {
		t.Fatalf("folder accessors not populated: %q/%q/%q", got.FolderID(), got.ParentFolderID(), got.FolderPath())
	}
	if got.Metadata["custom"] != "v" {
		t.Fatalf("metadata_json not carried: %+v", got.Metadata)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at not mapped")
	}
}

func TestPostgresCatalogRepository_GetClip_NotFound(t *testing.T) {
	repo := &postgresCatalogRepository{media: &fakeCatalogMediaReader{err: pgmedia.ErrMediaAssetNotFound}}
	got, err := repo.GetClip(context.Background(), "missing")
	if err != nil {
		t.Fatalf("GetClip: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil on not found, got %+v", got)
	}
}

func TestPostgresCatalogRepository_GetIndexState(t *testing.T) {
	repo := &postgresCatalogRepository{media: &fakeCatalogMediaReader{rec: &pgmedia.MediaAssetRecord{ID: "a", IndexState: "INDEXED"}}}
	state, err := repo.GetIndexState(context.Background(), "a")
	if err != nil {
		t.Fatalf("GetIndexState: %v", err)
	}
	if state != asset.IndexState("INDEXED") {
		t.Fatalf("state = %q, want INDEXED", state)
	}

	missing := &postgresCatalogRepository{media: &fakeCatalogMediaReader{err: pgmedia.ErrMediaAssetNotFound}}
	if _, err := missing.GetIndexState(context.Background(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing index state error = %v, want sql.ErrNoRows", err)
	}
}

func TestNewCatalogSyncRepository_FolderRoutingFollowsProjection(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "router.db")+"?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	legacy := imagesregistry.NewClipsRepository(nil, zap.NewNop())
	router, _ := newCatalogSyncRepository(db, legacy)
	routed, ok := router.(*postgresCatalogRepository)
	if !ok {
		t.Fatalf("want the PostgreSQL router, got %T", router)
	}
	if routed.folders != nil {
		t.Fatal("folder reads must stay on SQLite until the projection is wired")
	}

	legacy.SetFolderProjection(noopFolderProjection{})
	router, _ = newCatalogSyncRepository(db, legacy)
	routed, ok = router.(*postgresCatalogRepository)
	if !ok {
		t.Fatalf("want the PostgreSQL router, got %T", router)
	}
	if routed.folders == nil {
		t.Fatal("folder reads must move to PostgreSQL once the projection is wired")
	}
}

func TestNewCatalogSyncRepository_LegacyFallback(t *testing.T) {
	// With no PostgreSQL media handle the router must not be used: the legacy
	// SQLite repository is returned verbatim as both ports (graceful degrade).
	legacy := imagesregistry.NewClipsRepository(nil, zap.NewNop())
	repo, indexer := newCatalogSyncRepository(nil, legacy)
	if repo != legacy || indexer != legacy {
		t.Fatalf("legacy fallback = %T/%T, want the concrete legacy repo", repo, indexer)
	}

	// A nil legacy (even typed-nil) must not fabricate a PostgreSQL router.
	repo, indexer = newCatalogSyncRepository(nil, (*imagesregistry.ClipsRepository)(nil))
	if _, ok := repo.(*postgresCatalogRepository); ok {
		t.Fatalf("nil legacy fabricated a router: %T", repo)
	}
	if _, ok := indexer.(*postgresCatalogRepository); ok {
		t.Fatalf("nil legacy fabricated a router: %T", indexer)
	}
}
