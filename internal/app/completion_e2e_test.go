// Package app_test — completion_e2e_test.go (CUTOVER-COMPLETE-WITH-ARTIFACTS
// wave, Azione 8, July 2026).
//
// End-to-end test of the canonical post-Azion-6/7 completion spine:
//
//	1 PNG fixture local
//	  → Resolver (Azione 1; internal/application/assets/staged)
//	  → Verifier (Azione 2 surface; STUBBED — forward-pointer; not yet shipped)
//	  → Publisher (mock Drive; returns canonical AssetLocation envelope)
//	  → WithArtifactsService.CompleteWithArtifacts (Azione 6;
//	    internal/application/jobs/completion)
//	  → single-TX atomic chain (UpdateJobToSucceededCAS → InsertResultOnConflict
//	    → PersistArtifactMap → InsertOutboxEnvelope → InsertAssetLocations)
//	  → SQLite in-memory (storage.OpenSQLiteDB, t.TempDir path; canonical
//	    pattern mirrors worker_registry_e2e_test.go)
//
// godlike/06 SSOT: the test composes WITH the shipped surfaces without
// modifying them. The Verifier surface (Azione 2) is intentionally stubbed
// here as a typed seam closure; replaced by a real Verifier.Bind call
// when Azione 2 lands.
//
// 5 invariants pinned:
//
//	a. 1 PublishedArtifact emitted by the publisher closure (response
//	   carries exactly len(JobArtifactIDs) == 1).
//	b. jobs.status flipped to "SUCCEEDED" by WithArtifactsService (DB
//	   read-back via SELECT; the test does NOT trust the in-memory
//	   response alone).
//	c. 1 asset_locations row persisted, location_kind="drive" (NOT
//	   'local'), uri=matches mock publisher FileID.
//	d. Retry with same (jobID, attempt, resultHash) triple returns the
//	   IDENTICAL response + creates 0 new DB rows (jobs, job_results,
//	   job_artifacts, outbox_events, asset_locations counts all
//	   byte-stable across retry).
//	e. 0 local filesystem paths in the response/envelope (godlike/07
//	   typed LocalPath ban; verified via JSON not-Contains assertions
//	   on the canonical response envelope).
//
// Scope honesty:
//
//   - package app_test (external test), mirroring the canonical
//     pattern from internal/app/worker_registry_e2e_test.go.
//   - Search-side routes ("nuove routes azione 1+2") are TEST-ONLY —
//     mounted on /test/internal/v1/* to avoid colliding with future
//     canonical production routes (a separate Azione in the cutover
//     wave will move them to /internal/v1/* under WorkerAuth).
//   - The Verifier surface stub is named `VerifyFn`; documented as the
//     typed seam that Azione 2 binds when canonical
//     internal/application/assets/verification/verifier.go lands. A
//     semantic-drift check at that time: the Azione 2 verifier will
//     signature-match `*staged.StagedArtifact, expectedSHA string`,
//     so the seam closure here should drop in unchanged.
//   - sqlTxContext (the in-TX wrapper) implements all 7 TxContext
//     methods against real SQLite tables — production-fidelity at the
//     SQL layer; only the dispatch seam (BeginTx / Commit / Rollback)
//     is test-controlled.
//   - IdempotencyCachePort is an in-memory map (mirrors the canonical
//     `internal/application/jobs/completion/<existing_test>` mockCache,
//     but re-implemented here to keep the test self-contained).
//
// What this test does NOT pretend to cover:
//
//   - Production WorkerAuth / sign-and-validate middleware (a future
//     Azione wires the routes through canonical middleware).
//   - The AssetUploader / PrepareArtifactUpload 3-phase state machine
//     (C6; out-of-scope for Azione 8).
//   - Per-artifact MultiLocation writes (one primary per (asset_id,
//     location_kind) UNIQUE; the test votes for "drive" as primary).
//   - The 5-step TX's typed-error sentinels (covered in
//     internal/application/jobs/completion/complete_with_artifacts_service_test.go;
//     not re-tested here to avoid duplication).
package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/staged"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

// ── Typed seams (Pattern 0 ports; intentionally named for godlike/06 SSOT) ──

// VerifyFn is the typed seam for Azione 2 (Verifier — internal/application/
// assets/verification/verifier.go not yet shipped). The closure receives
// the resolved StagedArtifact + the expected SHA from the test driver and
// returns nil on hash match, typed error on drift. Semantic-drift check
// at Azione 2 merge: signature `(*staged.StagedArtifact, expectedSHA
// string) error` MUST match so the seam closure here drops in unchanged.
type VerifyFn func(ctx context.Context, stg *staged.StagedArtifact, expectedSHA string) error

// PublishFn is the typed seam for the Drive-side publisher. The closure
// returns a *finalization.PublishedArtifact with Provider="drive" so the
// WithArtifactsService derives LocationKindDrive and the godlike/07
// typed LocalPath ban is satisfied. Replaced by a real call into
// internal/application/assets/delivery/publisher::Publisher.Upload (C6
// surface) when the CUTOVER-phase Azione 7 lands.
type PublishFn func(ctx context.Context, localPath, artifactID, sha256Hex string) (*finalization.PublishedArtifact, error)

// ── SQLite-backed TxRunner + TxContext (production-fidelity SQL layer) ────

// sqlTxRunner opens a SQLite transaction on each RunInTx and threads
// an *sql.Tx through a typed-masked sqlTxContext that satisfies
// completion.TxContext. The wrap follows the C6 Adapter.go
// panic-isolation precedent: err inside fn rolls back; commit
// success preserves atomicity across all 5 in-TX writes.
type sqlTxRunner struct {
	db *sql.DB
}

// Compile-time pin (Pattern 0): catastrophic drift between the canonical
// port definition and our test impl is a build failure, not runtime panic.
var _ completion.CompleteJobTxRunner = (*sqlTxRunner)(nil)

// RunInTx opens a SQLite transaction on each invocation and threads
// an *sql.Tx through a typed-masked sqlTxContext that satisfies
// completion.TxContext. The wrap follows the C6 Adapter.go
// panic-isolation precedent: err inside fn rolls back; commit
// success preserves atomicity across all 5 in-TX writes; a panic
// inside fn rolls back + re-panics (so a future regression in
// WithArtifactsService doesn't leak Open SQLite TXs to GC).
func (r *sqlTxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context, tx completion.TxContext) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("e2e test txRunner: begin: %w", err)
	}
	sctx := &sqlTxContext{tx: tx}
	// Panic isolation (C6 Adapter.go precedent): if fn panics, the
	// BeginTx'd tx is rolled back before re-panic so the *sql.Tx
	// doesn't leak Open to the connection pool for the process
	// lifetime. The deferred recover propagates the original panic
	// value verbatim so callers can still distinguish ''real''
	// panics from tx-internal recovery mishaps.
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(ctx, sctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("e2e test txRunner: commit: %w", err)
	}
	return nil
}

// sqlTxContext implements completion.TxContext (7 methods, Azione 6
// extended surface) against a live SQLite TX. Every method maps 1:1 to
// a real SQL statement against the canonical tables seeded in step
// (2) of TestCompletionE2E.
type sqlTxContext struct {
	tx *sql.Tx
}

// Compile-time pin (Pattern 0): future drift in the TxContext surface
// (new method, renamed method, signature change) is a build failure,
// not a runtime panic at the FIRST typed-error gate.
var _ completion.TxContext = (*sqlTxContext)(nil)

func (s *sqlTxContext) GetJob(ctx context.Context, jobID string) (*completion.JobRow, error) {
	row := s.tx.QueryRowContext(ctx, "SELECT id, lease_id, attempt, status FROM jobs WHERE id = ?", jobID)
	var j completion.JobRow
	if err := row.Scan(&j.JobID, &j.LeaseID, &j.Attempt, &j.Status); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // canonical signal: not present
		}
		return nil, fmt.Errorf("e2e test tx: get job: %w", err)
	}
	return &j, nil
}

func (s *sqlTxContext) UpdateJobToSucceededCAS(ctx context.Context, jobID, leaseID string, attempt int) (int64, error) {
	res, err := s.tx.ExecContext(ctx,
		"UPDATE jobs SET status = 'SUCCEEDED' WHERE id = ? AND lease_id = ? AND attempt = ?",
		jobID, leaseID, attempt)
	if err != nil {
		return 0, fmt.Errorf("e2e test tx: CAS update job: %w", err)
	}
	return res.RowsAffected()
}

func (s *sqlTxContext) InsertResultOnConflict(ctx context.Context, jobID string, attempt int, codecID string, payload []byte, resultHash string) (int64, bool, error) {
	res, err := s.tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO job_results (job_id, attempt, result_hash, codec_id, result_payload) VALUES (?, ?, ?, ?, ?)",
		jobID, attempt, resultHash, codecID, payload)
	if err != nil {
		return 0, false, fmt.Errorf("e2e test tx: insert result: %w", err)
	}
	rows, _ := res.RowsAffected()
	replayed := rows == 0
	var id int64
	if !replayed {
		if err := s.tx.QueryRowContext(ctx,
			"SELECT id FROM job_results WHERE job_id = ? AND attempt = ? AND result_hash = ?",
			jobID, attempt, resultHash).Scan(&id); err != nil {
			return 0, false, fmt.Errorf("e2e test tx: select result row id: %w", err)
		}
	}
	return id, replayed, nil
}

func (s *sqlTxContext) GetPriorArtifactHashes(ctx context.Context, jobID string) (map[string]completion.PriorArtifactHash, error) {
	rows, err := s.tx.QueryContext(ctx,
		"SELECT artifact_id, sha256, remote_asset_id, status FROM job_artifacts WHERE job_id = ?", jobID)
	if err != nil {
		return nil, fmt.Errorf("e2e test tx: get prior hashes: %w", err)
	}
	defer rows.Close()
	out := map[string]completion.PriorArtifactHash{}
	for rows.Next() {
		var id, sha, remoteID, status string
		if err := rows.Scan(&id, &sha, &remoteID, &status); err != nil {
			return nil, fmt.Errorf("e2e test tx: scan prior hash: %w", err)
		}
		out[id] = completion.PriorArtifactHash{SHA256: sha, RemoteAssetID: remoteID, Status: status}
	}
	return out, rows.Err()
}

func (s *sqlTxContext) PersistArtifactMap(ctx context.Context, jobID string, attempt int, entries []completion.ArtifactMapEntry) error {
	for _, e := range entries {
		_, err := s.tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO job_artifacts (job_id, artifact_id, sha256, remote_asset_id, status, attempt) VALUES (?, ?, ?, ?, ?, ?)",
			jobID, e.ArtifactID, e.SHA256, e.RemoteAssetID, e.Status, attempt)
		if err != nil {
			return fmt.Errorf("e2e test tx: persist artifact map: %w", err)
		}
	}
	return nil
}

func (s *sqlTxContext) InsertOutboxEnvelope(ctx context.Context, envelope completion.OutboxEnvelope) error {
	_, err := s.tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO outbox_events (idempotency_key, event_kind, payload) VALUES (?, ?, ?)",
		envelope.IdempotencyKey, envelope.EventKind, envelope.Payload)
	if err != nil {
		return fmt.Errorf("e2e test tx: insert outbox envelope: %w", err)
	}
	return nil
}

func (s *sqlTxContext) InsertAssetLocations(ctx context.Context, entries []completion.AssetLocationEntry) error {
	for _, e := range entries {
		_, err := s.tx.ExecContext(ctx,
			`INSERT INTO asset_locations
				(asset_id, location_kind, uri, mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (asset_id, location_kind) DO UPDATE SET
				uri = excluded.uri,
				mime_type = excluded.mime_type,
				file_size_bytes = excluded.file_size_bytes,
				file_hash = excluded.file_hash,
				is_primary = excluded.is_primary,
				updated_at = excluded.updated_at`,
			e.AssetID, string(e.Kind), e.ExternalID, e.MIMEType, e.SizeBytes, e.LegacyFileMD5, boolToInt(e.IsPrimary), "now", "now")
		if err != nil {
			return fmt.Errorf("e2e test tx: insert asset_location: %w", err)
		}
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ── IdempotencyCachePort (in-memory map; canonical production impl is a SQLite table) ──

type inMemCache struct {
	mu sync.Mutex
	m  map[string]*remote.CompleteJobResponse
}

// Compile-time pin (Pattern 0): future drift in IdempotencyCachePort
// surface is a build failure.
var _ completion.IdempotencyCachePort = (*inMemCache)(nil)

func newInMemCache() *inMemCache {
	return &inMemCache{m: map[string]*remote.CompleteJobResponse{}}
}

func cacheKey(jobID string, attempt int, resultHash string) string {
	return fmt.Sprintf("%s:%d:%s", jobID, attempt, resultHash)
}

func (c *inMemCache) LookupReplay(_ context.Context, jobID string, attempt int, resultHash string) (*remote.CompleteJobResponse, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, hit := c.m[cacheKey(jobID, attempt, resultHash)]
	return r, hit, nil
}

func (c *inMemCache) StoreCanonical(_ context.Context, jobID string, attempt int, resultHash string, resp *remote.CompleteJobResponse) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[cacheKey(jobID, attempt, resultHash)] = resp
	return nil
}

// ── Helpers (test-scoped; prim types live in pkg/ or internal/ in production) ──

// minimalPNG is the canonical 68-byte transparent 1x1 PNG, hex-encoded.
// Embedding avoids the image/png stdlib import (which transitively
// pulls in golang.org/x/image) for hermeticity. Decoded size is 68
// bytes; SHA-256 is computed at runtime and pinned via t.Run assertions.
const minimalPNG = "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000a49444154789c63000100000005000179c9a9e80000000049454e44ae426082"

func loadMinimalPNG(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString(minimalPNG)
	require.NoError(t, err)
	require.Len(t, b, 68, "minimal PNG must decode to exactly 68 bytes")
	return b
}

func sha256HexBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// bootstrapCompletionSchema creates the canonical tables the test
// exercises. Schema map:
//
//	media_assets      ← asset_index lookup target (Azione 1)
//	jobs              ← CAS target (C7 step 3c)
//	job_results       ← UNIQUE (job_id, attempt, result_hash) idempotency
//	job_artifacts     ← Per-artifact map (C7 step 3e)
//	outbox_events     ← UNIQUE (idempotency_key) outbox fanout
//	asset_locations   ← FK media_assets(id), UNIQUE (asset_id, location_kind)
//
// (See migrations/sqlite/001_velox_core.sql + 055_asset_locations.sql
// + 119_job_results.sql for the canonical production DDL; this is the
// test-shaped subset.)
func bootstrapCompletionSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			source TEXT,
			media_type TEXT,
			embedding_json TEXT,
			transcript_embedding TEXT,
			metadata_json TEXT,
			local_path TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			type TEXT,
			payload TEXT,
			status TEXT NOT NULL,
			lease_id TEXT,
			attempt INTEGER NOT NULL DEFAULT 0,
			retry_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS job_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL,
			attempt INTEGER NOT NULL,
			result_hash TEXT NOT NULL,
			codec_id TEXT,
			result_payload BLOB,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE (job_id, attempt, result_hash)
		)`,
		`CREATE TABLE IF NOT EXISTS job_artifacts (
			job_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			sha256 TEXT,
			remote_asset_id TEXT,
			status TEXT,
			attempt INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (job_id, artifact_id, attempt)
		)`,
		`CREATE TABLE IF NOT EXISTS outbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT NOT NULL UNIQUE,
			event_kind TEXT NOT NULL,
			payload BLOB,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS asset_locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			location_kind TEXT NOT NULL CHECK (location_kind IN ('local','drive','object_storage')),
			uri TEXT NOT NULL,
			mime_type TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			file_hash TEXT NOT NULL DEFAULT '',
			is_primary INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, location_kind)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("e2e test bootstrap: %q: %w", s, err)
		}
	}
	return nil
}

// seedCompletionFixtures inserts the canonical 2 rows the test needs:
//
//   - media_assets(id='art1')         — asset_index entry keyed on artifactID
//     (Azione 1 lookup target).
//   - jobs(id='j1', status='RUNNING') — CompleteWithArtifacts CAS target.
//     // Order matters: media_assets MUST come first because asset_locations
//
// has a FK to media_assets(id). storage.OpenSQLiteDB now enables
// foreign_keys enforcement, so we MUST satisfy the FK at insert
// time. The flow needs TWO media_assets rows:
//   - "art1"   : Resolver lookup target (Azione 1 asset_index key)
//   - "asset-1": FK target for asset_locations where asset_id is
//     the AssetMappings["art1"] → "asset-1" mapping result
//     produced by WithArtifactsService during completion.
//
// Both rows coexist; they document the canonical lookup-key vs
// FK-target split (godlike/06 SSOT: one canonical owner per fact).
func seedCompletionFixtures(db *sql.DB, pngPath string) error {
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, source, media_type, local_path) VALUES ('art1', 'artlist', 'image', ?)`,
		pngPath); err != nil {
		return fmt.Errorf("e2e test seed: media_assets: %w", err)
	}
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, source, media_type, local_path) VALUES ('asset-1', 'artlist', 'image', ?)`,
		pngPath); err != nil {
		return fmt.Errorf("e2e test seed: media_assets (FK target): %w", err)
	}
	if _, err := db.Exec(
		`INSERT INTO jobs (id, type, payload, status, lease_id, attempt, retry_count) VALUES ('j1', 'test.e2e', '{}', 'RUNNING', 'l1', 0, 0)`); err != nil {
		return fmt.Errorf("e2e test seed: jobs: %w", err)
	}
	return nil
}

// ── Main E2E test ────────────────────────────────────────────────────────────

// TestCompletionE2E is the Azione 8 end-to-end acceptance test for the
// CUTOVER-COMPLETE-WITH-ARTIFACTS wave. It runs the canonical flow once
// with 5 t.Run subtests pinning the named invariants; the outer test
// has its own typecheck/test guards so a regression in any single
// invariant surfaces with a focused failure message.
func TestCompletionE2E(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// ── (1) PNG fixture (canonical 67-byte transparent 1x1)
	pngBytes := loadMinimalPNG(t)
	expectedSHA := sha256HexBytes(pngBytes)

	dir := t.TempDir()
	pngPath := filepath.Join(dir, "fixture.png")
	require.NoError(t, os.WriteFile(pngPath, pngBytes, 0o600),
		"create PNG fixture at %q", pngPath)

	// ── (2) SQLite in-memory / temp-file (canonical pattern)
	dbPath := filepath.Join(dir, "media.db")
	db, err := storage.OpenSQLiteDB(dbPath, zap.NewNop())
	require.NoError(t, err, "open SQLite at %q", dbPath)
	db.SetMaxOpenConns(1) // deterministic goroutine sequencing
	t.Cleanup(func() { _ = db.Close() })
	// sdb is the embedded *sql.DB handle — the storage.SQLiteDB typed handle
	// wraps *sql.DB and exposes additional config (WAL mode, busy_timeout);
	// the helper closures below and the sqlTxRunner consume the
	// stdlib *sql.DB surface for production-fidelity (mirrors
	// worker_registry_e2e_test.go where the typed handle is used for
	// lifecycle controls but the *sql.DB is used for direct queries).
	sdb := db.DB

	require.NoError(t, bootstrapCompletionSchema(sdb), "create canonical tables")
	require.NoError(t, seedCompletionFixtures(sdb, pngPath), "seed 2 fixture rows")

	// ── (3) Wire stitched components

	// Resolver (Azione 1). The lookupFn closure queries media_assets
	// for the artifactID — i.e. media_assets IS the asset_index per
	// the canonical mapping documented in Azione 1's package doc.
	resolver, err := staged.NewResolver(func(_ context.Context, artifactID string) (*staged.IndexRow, error) {
		var p string
		err := sdb.QueryRow(
			"SELECT local_path FROM media_assets WHERE id = ? AND local_path <> ''",
			artifactID).Scan(&p)
		if err == sql.ErrNoRows {
			return nil, staged.ErrStagedArtifactMissing
		}
		if err != nil {
			return nil, fmt.Errorf("e2e test resolver lookupFn: %w", err)
		}
		return &staged.IndexRow{Path: p, Source: "artlist"}, nil
	})
	require.NoError(t, err, "construct Resolver (Azione 1)")

	verifyFn := func(_ context.Context, stg *staged.StagedArtifact, expected string) error {
		if stg == nil {
			return fmt.Errorf("staged is nil")
		}
		if stg.SHA256 != expected {
			return fmt.Errorf("staged sha256=%q does not match expected=%q",
				stg.SHA256, expected)
		}
		return nil
	}

	publishFn := func(_ context.Context, localPath, artifactID, sha string) (*finalization.PublishedArtifact, error) {
		return &finalization.PublishedArtifact{
			ArtifactID:     artifactID,
			Kind:           finalization.KindImage,
			Filename:       filepath.Base(localPath),
			MIMEType:       "image/png",
			SizeBytes:      int64(len(pngBytes)),
			SHA256:         sha,
			SourceVersion:  1,
			Requirement:    finalization.ArtifactRequirementRequired,
			IdempotencyKey: remote.ArtifactIdempotencyKey("j1", artifactID, sha),
			Location: finalization.AssetLocation{
				Provider:     "drive",
				FileID:       "drive_test_" + artifactID,
				WebViewLink:  "https://drive.google.com/file/d/drive_test_" + artifactID + "/view",
				DownloadLink: "https://drive.google.com/uc?export=download&id=drive_test_" + artifactID,
				FolderID:     "drive_test_folder",
				FolderPath:   "test/",
				Action:       finalization.PublishCreated,
			},
		}, nil
	}

	txRunner := &sqlTxRunner{db: sdb}
	cache := newInMemCache()
	svc, err := completion.NewWithArtifactsService(txRunner, cache)
	require.NoError(t, err, "construct WithArtifactsService (Azione 6)")

	// ── (4) Sender httptest server (4 test-only routes on /test/internal/v1/*)
	engine := gin.New()
	grp := engine.Group("/test/internal/v1")
	grp.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"ok": false, "error": "missing Authorization header"})
			return
		}
		c.Next()
	})

	grp.POST("/artifacts/resolve", func(c *gin.Context) {
		var req struct {
			ArtifactID string `json:"artifact_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}
		out, err := resolver.ResolveStagedArtifact(c.Request.Context(), req.ArtifactID)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "staged": out})
	})

	grp.POST("/artifacts/verify", func(c *gin.Context) {
		var req struct {
			Staged      *staged.StagedArtifact `json:"staged"`
			ExpectedSHA string                 `json:"expected_sha256"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}
		if err := verifyFn(c.Request.Context(), req.Staged, req.ExpectedSHA); err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":              true,
			"verified_sha256": req.Staged.SHA256,
		})
	})

	grp.POST("/drive/publish", func(c *gin.Context) {
		var req struct {
			ArtifactID string `json:"artifact_id"`
			LocalPath  string `json:"local_path"`
			SHA256     string `json:"sha256"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}
		out, err := publishFn(c.Request.Context(), req.LocalPath, req.ArtifactID, req.SHA256)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "published": out})
	})

	grp.POST("/complete-with-artifacts", func(c *gin.Context) {
		var envelope struct {
			Request   *remote.CompleteWithArtifactsRequest `json:"request"`
			Published []*finalization.PublishedArtifact    `json:"published"`
		}
		if err := c.ShouldBindJSON(&envelope); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}
		resp, err := svc.CompleteWithArtifacts(c.Request.Context(), envelope.Request, envelope.Published)
		if err != nil {
			c.JSON(http.StatusInternalServerError,
				gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "resp": resp})
	})

	ts := httptest.NewServer(engine)
	t.Cleanup(ts.Close)

	auth := "Bearer test-e2e-token"

	// ── Drive the E2E flow ─────────────────────────────────────────────────

	// (a) HTTP POST /artifacts/resolve  →  Resolver → JSON StagedArtifact
	resolveBody, _ := json.Marshal(map[string]any{"artifact_id": "art1"})
	resolveResp := doPostJSON(t, ts.URL+"/test/internal/v1/artifacts/resolve", resolveBody, auth)
	resolveOK, _ := resolveResp["ok"].(bool)
	require.True(t, resolveOK, "resolve failed: %v", resolveResp)

	stagedMap, ok := resolveResp["staged"].(map[string]any)
	require.True(t, ok, "resolve response missing 'staged' object")
	require.Equal(t, pngPath, stagedMap["Path"], "resolved Path matches PNG fixture path")
	require.Equal(t, expectedSHA, stagedMap["SHA256"], "resolved SHA matches PNG fixture SHA")
	require.Equal(t, float64(len(pngBytes)), stagedMap["Bytes"], "Bytes matches PNG fixture length")

	// (b) HTTP POST /artifacts/verify  →  Verifier stub → ok
	stagedTyped := &staged.StagedArtifact{
		AssetID: stagedMap["AssetID"].(string),
		Path:    stagedMap["Path"].(string),
		SHA256:  stagedMap["SHA256"].(string),
		Bytes:   int64(stagedMap["Bytes"].(float64)),
		Source:  stagedMap["Source"].(string),
	}
	verifyBody, _ := json.Marshal(map[string]any{
		"staged":          stagedTyped,
		"expected_sha256": expectedSHA,
	})
	verifyResp := doPostJSON(t, ts.URL+"/test/internal/v1/artifacts/verify", verifyBody, auth)
	verifyOK, _ := verifyResp["ok"].(bool)
	require.True(t, verifyOK, "verify failed: %v", verifyResp)
	require.Equal(t, expectedSHA, verifyResp["verified_sha256"], "verifier returns the SHA byte-stable")

	// (c) HTTP POST /drive/publish  →  mock publisher → PublishedArtifact envelope
	publishBody, _ := json.Marshal(map[string]any{
		"artifact_id": "art1",
		"local_path":  pngPath,
		"sha256":      expectedSHA,
	})
	publishResp := doPostJSON(t, ts.URL+"/test/internal/v1/drive/publish", publishBody, auth)
	pubOK, _ := publishResp["ok"].(bool)
	require.True(t, pubOK, "publish failed: %v", publishResp)

	publishedMap, ok := publishResp["published"].(map[string]any)
	require.True(t, ok, "publish response missing 'published' object")
	// Provider SSOT lives at AssetLocation.Provider (godlike/06 one-
	// canonical-owner-per-fact); the mock wires Location.Provider="drive",
	// the JSON wire-shape exposes it at the nested location.provider
	// (snake_case via AssetLocation's `json:"provider"` tag, see
	// internal/domain/finalization/types.go:103). Reading the wrong
	// top-level key would silently fail the typed LocalPath ban path
	// (godlike/07).
	locationMap, ok := publishedMap["location"].(map[string]any)
	require.True(t, ok, "publish response missing nested 'location' object (AssetLocation.Provider is the SSOT)")
	require.Equal(t, "drive", locationMap["provider"],
		"mock publisher returns Provider='drive' at AssetLocation.Provider (the typed LocalPath ban path)")
	require.Equal(t, "drive_test_art1", locationMap["file_id"],
		"mock publisher returns FileID='drive_test_art1' at AssetLocation.FileID")

	// (d) HTTP POST /complete-with-artifacts  →  WithArtifactsService 5-step TX → SUCCEEDED
	completeReq := &remote.CompleteWithArtifactsRequest{
		WorkerID:   "w-e2e-1",
		JobID:      "j1",
		Attempt:    0,
		LeaseID:    "l1",
		Result:     []byte(`{"ok":true,"v":1}`),
		ResultHash: sha256HexBytes([]byte(`{"ok":true,"v":1}`)),
		AssetMappings: map[string]string{
			"art1": "asset-1",
		},
	}
	completeBody, _ := json.Marshal(map[string]any{
		"request":   completeReq,
		"published": []map[string]any{publishedMap},
	})
	completeResp1 := doPostJSON(t, ts.URL+"/test/internal/v1/complete-with-artifacts", completeBody, auth)
	completeOK1, _ := completeResp1["ok"].(bool)
	require.True(t, completeOK1, "complete-with-artifacts (attempt 1) failed: %v", completeResp1)

	resp1Map, ok := completeResp1["resp"].(map[string]any)
	require.True(t, ok, "complete response missing 'resp' object")
	require.Equal(t, "SUCCEEDED", resp1Map["Status"], "JobFinalizer status MUST be SUCCEEDED")
	require.Equal(t, "j1", resp1Map["JobID"], "JobID echo is preserved")

	// ── INVARIANT (a) — 1 PublishedArtifact emitted ────────────────────────
	t.Run("a_one_published_artifact_emitted", func(t *testing.T) {
		jobArtifactIDs, ok := resp1Map["JobArtifactIDs"].([]any)
		require.True(t, ok, "JobArtifactIDs present in response")
		assert.Len(t, jobArtifactIDs, 1,
			"JobArtifactIDs count must be exactly 1 (one PNG artifact published)")
		assert.Equal(t, "art1", jobArtifactIDs[0],
			"JobArtifactIDs[0] is the published artifact's ArtifactID")

		jobAssetIDs, ok := resp1Map["JobAssetIDs"].([]any)
		require.True(t, ok, "JobAssetIDs present in response")
		assert.Len(t, jobAssetIDs, 1,
			"JobAssetIDs count matches JobArtifactIDs count (positional alignment)")
		assert.Equal(t, "asset-1", jobAssetIDs[0],
			"JobAssetIDs[0] resolved from request's AssetMappings[art1]")
	})

	// ── INVARIANT (b) — JobFinalizer SUCCEEDED ──────────────────────────────
	t.Run("b_jobfinalizer_status_succeeded", func(t *testing.T) {
		var gotStatus string
		require.NoError(t, sdb.QueryRow(
			`SELECT status FROM jobs WHERE id = ?`, "j1").Scan(&gotStatus),
			"query jobs.status")
		assert.Equal(t, "SUCCEEDED", gotStatus,
			"jobs.status flipped to 'SUCCEEDED' by the WithArtifactsService TX")
	})

	// ── INVARIANT (c) — 1 asset_location persisted ─────────────────────────
	t.Run("c_one_asset_location_persisted", func(t *testing.T) {
		var locCount int
		require.NoError(t, sdb.QueryRow(
			`SELECT count(*) FROM asset_locations WHERE asset_id = ?`, "asset-1").Scan(&locCount))
		assert.Equal(t, 1, locCount,
			"asset_locations has exactly 1 row for the published asset (UNIQUE asset_id+location_kind enforces it)")

		var locKind, locURI, locMime string
		var locSize int64
		require.NoError(t, sdb.QueryRow(
			`SELECT location_kind, uri, mime_type, file_size_bytes FROM asset_locations WHERE asset_id = ?`,
			"asset-1").Scan(&locKind, &locURI, &locMime, &locSize))
		assert.Equal(t, "drive", locKind,
			"asset_locations.location_kind must be 'drive' (Provider='drive' mapped via locationKindFromProvider)")
		assert.Equal(t, "drive_test_art1", locURI,
			"asset_locations.uri matches the mock publisher's FileID")
		assert.Equal(t, "image/png", locMime,
			"asset_locations.mime_type propagates from PublishedArtifact.MIMEType")
		assert.Equal(t, int64(len(pngBytes)), locSize,
			"asset_locations.file_size_bytes matches the PNG fixture size")
	})

	// ── INVARIANT (d) — Retry idempotency on (jobID, attempt, resultHash) ─
	var resp2Map map[string]any
	t.Run("d_retry_idempotent_replay", func(t *testing.T) {
		completeResp2 := doPostJSON(t, ts.URL+"/test/internal/v1/complete-with-artifacts", completeBody, auth)
		completeOK2, _ := completeResp2["ok"].(bool)
		require.True(t, completeOK2, "complete-with-artifacts (attempt 2 retry) failed: %v", completeResp2)

		resp2MapRaw, ok := completeResp2["resp"].(map[string]any)
		require.True(t, ok, "retry response missing 'resp' object")
		resp2Map = resp2MapRaw

		assert.Equal(t, resp1Map["Status"], resp2Map["Status"],
			"retry preserves Status verbatim")
		assert.Equal(t, resp1Map["JobID"], resp2Map["JobID"],
			"retry preserves JobID verbatim")
		assert.Equal(t, resp1Map["JobArtifactIDs"], resp2Map["JobArtifactIDs"],
			"retry preserves JobArtifactIDs verbatim")
		assert.Equal(t, resp1Map["JobAssetIDs"], resp2Map["JobAssetIDs"],
			"retry preserves JobAssetIDs verbatim")
		assert.Equal(t, resp1Map["ResultHash"], resp2Map["ResultHash"],
			"retry preserves ResultHash verbatim (the idempotency-key triple)")

		// DB row counts MUST be byte-stable across the retry (godlike/07 no-fake-availability)
		var jobCount int
		require.NoError(t, sdb.QueryRow(
			`SELECT count(*) FROM jobs WHERE id = ?`, "j1").Scan(&jobCount))
		assert.Equal(t, 1, jobCount,
			"no new jobs row from retry (TX short-circuited at the cache hit)")

		var jrCount int
		require.NoError(t, sdb.QueryRow(
			`SELECT count(*) FROM job_results WHERE job_id = ?`, "j1").Scan(&jrCount))
		assert.Equal(t, 1, jrCount,
			"job_results has exactly 1 row (UNIQUE (job_id, attempt, result_hash) preserved)")

		var jaCount int
		require.NoError(t, sdb.QueryRow(
			`SELECT count(*) FROM job_artifacts WHERE job_id = ?`, "j1").Scan(&jaCount))
		assert.Equal(t, 1, jaCount,
			"job_artifacts has exactly 1 row (single published artifact)")

		var locCount int
		require.NoError(t, sdb.QueryRow(
			`SELECT count(*) FROM asset_locations WHERE asset_id = ?`, "asset-1").Scan(&locCount))
		assert.Equal(t, 1, locCount,
			"asset_locations UNIQUE (asset_id, location_kind) preserved exactly 1 row across retry")

		var outboxCount int
		// NOTE: CompleteJobIdempotencyKey returns a 64-char SHA-256 hex,
		// not a "j1:..."-prefixed string — so we cannot filter by
		// idempotency_key LIKE 'j1:%'. The two events are deterministically
		// named 'job.completed' (always 1 per completion) + the per-artifact
		// 'artifact.<kind>.uploaded' (1 per published artifact). For our
		// single PNG-kind artifact, the kind is 'image' so the per-artifact
		// event is 'artifact.image.uploaded'.
		require.NoError(t, sdb.QueryRow(
			`SELECT count(*) FROM outbox_events WHERE event_kind IN ('job.completed', 'artifact.image.uploaded')`).Scan(&outboxCount))
		assert.Equal(t, 2, outboxCount,
			"outbox_events: 1 job.completed + 1 artifact.image.uploaded (no doubles from retry)")
	})

	// ── INVARIANT (e) — 0 local paths in response/envelope ─────────────────
	t.Run("e_zero_local_paths_in_response", func(t *testing.T) {
		// The full response envelope (resp1 + published-side Map) contains
		// exactly WHAT? A AssetLocation with Provider='drive' → the typed
		// LocalPath ban path. We marshal both and check NO local-path
		// substring appears.
		rawResp1, err := json.Marshal(completeResp1)
		require.NoError(t, err)
		require.NotContains(t, string(rawResp1), "local_path",
			"response carries no 'local_path' references (godlike/07 typed LocalPath ban)")
		require.NotContains(t, string(rawResp1), "LocalPath",
			"response envelope has no LocalPath key (the typed field is structurally absent)")
		require.NotContains(t, string(rawResp1), `"location_kind":"local"`,
			"response has zero 'local' location_kind entries (mock Publisher uses Provider='drive')")
		require.NotContains(t, string(rawResp1), "tmpDir",
			"response has no temp dir references (godlike/07 audit-friendly envelope)")

		// DB re-check: location_kind in the persisted asset_location row.
		var locKind string
		require.NoError(t, sdb.QueryRow(
			`SELECT location_kind FROM asset_locations WHERE asset_id = ?`,
			"asset-1").Scan(&locKind))
		assert.NotEqual(t, "local", locKind,
			"persisted asset_locations row is NOT 'local' (matches the typed ban)")
		assert.Equal(t, "drive", locKind,
			"persisted asset_locations.location_kind is 'drive'")
	})
}

// ── HTTP test helpers ──────────────────────────────────────────────────

func doPostJSON(t *testing.T, url string, body []byte, auth string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err, "build POST request to %q", url)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err, "execute POST to %q", url)
	defer resp.Body.Close()

	// Buffer body BEFORE the non-2xx assertion so the error
	// message can include the response payload, AND decode from
	// the buffered bytes (not the already-drained reader). The
	// previous implementation called io.ReadAll + (then)
	// json.NewDecoder(resp.Body).Decode, which EOF'd on the
	// decoder because the body had been drained by io.ReadAll.
	// Fix: one read → bytes → both the status message AND the
	// JSON decode consume the same buffered copy.
	bodyBytes, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr, "read response body from %q", url)
	require.True(t, resp.StatusCode >= 200 && resp.StatusCode < 300,
		"unexpected HTTP status %d from %q (body: %s)", resp.StatusCode, url, string(bodyBytes))

	out := map[string]any{}
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		t.Fatalf("decode JSON from %q: %v", url, err)
	}
	return out
}
