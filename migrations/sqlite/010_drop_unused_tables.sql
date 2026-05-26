-- Droppa tabelle inutilizzate.
-- media_items, media_files, media_tags erano modelli "unificati" mai realmente popolati.
-- asset_index e asset_tree_nodes sono attivi (5208 e 2645 righe) -> restano come indice/cache.

DROP TABLE IF EXISTS media_tags;
DROP TABLE IF EXISTS media_files;
DROP TABLE IF EXISTS media_items;
