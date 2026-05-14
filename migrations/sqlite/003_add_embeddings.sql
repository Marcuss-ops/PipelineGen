-- SQLite Migration: Aggiunta colonne per Hybrid Search
-- Aggiunge embedding_json a tabelle destinate alla ricerca semantica

-- Clips (database: clips.db.sqlite)
ALTER TABLE clips ADD COLUMN embedding_json TEXT;
ALTER TABLE clips ADD COLUMN tags_norm TEXT;

-- Artlist Tracks (database: artlist.db.sqlite)
ALTER TABLE clips ADD COLUMN embedding_json TEXT; -- Supponendo che la tabella sia clips o tracks, in base allo script python (artlist.db.sqlite "SELECT search_text, embedding_json FROM clips")

-- Stock (database: stock.db.sqlite)
-- (Sostituisci "stock_media" o "clips" in base allo schema reale)
-- ALTER TABLE stock_media ADD COLUMN embedding_json TEXT;
-- ALTER TABLE stock_media ADD COLUMN tags_norm TEXT;
