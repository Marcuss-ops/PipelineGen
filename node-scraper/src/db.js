import Database from 'better-sqlite3';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT_DIR = path.resolve(__dirname, '..');
const LIVE_DB_PATH = path.join(ROOT_DIR, 'artlist_videos.db');
const BACKUP_DB_PATH = path.join(ROOT_DIR, 'artlist_videos.db.backup_20260414_180734');

let dbInstance = null;

function ensureDatabaseFile() {
  if (!fs.existsSync(LIVE_DB_PATH) && fs.existsSync(BACKUP_DB_PATH)) {
    fs.copyFileSync(BACKUP_DB_PATH, LIVE_DB_PATH);
  }
}

function ensureSchema(db) {
  db.exec(`
    CREATE TABLE IF NOT EXISTS categories (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      name TEXT UNIQUE NOT NULL,
      description TEXT,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
      updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );

    CREATE TABLE IF NOT EXISTS search_terms (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      category_id INTEGER NOT NULL,
      term TEXT NOT NULL,
      scraped INTEGER DEFAULT 0,
      last_scraped DATETIME,
      video_count INTEGER DEFAULT 0,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
      FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE,
      UNIQUE(category_id, term)
    );

    CREATE INDEX IF NOT EXISTS idx_terms_category ON search_terms(category_id);
    CREATE INDEX IF NOT EXISTS idx_terms_scraped ON search_terms(scraped);

    CREATE TABLE IF NOT EXISTS video_links (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      search_term_id INTEGER NOT NULL,
      category_id INTEGER NOT NULL,
      url TEXT NOT NULL,
      video_id TEXT,
      file_name TEXT,
      file_size REAL,
      downloaded INTEGER DEFAULT 0,
      download_path TEXT,
      source TEXT DEFAULT 'unknown',
      width INTEGER DEFAULT 0,
      height INTEGER DEFAULT 0,
      duration INTEGER DEFAULT 0,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
      FOREIGN KEY (search_term_id) REFERENCES search_terms(id) ON DELETE CASCADE,
      FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE,
      UNIQUE(search_term_id, url)
    );

    CREATE INDEX IF NOT EXISTS idx_videos_category ON video_links(category_id);
    CREATE INDEX IF NOT EXISTS idx_videos_downloaded ON video_links(downloaded);
    CREATE INDEX IF NOT EXISTS idx_videos_source ON video_links(source);
  `);
}

function openDB() {
  ensureDatabaseFile();
  const db = new Database(LIVE_DB_PATH);
  db.pragma('foreign_keys = ON');
  db.pragma('journal_mode = WAL');
  ensureSchema(db);
  return db;
}

function getDB() {
  if (!dbInstance) {
    dbInstance = openDB();
  }
  return dbInstance;
}

function closeDB() {
  if (dbInstance) {
    dbInstance.close();
    dbInstance = null;
  }
}

function getCategoryByName(db, name) {
  return db.prepare('SELECT * FROM categories WHERE lower(name) = lower(?)').get(name);
}

function ensureCategory(db, name, description = '') {
  const existing = getCategoryByName(db, name);
  if (existing) {
    if (description && description !== existing.description) {
      db.prepare('UPDATE categories SET description = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?').run(description, existing.id);
      return { ...existing, description };
    }
    return existing;
  }

  const result = db.prepare('INSERT INTO categories (name, description) VALUES (?, ?)').run(name, description);
  return { id: Number(result.lastInsertRowid), name, description };
}

export const categories = {
  add(name, description = '') {
    return ensureCategory(getDB(), name, description);
  },

  getByName(name) {
    return getCategoryByName(getDB(), name);
  },
};

export const searchTerms = {
  addMultiple(categoryName, terms) {
    const db = getDB();
    const category = ensureCategory(db, categoryName, '');
    const insert = db.prepare('INSERT OR IGNORE INTO search_terms (category_id, term) VALUES (?, ?)');
    let count = 0;

    for (const term of terms) {
      const normalized = String(term || '').trim().toLowerCase();
      if (!normalized) continue;
      const info = insert.run(category.id, normalized);
      if (info.changes > 0) {
        count += 1;
      }
    }

    return count;
  },

  list(categoryName, options = {}) {
    const db = getDB();
    const category = getCategoryByName(db, categoryName);
    if (!category) return [];

    const onlyPending = options.onlyPending === true;
    const minVideoCount = Number.isFinite(options.minVideoCount) && options.minVideoCount >= 0
      ? Math.trunc(options.minVideoCount)
      : 10;
    const limit = Number.isFinite(options.limit) && options.limit > 0 ? Math.trunc(options.limit) : 0;

    let sql = `
      SELECT term, scraped, last_scraped, video_count
      FROM search_terms
      WHERE category_id = ?
    `;
    const params = [category.id];

    if (onlyPending) {
      sql += ' AND COALESCE(video_count, 0) < ?';
      params.push(minVideoCount);
    }

    sql += ' ORDER BY COALESCE(video_count, 0) DESC, lower(term) ASC';

    if (limit > 0) {
      sql += ' LIMIT ?';
      params.push(limit);
    }

    return db.prepare(sql).all(...params);
  },

  markScraped(categoryName, term, videoCount = 0) {
    const db = getDB();
    const category = getCategoryByName(db, categoryName);
    if (!category) return 0;

    const row = db.prepare('SELECT id FROM search_terms WHERE category_id = ? AND lower(term) = lower(?)').get(category.id, term);
    if (!row) return 0;

    return db.prepare(`
      UPDATE search_terms
      SET scraped = 1,
          last_scraped = CURRENT_TIMESTAMP,
          video_count = ?
      WHERE id = ?
    `).run(videoCount, row.id).changes;
  },
};

function resolveTermId(db, categoryId, term) {
  const row = db.prepare('SELECT id FROM search_terms WHERE category_id = ? AND lower(term) = lower(?)').get(categoryId, term);
  return row ? row.id : null;
}

function toInt(value, fallback = 0) {
  const n = Number(value);
  return Number.isFinite(n) ? Math.trunc(n) : fallback;
}

export const videoLinks = {
  addMultipleWithSource(categoryName, term, urls, source = 'artlist', metadata = []) {
    const db = getDB();
    const category = ensureCategory(db, categoryName, '');
    let termId = resolveTermId(db, category.id, term);

    if (!termId) {
      const inserted = db.prepare('INSERT INTO search_terms (category_id, term) VALUES (?, ?)').run(category.id, String(term).trim().toLowerCase());
      termId = Number(inserted.lastInsertRowid);
    }

    const insert = db.prepare(`
      INSERT OR IGNORE INTO video_links
        (search_term_id, category_id, url, video_id, file_name, file_size, downloaded, download_path, source, width, height, duration)
      VALUES
        (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)
    `);

    let count = 0;
    urls.forEach((url, index) => {
      const meta = metadata[index] || {};
      const cleanedUrl = String(url || '').trim();
      if (!cleanedUrl) return;
      const videoId = String(meta.video_id || cleanedUrl.split('/').filter(Boolean).pop() || '').trim();
      const fileName = String(meta.file_name || videoId || '').trim();
      const fileSize = meta.size !== undefined ? Number(meta.size) : null;
      const downloadPath = meta.download_path ? String(meta.download_path) : null;
      const width = toInt(meta.width, 0);
      const height = toInt(meta.height, 0);
      const duration = toInt(meta.duration, 0);
      const result = insert.run(
        termId,
        category.id,
        cleanedUrl,
        videoId || null,
        fileName || null,
        fileSize,
        downloadPath,
        source,
        width,
        height,
        duration
      );
      if (result.changes > 0) {
        count += 1;
      }
    });

    if (count > 0) {
      db.prepare(`
        UPDATE search_terms
        SET scraped = 1,
            last_scraped = CURRENT_TIMESTAMP,
            video_count = video_count + ?
        WHERE id = ?
      `).run(count, termId);
    }

    return count;
  },
};

export { getDB, closeDB };
