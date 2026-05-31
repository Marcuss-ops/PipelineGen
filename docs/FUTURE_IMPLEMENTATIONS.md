# Future Implementations & Next Actions

This document outlines the immediate technical priorities and architectural improvements planned for the next development phases, following the successful implementation of the 6-worker concurrent pipeline.

## 1. "Warm" Artlist Pipeline (Low-Latency Stock Search)
**Goal:** Drastically reduce the time it takes to find and retrieve stock clips from Artlist.
*   **Problem:** Currently, the Artlist search and scrape pipeline may suffer from startup latency or session initialization overhead during script generation.
*   **Action:** Implement a "warm" session pool for the Artlist pipeline (similar to the Playwright pooling used for Google Vids). By keeping authenticated sessions or API wrappers persistently active in the background, the system can instantly query and retrieve stock footage as soon as the script scenes are generated. 
*   **Bonus:** Implement background pre-fetching for common narrative themes (e.g., "technology", "nature", "stress") to build a hot cache of ready-to-use clips.

## 2. Unified Metadata Enrichment & Pre-Linking (Stock, Clips, Artlist)
**Goal:** Enable instantaneous asset discovery and scene-matching across all sources.
*   **Problem:** Finding the right clip for a specific scene requires complex real-time search logic, which can be slow if metadata is sparse or unstandardized across different providers.
*   **Action:** Add a robust, unified metadata enrichment step at *ingest time* for all assets (`stock`, `clips` from YouTube, and `artlist`).
*   **Implementation:** 
    *   Automatically generate semantic tags, emotional tone, and visual objects via Ollama for *every* incoming asset.
    *   Pre-link related assets (e.g., matching a YouTube clip's visual embedding with a similar Artlist B-roll) in the database.
    *   When the script generation pipeline runs, it will simply query these pre-linked, highly enriched metadata fields for instant matching, rather than doing heavy lifting on the fly.

## 3. SQLite Concurrency & Lock Management
**Goal:** Prevent `database is locked` errors under the new high-concurrency load.
*   **Problem:** With the recent upgrade to 6 concurrent Go workers (goroutines) generating images and writing paths to the database simultaneously, the SQLite database (even in WAL mode) is at risk of encountering write-lock contention under heavy stress.
*   **Action:** 
    *   **Connection Pool:** Ensure the Go database connection pool is properly configured for SQLite concurrency. Specifically, set `SetMaxOpenConns(1)` for the database connection handling writes to serialize write operations safely.
    *   **Busy Timeout:** Ensure the database connection string or initialization routine explicitly sets `PRAGMA busy_timeout = 5000;` (or higher). This forces SQLite to automatically queue and retry locks for up to 5 seconds instead of immediately throwing an error.
    *   **Observability:** Actively monitor `journalctl` for slow database locks or write failures during batch generations to catch any unhandled contention early.
