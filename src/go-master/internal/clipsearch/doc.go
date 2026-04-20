// Package clipsearch provides a high-level service for searching and downloading video clips dynamically.
//
// It serves as an orchestrator for several low-level operations:
// 1. Searching: Queries multiple sources (YouTube, Artlist, local cache) to find video segments matching a keyword.
// 2. Downloading: Fetches remote clips and persists them to a local storage or Google Drive.
// 3. Checkpointing: Maintains a record of current search jobs to handle retries and prevent duplicate work.
// 4. Processing: Refines search results, handles metadata extraction, and ensures consistent result formats.
//
// The service is designed to be called by higher-level components like the script pipeline to fill
// visual gaps when pre-indexed stock clips are insufficient.
package clipsearch
