package finalizer_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	testsupport "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry/testsupport"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	appwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	finalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"

	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// fakeChrononRenderer materializes the chronon binary boundary: it writes a
// deterministic byte payload to the output path so the render→probe→manifest
// half of the flow is exercised with a real on-disk file (the C++ process is
// the only component that cannot run hermetically).
type fakeChrononRenderer struct{ bytes []byte }

func (f *fakeChrononRenderer) Render(_ context.Context, _ []byte, output string) error {
	return os.WriteFile(output, f.bytes, 0644)
}

var _ infraoverlays.Renderer = (*fakeChrononRenderer)(nil)

// fakeChrononProber stands in for the canonical rustexec probe: it returns
// contract-valid facts (DefaultOverlayContractV1) and hashes the on-disk
// bytes so the manifest's sha256/size stay byte-exact.
type fakeChrononProber struct{}

func (fakeChrononProber) ProbeOverlay(_ context.Context, path string) (capoverlay.OverlayProbeResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return capoverlay.OverlayProbeResult{}, err
	}
	sum := sha256.Sum256(b)
	return capoverlay.OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   1_000_000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    int64(len(b)),
		SHA256:       hex.EncodeToString(sum[:]),
	}, nil
}

// stubDeliveryPublisher is the Drive HTTP boundary substitute. It records the
// PublishRequest (for routing assertions) and returns a canned Drive identity.
// Every component above it — ArtifactPublisherAdapter (SHA-256 re-verify,
// kind→destination mapping, subpath) and ArtifactPreparation — is real.
type stubDeliveryPublisher struct {
	result *delivery.PublishResult
	err    error
	last   *delivery.PublishRequest
}

func (s *stubDeliveryPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	s.last = &req
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

func (s *stubDeliveryPublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "artifact-folder-847", nil
}

var _ delivery.Publisher = (*stubDeliveryPublisher)(nil)

// TestOverlayEndToEnd_PlanRenderPublishPersist is the single end-to-end test
// for the RenderingGen overlay spine:
//
//	OverlayPlan → overlay.render (HandlerSet) → ArtifactManifest
//	            → ArtifactPreparation (Drive publisher)
//	            → AssetTxFinalizer → media_assets
//
// Only the chronon binary (fakeChrononRenderer) and the Drive HTTP client
// (stubDeliveryPublisher) are stubbed; the handler, the manifest emission, the
// canonical ArtifactPublisherAdapter, ArtifactPreparation, AssetTxFinalizer,
// and SQLiteAssetCommitter are all production code running against an
// in-memory SQLite DB.
func TestOverlayEndToEnd_PlanRenderPublishPersist(t *testing.T) {
	ctx := context.Background()

	// ── Stage 1: OverlayPlan → overlay.render → ArtifactManifest ──────────
	cache, err := infraoverlays.NewCache(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	gate, err := infraoverlays.NewGPUGate(filepath.Join(t.TempDir(), "gpu.lock"))
	if err != nil {
		t.Fatal(err)
	}
	renderer := &fakeChrononRenderer{bytes: []byte("chronon-overlay-e2e")}
	h, err := appwiring.NewHandlerSet(cache, renderer, gate, &fakeChrononProber{}, "chronon-e2e")
	if err != nil {
		t.Fatal(err)
	}

	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        "plan-e2e",
		VideoID:       "video-847",
		ProjectID:     "project-e2e",
		Width:         1920,
		Height:        1080,
		FPSNum:        24,
		FPSDen:        1,
		Items: []capoverlay.OverlayItem{{
			ID:         "overlay-1",
			TemplateID: "person_default",
			StartMs:    100,
			EndMs:      1100, // 1000ms duration
			Text:       "Ada",
		}},
	}
	payload, err := json.Marshal(capoverlay.RenderRequest{Plan: plan, OverlayID: "overlay-1"})
	if err != nil {
		t.Fatal(err)
	}
	jobID := "job-e2e-overlay"
	result, err := h.Render(ctx, &job.Job{ID: jobID, Type: capoverlay.JobTypeRender, Payload: payload}, nil)
	if err != nil {
		t.Fatalf("overlay.render: %v", err)
	}
	defer os.RemoveAll(filepath.Join(os.TempDir(), "pipelinegen", "renderinggen", "workspace", "jobs", jobID))

	manifest, ok := result[job.ManifestKey].(job.ArtifactManifest)
	if !ok {
		t.Fatalf("render result missing artifact manifest (type=%T)", result[job.ManifestKey])
	}
	if len(manifest.Artifacts) != 1 {
		t.Fatalf("expected 1 manifest artifact, got %d", len(manifest.Artifacts))
	}
	a := manifest.Artifacts[0]
	meta := a.ArtifactMetadata
	if meta["source"] != "chronon" {
		t.Fatalf("manifest source = %v, want chronon", meta["source"])
	}
	if a.SHA256 == "" || a.SizeBytes <= 0 {
		t.Fatalf("manifest probe must carry sha256 + size_bytes: sha256=%q size=%d", a.SHA256, a.SizeBytes)
	}

	// ── Stage 2: manifest → VerifiedArtifact (mirrors broker staged→verified) ──
	verified := finalization.VerifiedArtifact{
		ArtifactID:       a.ID,
		Kind:             finalization.KindVideo, // overlay → youtube_clip → KindVideo
		Filename:         a.Filename,
		LocalPath:        a.Path,
		MIMEType:         a.MIMEType,
		SizeBytes:        a.SizeBytes,
		SHA256:           a.SHA256,
		SourceVersion:    1,
		Requirement:      finalization.ArtifactRequirementRequired,
		IdempotencyKey:   a.ID,
		Source:           "chronon",
		DriveSubpath:     []string{"overlay"},
		ArtifactMetadata: meta,
		// The parent video's Drive folder was already resolved by the broker
		// (video-847 → artifact-folder-847); the overlay publishes BELOW it.
		ResolvedFolderID:   "artifact-folder-847",
		RootFolderResolved: true,
	}

	// ── Stage 3: ArtifactPreparation + canonical Drive publisher ──────────
	pub := &stubDeliveryPublisher{result: &delivery.PublishResult{
		FileID:       "drive-file-overlay-e2e",
		WebViewLink:  "https://drive.google.com/file/d/drive-file-overlay-e2e/view",
		DownloadLink: "https://drive.google.com/uc?id=drive-file-overlay-e2e",
		FolderID:     "artifact-folder-847",
		FolderPath:   "/video/847/overlay",
		Action:       delivery.PublishActionCreated,
	}}
	adapter := drive.NewArtifactPublisherAdapter(pub, nil)
	prep := finalizer.NewArtifactPreparation(adapter, nil)
	published, err := prep.Prepare(ctx, verified)
	if err != nil {
		t.Fatalf("ArtifactPreparation.Prepare: %v", err)
	}

	// The canonical publisher must receive the overlay routing intact.
	if pub.last == nil {
		t.Fatal("Drive publisher was never called")
	}
	if pub.last.Destination != delivery.DestinationYouTubeClip {
		t.Fatalf("publisher destination = %q, want youtube_clip", pub.last.Destination)
	}
	if pub.last.DestinationFolderID != "artifact-folder-847" {
		t.Fatalf("publisher folder = %q, want artifact-folder-847", pub.last.DestinationFolderID)
	}
	if len(pub.last.DestinationSubpath) != 1 || pub.last.DestinationSubpath[0] != "overlay" {
		t.Fatalf("publisher subpath = %#v, want [overlay]", pub.last.DestinationSubpath)
	}
	if pub.last.ContentHash != a.SHA256 {
		t.Fatalf("publisher content hash = %q, want manifest sha256 %q", pub.last.ContentHash, a.SHA256)
	}

	// Post-publish: the metadata now carries the Drive identity.
	if published.ArtifactMetadata["drive_file_id"] != "drive-file-overlay-e2e" {
		t.Fatalf("published drive_file_id = %v", published.ArtifactMetadata["drive_file_id"])
	}
	if published.ArtifactMetadata["drive_link"] != "https://drive.google.com/file/d/drive-file-overlay-e2e/view" {
		t.Fatalf("published drive_link = %v", published.ArtifactMetadata["drive_link"])
	}

	// ── Stage 4: AssetTxFinalizer → media_assets ──────────────────────────
	db := setupTestDB(t)
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	if _, _, err := finalizer.NewAssetTxFinalizer(nil, testsupport.NewSQLiteAssetCommitter(db, outboxevents.NewRepository(db), nil)).FinalizeAsset(ctx, finalizer.WrapTx(tx), published); err != nil {
		t.Fatalf("FinalizeAsset: %v", err)
	}

	var source, fileHash, driveFileID, driveLink, folderPath string
	var durationMs int64
	if err := tx.QueryRowContext(ctx, `
		SELECT source, legacy_file_md5, drive_file_id, drive_link, folder_path, duration_ms
		FROM media_assets WHERE id = ?`, a.ID).
		Scan(&source, &fileHash, &driveFileID, &driveLink, &folderPath, &durationMs); err != nil {
		t.Fatalf("verify overlay media_assets: %v", err)
	}
	if source != "chronon" {
		t.Errorf("media_assets.source = %q, want chronon", source)
	}
	if fileHash != a.SHA256 {
		t.Errorf("media_assets.file_hash = %q, want probe sha256 %q", fileHash, a.SHA256)
	}
	if driveFileID != "drive-file-overlay-e2e" {
		t.Errorf("media_assets.drive_file_id = %q, want drive-file-overlay-e2e", driveFileID)
	}
	if driveLink != "https://drive.google.com/file/d/drive-file-overlay-e2e/view" {
		t.Errorf("media_assets.drive_link = %q", driveLink)
	}
	if folderPath != "/video/847/overlay" {
		t.Errorf("media_assets.folder_path = %q, want /video/847/overlay", folderPath)
	}
	if durationMs != 1000 {
		t.Errorf("media_assets.duration_ms = %d, want 1000 (real overlay duration, not SizeBytes/250000 fallback)", durationMs)
	}

	// asset_locations carries the sha256 + Drive identity too.
	var locKind, locExternalID, locWebView, locFileHash string
	if err := tx.QueryRowContext(ctx, `
		SELECT location_kind, external_id, web_view_link, legacy_file_md5
		FROM asset_locations WHERE asset_id = ?`, a.ID).
		Scan(&locKind, &locExternalID, &locWebView, &locFileHash); err != nil {
		t.Fatalf("verify overlay asset_locations: %v", err)
	}
	if locKind != "drive" || locExternalID != "drive-file-overlay-e2e" || locWebView == "" || locFileHash != a.SHA256 {
		t.Errorf("asset_locations incomplete: kind=%q external_id=%q web_view_link=%q file_hash=%q",
			locKind, locExternalID, locWebView, locFileHash)
	}
}

// setupTestDB is a copy of the internal-test helper from
// asset_finalizer_tx_test.go, duplicated here because this file is an
// external test package (package finalizer_test) so it can import the
// composition root without the finalizer → wiring → finalizer cycle.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_journal=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	// Create tables matching the canonical schemas (055, 105, plus media_assets).
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			download_link TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			local_path TEXT NOT NULL DEFAULT '',
			source_provider TEXT NOT NULL DEFAULT '',
			source_version TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '',
			tags_norm TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    search_text TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
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
    status TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS asset_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			version_number INTEGER NOT NULL,
			source_uri TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			mime_type TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, version_number)
		)`,
		`CREATE TABLE IF NOT EXISTS asset_locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			location_kind TEXT NOT NULL CHECK (location_kind IN ('local', 'drive', 'object_storage')),
			uri TEXT NOT NULL,
			external_id TEXT NOT NULL DEFAULT '',
			web_view_link TEXT NOT NULL DEFAULT '',
			download_url TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			is_primary INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, location_kind)
		)`,
		`CREATE TABLE IF NOT EXISTS asset_renditions (
			id TEXT PRIMARY KEY,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			location_id INTEGER NOT NULL REFERENCES asset_locations(id),
			kind TEXT NOT NULL,
			container TEXT NOT NULL DEFAULT '',
			codec TEXT NOT NULL DEFAULT '',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			fps REAL NOT NULL DEFAULT 0,
			bitrate INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT NOT NULL DEFAULT '',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, kind)
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'QUEUED',
			worker_id TEXT NOT NULL DEFAULT '',
			lease_id TEXT NOT NULL DEFAULT '',
			retry_count INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 0,
			result_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS outbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL DEFAULT '',
			aggregate_type TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '{}',
			event_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 5,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
			ON outbox_events(event_key) WHERE event_key != ''`,
		`CREATE TABLE IF NOT EXISTS job_events (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			data_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	return db
}
