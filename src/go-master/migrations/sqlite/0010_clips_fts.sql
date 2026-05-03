-- Create FTS5 virtual table for clips search
CREATE VIRTUAL TABLE IF NOT EXISTS clips_fts USING fts5(
  name,
  tags,
  folder_path,
  group_name,
  category,
  content='clips',
  content_rowid='id'
);

-- Triggers to keep FTS in sync with clips table
CREATE TRIGGER IF NOT EXISTS clips_ai AFTER INSERT ON clips BEGIN
  INSERT INTO clips_fts(rowid, name, tags, folder_path, group_name, category)
  VALUES (new.id, new.name, new.tags, new.folder_path, new.group_name, new.category);
END;

CREATE TRIGGER IF NOT EXISTS clips_au AFTER UPDATE ON clips BEGIN
  INSERT INTO clips_fts(clips_fts, rowid) VALUES('delete', old.id);
  INSERT INTO clips_fts(rowid, name, tags, folder_path, group_name, category)
  VALUES (new.id, new.name, new.tags, new.folder_path, new.group_name, new.category);
END;

CREATE TRIGGER IF NOT EXISTS clips_ad AFTER DELETE ON clips BEGIN
  INSERT INTO clips_fts(clips_fts, rowid) VALUES('delete', old.id);
END;
