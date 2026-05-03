-- Create FTS5 virtual table for clip_folders search
CREATE VIRTUAL TABLE IF NOT EXISTS clip_folders_fts USING fts5(
  source_url,
  video_id,
  folder_path,
  group_name,
  content='clip_folders',
  content_rowid='id'
);

-- Triggers to keep FTS in sync with clip_folders table
CREATE TRIGGER IF NOT EXISTS clip_folders_ai AFTER INSERT ON clip_folders BEGIN
  INSERT INTO clip_folders_fts(rowid, source_url, video_id, folder_path, group_name)
  VALUES (new.id, new.source_url, new.video_id, new.folder_path, new.group_name);
END;

CREATE TRIGGER IF NOT EXISTS clip_folders_au AFTER UPDATE ON clip_folders BEGIN
  INSERT INTO clip_folders_fts(clip_folders_fts, rowid) VALUES('delete', old.id);
  INSERT INTO clip_folders_fts(rowid, source_url, video_id, folder_path, group_name)
  VALUES (new.id, new.source_url, new.video_id, new.folder_path, new.group_name);
END;

CREATE TRIGGER IF NOT EXISTS clip_folders_ad AFTER DELETE ON clip_folders BEGIN
  INSERT INTO clip_folders_fts(clip_folders_fts, rowid) VALUES('delete', old.id);
END;
