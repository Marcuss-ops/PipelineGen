-- 007_create_pipeline_run_items.sql
-- Pipeline run items table for tracking individual items in a pipeline run

CREATE TABLE IF NOT EXISTS pipeline_run_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    asset_id TEXT,
    item_type TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT,
    metadata TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pipeline_run_items_run_id
ON pipeline_run_items(run_id);

CREATE INDEX IF NOT EXISTS idx_pipeline_run_items_asset_id
ON pipeline_run_items(asset_id)
WHERE asset_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_pipeline_run_items_type
ON pipeline_run_items(item_type)
WHERE item_type IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_pipeline_run_items_status
ON pipeline_run_items(status);

CREATE INDEX IF NOT EXISTS idx_pipeline_run_items_composite
ON pipeline_run_items(run_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_pipeline_run_items_created_at
ON pipeline_run_items(created_at DESC);
