package artlist

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// markerFinalizer records which engine owned the transaction it was handed by
// probing a marker table against the caller-owned tx. It is the regression
// probe for MEDIA-SSOT P0-3: the canonical Artlist committer is PostgreSQL
// while mainDB is operational SQLite, so the finalizer tx MUST be opened on
// the media handle. Without the fix the probe reads the mainDB marker (or
// fails because the table does not exist there).
type markerFinalizer struct {
	marker string
	calls  int
}

var _ finalization.AssetFinalizerTx = (*markerFinalizer)(nil)

func (f *markerFinalizer) FinalizeAsset(
	ctx context.Context,
	tx finalization.Transaction,
	artifact finalization.PublishedArtifact,
) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	f.calls++
	sqlTx, ok := assetfinalizer.UnwrapSQLTx(tx)
	if !ok {
		return finalization.ArtifactRef{}, nil, errors.New("markerFinalizer: transaction is not *sql.Tx")
	}
	if err := sqlTx.QueryRowContext(ctx, `SELECT v FROM finalizer_marker LIMIT 1`).Scan(&f.marker); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}
	return finalization.ArtifactRef{ArtifactID: artifact.ArtifactID, AssetID: artifact.ArtifactID}, nil, nil
}

func seedFinalizerMarker(t *testing.T, db *sql.DB, value string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE finalizer_marker (v TEXT NOT NULL)`); err != nil {
		t.Fatalf("create finalizer_marker: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO finalizer_marker (v) VALUES (?)`, value); err != nil {
		t.Fatalf("seed finalizer_marker: %v", err)
	}
}

// TestStagePersistResults_OpensFinalizerTxOnMediaDB pins MEDIA-SSOT P0-3: when
// mediaDB is wired, stagePersistResults must open the asset finalizer
// transaction on it (the committer's engine) instead of mainDB.
func TestStagePersistResults_OpensFinalizerTxOnMediaDB(t *testing.T) {
	mainDB := createTestDB(t)
	defer mainDB.Close()
	mediaDB := createTestDB(t)
	defer mediaDB.Close()
	seedFinalizerMarker(t, mainDB, "main")
	seedFinalizerMarker(t, mediaDB, "media")

	fin := &markerFinalizer{}
	svc := &Service{
		cfg:            &config.Config{},
		log:            zap.NewNop(),
		mainDB:         mainDB,
		mediaDB:        mediaDB,
		assetFinalizer: fin,
	}
	orch := NewRunOrchestratorService(svc)
	resp := &RunTagResponse{
		Items: []RunTagItem{{
			ClipID:        "clip-1",
			Name:          "clip",
			Filename:      "clip.mp4",
			Status:        "processed",
			DriveFileID:   "drive-1",
			DriveLink:     "https://drive.example/x",
			LegacyFileMD5: "sha256:deadbeef",
		}},
	}

	if err := orch.stagePersistResults(context.Background(), resp); err != nil {
		t.Fatalf("stagePersistResults: %v", err)
	}
	if fin.calls != 1 {
		t.Fatalf("finalizer calls = %d, want 1", fin.calls)
	}
	if fin.marker != "media" {
		t.Fatalf("finalizer tx engine marker = %q, want %q (mediaDB must own the tx)", fin.marker, "media")
	}
	if resp.Processed != 1 {
		t.Fatalf("resp.Processed = %d, want 1", resp.Processed)
	}
	if resp.Failed != 0 {
		t.Fatalf("resp.Failed = %d, want 0", resp.Failed)
	}
}

// TestService_AssetFinalizerDB_Selection pins the engine-selection fallback:
// mediaDB wins whenever wired; legacy/test compositions without a media handle
// keep using mainDB.
func TestService_AssetFinalizerDB_Selection(t *testing.T) {
	mainDB := &sql.DB{}
	mediaDB := &sql.DB{}

	if got := (&Service{mainDB: mainDB, mediaDB: mediaDB}).assetFinalizerDB(); got != mediaDB {
		t.Fatalf("assetFinalizerDB() = %p, want mediaDB %p", got, mediaDB)
	}
	if got := (&Service{mainDB: mainDB}).assetFinalizerDB(); got != mainDB {
		t.Fatalf("assetFinalizerDB() = %p, want mainDB %p", got, mainDB)
	}
	if got := (&Service{}).assetFinalizerDB(); got != nil {
		t.Fatalf("assetFinalizerDB() = %p, want nil", got)
	}
}
