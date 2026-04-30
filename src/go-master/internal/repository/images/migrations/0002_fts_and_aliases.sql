-- internal/repository/images/migrations/0002_fts_and_aliases.sql

CREATE VIRTUAL TABLE IF NOT EXISTS subjects_fts USING fts4(display_name, aliases, notes, content='subjects');

CREATE TRIGGER IF NOT EXISTS subjects_ai AFTER INSERT ON subjects BEGIN INSERT INTO subjects_fts(docid, display_name, aliases, notes) VALUES (new.id, new.display_name, new.aliases, new.notes); END;
CREATE TRIGGER IF NOT EXISTS subjects_ad AFTER DELETE ON subjects BEGIN INSERT INTO subjects_fts(subjects_fts, docid, display_name, aliases, notes) VALUES('delete', old.id, old.display_name, old.aliases, old.notes); END;
CREATE TRIGGER IF NOT EXISTS subjects_au AFTER UPDATE ON subjects BEGIN INSERT INTO subjects_fts(subjects_fts, docid, display_name, aliases, notes) VALUES('delete', old.id, old.display_name, old.aliases, old.notes); INSERT INTO subjects_fts(docid, display_name, aliases, notes) VALUES (new.id, new.display_name, new.aliases, new.notes); END;

CREATE VIRTUAL TABLE IF NOT EXISTS images_fts USING fts4(description, metadata_json, content='images');

CREATE TRIGGER IF NOT EXISTS images_ai AFTER INSERT ON images BEGIN INSERT INTO images_fts(docid, description, metadata_json) VALUES (new.id, new.description, new.metadata_json); END;
CREATE TRIGGER IF NOT EXISTS images_ad AFTER DELETE ON images BEGIN INSERT INTO images_fts(images_fts, docid, description, metadata_json) VALUES('delete', old.id, old.description, old.metadata_json); END;
CREATE TRIGGER IF NOT EXISTS images_au AFTER UPDATE ON images BEGIN INSERT INTO images_fts(images_fts, docid, description, metadata_json) VALUES('delete', old.id, old.description, old.metadata_json); INSERT INTO images_fts(docid, description, metadata_json) VALUES (new.id, new.description, new.metadata_json); END;
