package maintenance

import (
	"context"
	"database/sql"
	"testing"

	"velox/go-master/internal/config"
	"velox/go-master/internal/media"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assettree"
	assettreerepo "velox/go-master/internal/repository/assettree"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMaintenancePruning(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	// 1. Create in-memory DB
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	require.NoError(t, err)
	defer db.Close()

	// Setup necessary schemas for assettree if repository creation performs checks
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS asset_nodes (
			id TEXT PRIMARY KEY,
			parent_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			node_type TEXT NOT NULL DEFAULT 'folder',
			asset_type TEXT NOT NULL DEFAULT '',
			asset_source TEXT NOT NULL DEFAULT '',
			asset_id TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);
	`)
	require.NoError(t, err)

	// Setup schema for asset_index to satisfy orphan file cleanup checks
	_, err = db.Exec(`
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
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);
	`)
	require.NoError(t, err)

	// 2. Setup api_requests table
	_, err = db.Exec(`
		CREATE TABLE api_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts DATETIME DEFAULT CURRENT_TIMESTAMP,
			request_id TEXT,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			status INTEGER,
			duration_ms REAL,
			client_ip TEXT,
			user_id TEXT,
			bytes_in INTEGER,
			bytes_out INTEGER,
			user_agent TEXT,
			error TEXT
		);
	`)
	require.NoError(t, err)

	// 3. Seed old, recent, and current requests
	_, err = db.Exec(`
		INSERT INTO api_requests (ts, method, path, status) VALUES 
		(datetime('now', '-35 days'), 'GET', '/old', 200),
		(datetime('now', '-10 days'), 'GET', '/recent', 200),
		(datetime('now'), 'POST', '/current', 201);
	`)
	require.NoError(t, err)

	// Verify we have 3 records initially
	var initialCount int
	err = db.QueryRow("SELECT COUNT(*) FROM api_requests").Scan(&initialCount)
	require.NoError(t, err)
	assert.Equal(t, 3, initialCount)

	// 4. Setup mock dependencies for Service
	cfg := &config.Config{}
	
	// Set up simple asset tree service
	treeRepo, err := assettreerepo.NewRepository(db, logger)
	require.NoError(t, err)
	treeSvc := assettree.NewService(treeRepo, logger)

	// Set up simple asset index service
	idxRepo := assetindex.NewRepository(db)
	idxSvc := assetindex.NewService(idxRepo)

	// Set up deletion service
	deletionSvc := media.NewDeletionService(
		nil, nil, nil, nil, nil, nil,
		treeSvc,
		idxSvc,
		logger,
	)

	// 5. Create Maintenance Service with our in-memory DB
	svc := NewService(cfg, logger, idxSvc, treeSvc, deletionSvc, nil, db)

	// 6. Run the cleanup job
	results, err := svc.RunCleanup(ctx, false, false)
	require.NoError(t, err)
	assert.NotNil(t, results)

	// Verify that 1 old record was pruned
	deletedVal, ok := results["api_requests_deleted"]
	assert.True(t, ok)
	assert.Equal(t, int64(1), deletedVal)

	// Verify remaining records (2 should be left: recent and current)
	var finalCount int
	err = db.QueryRow("SELECT COUNT(*) FROM api_requests").Scan(&finalCount)
	require.NoError(t, err)
	assert.Equal(t, 2, finalCount)

	// Verify that the remaining paths are /recent and /current
	rows, err := db.Query("SELECT path FROM api_requests ORDER BY ts ASC")
	require.NoError(t, err)
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		err = rows.Scan(&path)
		require.NoError(t, err)
		paths = append(paths, path)
	}

	assert.Equal(t, []string{"/recent", "/current"}, paths)
}
