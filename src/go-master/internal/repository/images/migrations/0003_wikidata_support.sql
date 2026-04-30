-- internal/repository/images/migrations/0003_wikidata_support.sql

ALTER TABLE subjects ADD COLUMN wikidata_id TEXT;
CREATE INDEX IF NOT EXISTS idx_subjects_wikidata ON subjects(wikidata_id);

-- Aggiorniamo l'indice FTS per includere il nuovo campo (Downgrade a FTS4)
DROP TABLE IF EXISTS subjects_fts;
CREATE VIRTUAL TABLE subjects_fts USING fts4(
    display_name,
    aliases,
    notes,
    wikidata_id,
    content='subjects'
);
