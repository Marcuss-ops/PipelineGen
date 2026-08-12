package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	_ "github.com/mattn/go-sqlite3"
)

type State string

const (
	Requested     State = "REQUESTED"
	Creating      State = "CREATING"
	Created       State = "CREATED"
	Verifying     State = "VERIFYING"
	Verified      State = "VERIFIED"
	RestoreTested State = "RESTORE_TESTED"
	Failed        State = "FAILED"
)

type Manifest struct {
	BackupID             string `json:"backup_id"`
	ControlPlaneDBSHA256 string `json:"control_plane_db_sha256"`
	SchemaVersion        int    `json:"schema_version"`
	DatabaseID           string `json:"database_id"`
	RegistrySeq          int64  `json:"registry_seq"`
	CASManifestSHA256    string `json:"cas_manifest_sha256"`
	CASObjectCount       int64  `json:"cas_object_count"`
	QdrantProjection     string `json:"qdrant_projection"`
	QdrantSnapshotSHA256 string `json:"qdrant_snapshot_sha256"`
	GitSHA               string `json:"git_sha"`
	CreatedAt            string `json:"created_at"`
}

type Manager struct{}

func NewManager() *Manager { return &Manager{} }

func (m *Manager) CreateControlPlaneBackup(ctx context.Context, source, destination, manifestPath string) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	r, err := storage.Backup(source, destination)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := buildManifest(destination, r.SHA256)
	if err != nil {
		return Manifest{}, err
	}
	if manifestPath == "" {
		manifestPath = destination + ".manifest.json"
	}
	if err := writeManifest(manifestPath, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m *Manager) VerifyControlPlaneBackup(ctx context.Context, backupPath, manifestPath string) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return manifest, fmt.Errorf("decode backup manifest: %w", err)
	}
	sha, err := fileSHA256(backupPath)
	if err != nil {
		return manifest, err
	}
	if sha != manifest.ControlPlaneDBSHA256 {
		return manifest, fmt.Errorf("backup manifest hash mismatch: got %s want %s", sha, manifest.ControlPlaneDBSHA256)
	}
	db, err := sql.Open("sqlite3", backupPath+"?mode=ro&_query_only=1")
	if err != nil {
		return manifest, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return manifest, err
	}
	if integrity != "ok" {
		return manifest, fmt.Errorf("backup integrity check: %s", integrity)
	}
	return manifest, nil
}

func (m *Manager) RunRestoreDrill(ctx context.Context, backupPath, destination string) (*storage.RestoreResult, error) {
	return storage.Restore(ctx, backupPath, destination)
}

func buildManifest(path, dbSHA string) (Manifest, error) {
	db, err := sql.Open("sqlite3", path+"?mode=ro&_query_only=1")
	if err != nil {
		return Manifest{}, err
	}
	defer db.Close()
	var schemaVersion int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		return Manifest{}, err
	}
	var databaseID string
	_ = db.QueryRow(`SELECT database_id FROM control_plane_meta LIMIT 1`).Scan(&databaseID)
	var registrySeq int64
	_ = db.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM registry_events`).Scan(&registrySeq)
	var casCount int64
	_ = db.QueryRow(`SELECT COUNT(*) FROM content_objects`).Scan(&casCount)
	return Manifest{BackupID: fmt.Sprintf("backup_%d", time.Now().UnixNano()), ControlPlaneDBSHA256: dbSHA, SchemaVersion: schemaVersion, DatabaseID: databaseID, RegistrySeq: registrySeq, CASObjectCount: casCount, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

func writeManifest(path string, manifest Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(manifest)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
