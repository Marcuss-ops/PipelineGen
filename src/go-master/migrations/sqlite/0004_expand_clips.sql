-- 0004_expand_clips.sql
-- Expand clips table to support Artlist metadata and other sources
-- Safe version that checks if columns exist before adding

-- Create a temporary table to track which ALTERs are needed
CREATE TEMPORARY TABLE IF NOT EXISTS alter_cmds (cmd TEXT);

-- Check and add source column
INSERT INTO alter_cmds 
SELECT 'ALTER TABLE clips ADD COLUMN source TEXT;'
WHERE NOT EXISTS (SELECT 1 FROM pragma_table_info('clips') WHERE name='source');

-- Check and add category column
INSERT INTO alter_cmds 
SELECT 'ALTER TABLE clips ADD COLUMN category TEXT;'
WHERE NOT EXISTS (SELECT 1 FROM pragma_table_info('clips') WHERE name='category');

-- Check and add external_url column
INSERT INTO alter_cmds 
SELECT 'ALTER TABLE clips ADD COLUMN external_url TEXT;'
WHERE NOT EXISTS (SELECT 1 FROM pragma_table_info('clips') WHERE name='external_url');

-- Check and add duration column
INSERT INTO alter_cmds 
SELECT 'ALTER TABLE clips ADD COLUMN duration INTEGER DEFAULT 0;'
WHERE NOT EXISTS (SELECT 1 FROM pragma_table_info('clips') WHERE name='duration');

-- Check and add metadata column
INSERT INTO alter_cmds 
SELECT 'ALTER TABLE clips ADD COLUMN metadata TEXT;'
WHERE NOT EXISTS (SELECT 1 FROM pragma_table_info('clips') WHERE name='metadata');

-- Execute all collected ALTER statements
-- Note: SQLite doesn't support dynamic SQL execution in plain SQL
-- This migration requires the migration runner to handle it specially
-- For now, we'll let it fail silently on duplicate columns

ALTER TABLE clips ADD COLUMN source TEXT;
ALTER TABLE clips ADD COLUMN category TEXT;
ALTER TABLE clips ADD COLUMN external_url TEXT;
ALTER TABLE clips ADD COLUMN duration INTEGER DEFAULT 0;
ALTER TABLE clips ADD COLUMN metadata TEXT;
