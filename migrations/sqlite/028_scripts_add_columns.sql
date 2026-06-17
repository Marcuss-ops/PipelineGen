-- 028_scripts_add_columns.sql
-- Add missing columns for richer script and section metadata.

-- scripts table
ALTER TABLE scripts ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE scripts ADD COLUMN tone TEXT NOT NULL DEFAULT '';
ALTER TABLE scripts ADD COLUMN target_words INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scripts ADD COLUMN final_word_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scripts ADD COLUMN status TEXT NOT NULL DEFAULT 'completed';

-- script_sections table
ALTER TABLE script_sections ADD COLUMN word_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE script_sections ADD COLUMN status TEXT NOT NULL DEFAULT 'completed';

-- Indexes for new columns
CREATE INDEX IF NOT EXISTS idx_scripts_status ON scripts(status);
CREATE INDEX IF NOT EXISTS idx_scripts_tone ON scripts(tone);
CREATE INDEX IF NOT EXISTS idx_script_sections_status ON script_sections(status);
