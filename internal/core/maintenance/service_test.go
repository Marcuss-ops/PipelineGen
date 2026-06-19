package maintenance

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	assettreerepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMaintenancePruning(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	// 1. Create in-memory DB with all needed schemas
	db := drive.NewTestDB(t, &drive.TestDBOpts{InMemory: true})
	defer db.Close()

	// Setup necessary schemas for assettree if repository creation performs checks
	drive.MustExec(t, db, `
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

	// Setup schema for asset_index to satisfy orphan file cleanup checks
	drive.MustExec(t, db, `
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

	// 2. Setup api_requests table
	drive.MustExec(t, db, `
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

	// 3. Seed old, recent, and current requests
	drive.MustExec(t, db, `
		INSERT INTO api_requests (ts, method, path, status) VALUES 
		(datetime('now', '-35 days'), 'GET', '/old', 200),
		(datetime('now', '-10 days'), 'GET', '/recent', 200),
		(datetime('now'), 'POST', '/current', 201);
	`)

	// Verify we have 3 records initially
	var initialCount int
	err := db.QueryRow("SELECT COUNT(*) FROM api_requests").Scan(&initialCount)
	require.NoError(t, err)
	assert.Equal(t, 3, initialCount)

	// 4. Setup mock dependencies for Service
	cfg := &config.Config{}
	cfg.Jobs.RetentionDays = 30

	// Set up simple asset tree service
	treeRepo, err := sqlite.NewAssetTreeRepository(db, logger)
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
	cleanupResults, cleanupErr := svc.RunCleanup(ctx, false, false)
	require.NoError(t, cleanupErr)
	assert.NotNil(t, cleanupResults)

	// Verify that 1 old record was pruned
	deletedVal, ok := cleanupResults["api_requests_deleted"]
	assert.True(t, ok)
	assert.Equal(t, int64(1), deletedVal)

	// Verify remaining records (2 should be left: recent and current)
	var finalCount int
	err = db.QueryRow("SELECT COUNT(*) FROM api_requests").Scan(&finalCount)
	require.NoError(t, err)
	assert.Equal(t, 2, finalCount)

	// Verify that the remaining paths are /recent and /current
	pathRows, err := db.Query("SELECT path FROM api_requests ORDER BY ts ASC")
	require.NoError(t, err)
	defer pathRows.Close()

	var paths []string
	for pathRows.Next() {
		var path string
		err = pathRows.Scan(&path)
		require.NoError(t, err)
		paths = append(paths, path)
	}

	assert.Equal(t, []string{"/recent", "/current"}, paths)
}
