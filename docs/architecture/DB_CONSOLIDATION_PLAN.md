# Database Consolidation Plan

## Current State
The system uses 8 separate SQLite databases:
- `velox.db.sqlite` - Main DB (scripts, media, monitoring)
- `stock.db.sqlite` - Stock footage clips
- `clips.db.sqlite` - YouTube clips + embeddings
- `artlist.db.sqlite` - Artlist assets
- `images.db.sqlite` - Images (placeholder)
- `voiceover.db.sqlite` - Voiceovers (placeholder)
- `assets.db.sqlite` - Assets (if exists)
- `jobs.db.sqlite` - Job queue

## Target State
Consolidate to 3 databases:
1. **app.db.sqlite** - Application data (scripts, monitoring, config state)
2. **media.db.sqlite** - All media assets (stock, clips, artlist, images, voiceovers)
3. **jobs.db.sqlite** - Job queue (keep separate for concurrency)

## media.db.sqlite Schema

### Unified media table
```sql
CREATE TABLE media_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid TEXT UNIQUE NOT NULL,
    source TEXT NOT NULL, -- 'youtube', 'artlist', 'stock', 'image', 'voiceover'
    media_type TEXT NOT NULL, -- 'video', 'image', 'audio'
    title TEXT,
    description TEXT,
    duration REAL,
    file_path TEXT,
    file_size INTEGER,
    thumbnail_url TEXT,
    source_id TEXT, -- Original ID from source
    source_url TEXT,
    author TEXT,
    license TEXT,
    status TEXT DEFAULT 'active',
    metadata JSON, -- Flexible metadata
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    indexed_at DATETIME,
    CHECK (source IN ('youtube', 'artlist', 'stock', 'image', 'voiceover'))
);

CREATE INDEX idx_media_source ON media_items(source);
CREATE INDEX idx_media_status ON media_items(status);
CREATE INDEX idx_media_source_id ON media_items(source_id);
```

### Clip folders (for YouTube/clips)
```sql
CREATE TABLE clip_folders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    drive_folder_id TEXT,
    group_name TEXT, -- 'boxe', 'wwe', 'wnba', etc.
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Segment embeddings (for semantic search)
```sql
CREATE TABLE segment_embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    media_id INTEGER NOT NULL,
    segment_index INTEGER,
    embedding BLOB, -- Vector embedding
    text TEXT, -- Segment text
    FOREIGN KEY (media_id) REFERENCES media_items(id) ON DELETE CASCADE
);
```

## Migration Strategy

### Phase 1: Create new schema
1. Create `media.db.sqlite` with new schema
2. Keep old databases running in parallel

### Phase 2: Dual-write
1. Update all repositories to write to BOTH old and new DB
2. Verify data consistency

### Phase 3: Migrate data
1. Run migration script to copy all data from old DBs to new
2. Verify row counts match

### Phase 4: Switch reads
1. Update all read operations to use new DB
2. Keep dual-write for safety

### Phase 5: Remove old databases
1. After verification period, remove old DB files
2. Update all code to remove old repository references

## Benefits
- Single source of truth for all media
- Cross-source queries possible (find similar across YouTube + Artlist)
- Simplified backup/restore
- Reduced file handles and connections
- Easier schema migrations

## Risks
- Large migration task
- Data consistency during transition
- Need careful testing

## Timeline Estimate
- Phase 1: 1 day (schema + new repositories)
- Phase 2: 2-3 days (dual-write implementation)
- Phase 3: 1 day (migration script + verification)
- Phase 4: 1-2 days (switch reads + testing)
- Phase 5: 1 day (cleanup)

Total: 6-8 days of work
