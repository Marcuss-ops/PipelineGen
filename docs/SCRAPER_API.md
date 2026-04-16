# Scraper API Documentation

The Node.js scraper integration provides endpoints to search, download, and manage stock video clips from multiple sources: **Artlist** (your own content), **Pixabay**, and **Pexels**.

## Base URL

```
http://localhost:8080/api/scraper
```

---

## Endpoints

### 1. Search Videos

Search for video clips using Artlist GraphQL API.

**Endpoint:** `POST /api/scraper/search`

**Request Body:**
```json
{
  "search_term": "spider",
  "max_pages": 5,
  "source": "artlist"
}
```

**Parameters:**
| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `search_term` | string | Yes | - | Search query (e.g., "nature", "city", "spider") |
| `max_pages` | int | No | 5 | Maximum pages to fetch (50 clips/page) |
| `source` | string | No | "artlist" | Video source: "artlist", "pixabay", "pexels" |
| `category` | string | No | - | Category to associate with search |

**Response:**
```json
{
  "search_term": "spider",
  "source": "artlist",
  "max_pages": 5,
  "clips_found": 250,
  "clips": [
    {
      "source": "artlist",
      "id": 20592,
      "name": "spider on web in front of shining sun",
      "duration": 11200,
      "width": 4608,
      "height": 2160,
      "url": "https://cms-public-artifacts.artlist.io/...",
      "thumbnail": "https://artgrid.imgix.net/..."
    }
  ]
}
```

**Example (curl):**
```bash
curl -X POST http://localhost:8080/api/scraper/search \
  -H "Content-Type: application/json" \
  -d '{"search_term": "nature", "max_pages": 2}'
```

---

### 2. Get Scraper Stats

Get statistics about the scraper database.

**Endpoint:** `GET /api/scraper/stats`

**Response:**
```json
{
  "scraper": "node-scraper",
  "status": "ready",
  "output_dir": "src/node-scraper/Output",
  "database": "src/node-scraper/artlist_videos.db"
}
```

---

### 3. Seed Database

Initialize the scraper database with default categories and search terms.

**Endpoint:** `POST /api/scraper/seed`

**Response:**
```json
{
  "status": "success",
  "message": "Database seeded successfully",
  "output": "✅ Added 8 categories\n✅ Added 64 search terms\n"
}
```

**Example (curl):**
```bash
curl -X POST http://localhost:8080/api/scraper/seed
```

---

### 4. List Categories

List all categories in the scraper database.

**Endpoint:** `GET /api/scraper/categories`

**Response:**
```json
{
  "categories": "CATEGORY         | DESCRIPTION               | TERMS | VIDEOS\n─────────────────+───────────────────────────+-------+-------\nNature           | Natural landscapes...    | 8     | 0\n"
}
```

---

### 5. Download Clips

Download pending video clips from the database to disk.

**Endpoint:** `POST /api/scraper/download`

**Request Body:**
```json
{
  "category": "Nature"
}
```

**Parameters:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `category` | string | No | Category name (omit to download all) |

**Response:**
```json
{
  "status": "success",
  "output": "📥 Download Manager\n════════════════════════════════════════\n..."
}
```

**Example (curl):**
```bash
# Download all pending clips
curl -X POST http://localhost:8080/api/scraper/download

# Download specific category
curl -X POST http://localhost:8080/api/scraper/download \
  -H "Content-Type: application/json" \
  -d '{"category": "Nature"}'
```

---

## Usage Workflow

### Typical workflow:

1. **Seed the database** (first time only):
   ```bash
   POST /api/scraper/seed
   ```

2. **Search for clips**:
   ```bash
   POST /api/scraper/search
   { "search_term": "nature", "max_pages": 5 }
   ```

3. **Review results** - The search returns metadata without downloading

4. **Download selected clips** (optional):
   ```bash
   POST /api/scraper/download
   ```

---

## Notes

- **Artlist** is your own content - no ToS violation
- **API keys** for Pixabay/Pexels are optional (defaults provided)
- **Output directory**: Videos are saved to `src/node-scraper/Output/`
- **Database**: SQLite at `src/node-scraper/artlist_videos.db`
- **Max clips per search**: 50 clips/page × max_pages (default 5 = 250 clips)

---

## Integration with Video Pipeline

The scraper integrates with the VeloxEditing video creation pipeline:

1. Search clips via `/api/scraper/search`
2. Clip URLs are stored in the SQLite database
3. The Go backend can then use these URLs in the `/api/stock/*` and `/api/clip/*` endpoints
4. The Rust binary downloads and processes clips during video assembly

---

## Testing

Test the scraper directly via CLI:

```bash
cd src/node-scraper

# Seed database
node scripts/cli.js seed

# Search Artlist (no download)
node scripts/map_artlist.js "spider" 2 --no-download

# Check stats
node scripts/cli.js stats "Nature"

# Test Pixabay API
node -e "import { searchAllPages } from './src/pixabay_api.js'; const r = await searchAllPages('nature', 2); console.log(r.length, 'videos');"
```
