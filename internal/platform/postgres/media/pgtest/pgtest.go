// Package pgtest is the test-only PostgreSQL media fixture surface.
//
// It lives in its own package (mirroring internal/platform/sqlite/sqltest) so
// the production media package does not import "testing" and does not link the
// fixture constructors into the server binary. Both the live-runtime tests in
// internal/platform/postgres/media and the one-off backfill tests in
// internal/platform/postgres/media/backfill import it, so the DSN gate, the
// destructive-database guard, the truncating reset and the commit-request
// builders have exactly one owner.
//
// The fixture runs against a live PostgreSQL 18 + pgvector instance (see
// docker-compose.test-postgres.yml) and is gated behind TEST_POSTGRES_DSN so a
// default `go test ./...` stays hermetic: without the variable the tests SKIP
// (never fake availability — godlike/07).
//
// Usage:
//
//	make test-postgres
//	# or manually:
//	docker compose -f docker-compose.test-postgres.yml up -d --wait
//	TEST_POSTGRES_DSN=postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media_test?sslmode=disable \
//	  go test ./internal/platform/postgres/media/...
package pgtest

import (
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// DSNEnv is the environment variable that gates every live-database fixture.
const DSNEnv = "TEST_POSTGRES_DSN"

// RequireDSN returns the DSN from TEST_POSTGRES_DSN and reports whether the
// live-database tests should run. It marks the calling test as skipped when the
// variable is unset.
func RequireDSN(t *testing.T) (string, bool) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(DSNEnv))
	if dsn == "" {
		t.Skip(DSNEnv + " not set; skipping live PostgreSQL tests (see docker-compose.test-postgres.yml)")
	}
	return dsn, true
}

// RequireDestructive fails closed unless TEST_POSTGRES_DSN points at a
// database that is unambiguously a TEST database.
//
// Why this exists: docker-compose.test-postgres.yml documents its container as
// "the durable local media database used by the native PipelineGen service",
// so that single server hosts the operational `pipelinegen_media` database
// next to `pipelinegen_media_test`. The truncating fixtures below had no
// protection at all against a DSN aimed at production, so one
//
//	TEST_POSTGRES_DSN=.../pipelinegen_media go test ./internal/platform/postgres/media/
//
// TRUNCATEd the live media_assets, asset_locations and asset_text_tracks —
// destroying committed assets and their Drive locations — while every
// assertion still passed. `make test-postgres` always used the right database,
// but a destructive fixture that trusts its input is a loaded gun.
//
// Read-only suites are unaffected: they call RequireDSN directly and never
// reach this guard.
func RequireDestructive(t *testing.T) {
	t.Helper()
	name := DatabaseNameFromDSN(strings.TrimSpace(os.Getenv(DSNEnv)))
	if name == "" {
		t.Fatalf("%s has no resolvable database name; refusing to truncate an unidentified database (fail closed)", DSNEnv)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run a media fixture against database %q: these fixtures RESET media_assets/asset_locations/asset_text_tracks and commit real `yt_*` fixture assets through the canonical committer, so they may only run against a *_test database (make test-postgres uses pipelinegen_media_test). Contaminating the operational catalog with test rows is exactly what this guard prevents", name)
	}
}

// DatabaseNameFromDSN extracts the database component from a libpq-style DSN in
// either the URL form (postgres://user:pw@host:port/dbname?params) or the
// keyword form (host=... dbname=...).
func DatabaseNameFromDSN(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return ""
		}
		return strings.Trim(parsed.Path, "/")
	}
	for _, field := range strings.Fields(dsn) {
		key, value, ok := strings.Cut(field, "=")
		if ok && strings.EqualFold(key, "dbname") {
			return value
		}
	}
	return ""
}

// ApplyMigrations applies the canonical ORDERED media migration sequence
// through its checksum-verified ledger.
func ApplyMigrations(db *sql.DB) error {
	_, err := pgmedia.RunMediaMigrations(context.Background(), db)
	return err
}

// NewTruncatedDB opens the live test database, applies the canonical media
// migrations (001 + 002), and truncates the media surfaces so each test starts
// from a clean transactional core. The embed bridge guarantees the DDL under
// test is byte-identical to the migration files.
func NewTruncatedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn, _ := RequireDSN(t)
	RequireDestructive(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping postgres (is the test container up? make test-postgres): %v", err)
	}

	if err := ApplyMigrations(db); err != nil {
		t.Fatalf("apply media migrations: %v", err)
	}

	// Clean slate per test: truncate the transactional core in FK-safe
	// order. Derived surfaces (features/embeddings) cascade from assets.
	for _, stmt := range []string{
		`TRUNCATE asset_text_track_segments, asset_text_tracks`,
		`TRUNCATE asset_versions`,
		`TRUNCATE registry_events`,
		`TRUNCATE media_asset_sources`,
		`TRUNCATE outbox_events`,
		`TRUNCATE asset_renditions`,
		`TRUNCATE media_embedding_families`,
		`TRUNCATE asset_processing, asset_locations, media_asset_features, media_embeddings, media_assets CASCADE`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("reset %s: %v", stmt, err)
		}
	}

	t.Cleanup(func() {
		for _, stmt := range []string{
			`TRUNCATE asset_text_tracks`,
			`TRUNCATE asset_versions`,
			`TRUNCATE registry_events`,
			`TRUNCATE media_asset_sources`,
			`TRUNCATE outbox_events`,
			`TRUNCATE asset_renditions`,
			`TRUNCATE media_embedding_families`,
			`TRUNCATE asset_processing, asset_locations, media_asset_features, media_embeddings, media_assets CASCADE`,
		} {
			_, _ = db.ExecContext(ctx, stmt)
		}
	})

	return db
}

// OpenBespokeDB opens the live test database and applies the canonical media
// DDL set without truncating, for tests that need a hand-managed handle.
//
// It is GUARDED like NewTruncatedDB: a bespoke handle here still means WRITING
// fixture assets through the real committer, so it must refuse to run against
// anything but a *_test database.
func OpenBespokeDB(t *testing.T, dsn string) (*sql.DB, error) {
	t.Helper()
	RequireDestructive(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	for _, ddl := range []string{
		pgmigration.MediaSchemaDDL,
		pgmigration.MediaVectorSurfacesDDL,
		pgmigration.MediaHNSWIndexesDDL,
		pgmigration.MediaDropAssetFacesDDL,
	} {
		if _, err := db.Exec(ddl); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

// NewCommitterOnDB builds a PostgresMediaCommitter over an existing db handle
// (no second truncation).
func NewCommitterOnDB(t *testing.T, db *sql.DB) *pgmedia.PostgresMediaCommitter {
	t.Helper()
	ledger, err := pgmedia.NewRegistry(db)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return pgmedia.NewPostgresMediaCommitter(db, pgmedia.NewOutboxRepository(db), ledger, nil)
}

// TxCommitRequestFor builds a canonical CommitRequest (the
// persistence.AssetCommitter surface used inside an explicit tx) for the given
// asset ID.
func TxCommitRequestFor(assetID string) persistence.CommitRequest {
	return persistence.CommitRequest{
		AssetID:        assetID,
		Source:         "youtube",
		Name:           "Funny Moment",
		Filename:       "clip.mp4",
		MediaType:      "video",
		Category:       "celebrity",
		DurationMs:     50000,
		ContentHash:    "sha256:content",
		Description:    "A funny moment",
		SearchText:     "funny moment",
		LifecycleState: "ACTIVE",
		EmitIndexEvent: true,
		Locations: []persistence.LocationCommit{
			{Kind: "drive", ExternalID: "drive-tx-1", WebViewLink: "https://drive.google.com/file/d/drive-tx-1/view", IsPrimary: true},
		},
	}
}

// WriteSolidPNG renders a solid-colour PNG frame to path. It is the frame
// fixture shared by the tests that exercise the keyframe → colour/motion and
// keyframe → visual-embedding chains with deterministic pixels instead of a
// real encoder.
func WriteSolidPNG(path string, c color.RGBA) error {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for x := 0; x < 32; x++ {
		for y := 0; y < 32; y++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return png.Encode(f, img)
}

// TouchMediaSource writes a tiny (8-byte-header) media file into the test's
// temp dir so a fixture's os.Stat liveness gate passes while injected fakes
// drive behaviour.
func TouchMediaSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(path, []byte{0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70}, 0o644); err != nil {
		t.Fatalf("write media source: %v", err)
	}
	return path
}
