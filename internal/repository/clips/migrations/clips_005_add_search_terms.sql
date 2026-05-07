-- clips_005_add_search_terms.sql
-- Aggiunge il campo search_terms per memorizzare le frasi di riferimento/query di ricerca
-- che hanno portato al download di questo clip

ALTER TABLE clips ADD COLUMN search_terms TEXT NOT NULL DEFAULT '[]';

-- Indice per ricerche basate su search_terms (JSON extraction)
-- SQLite non ha indici su campi JSON, ma possiamo usare espressioni
CREATE INDEX IF NOT EXISTS idx_clips_search_terms ON clips(search_terms);
