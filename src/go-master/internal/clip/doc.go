// Package clip provides low-level utilities and types for video clip management.
//
// It includes:
// 1. Data Models: Definitions of clip metadata, search result formats, and indexing types.
// 2. Indexing: Tools for scanning local folders or remote drives to catalog existing video files.
// 3. Scoring: Algorithms for calculating semantic relevance between textual phrases and clip metadata.
// 4. Sources: Adapters for specific clip sources (e.g., Artlist, YouTube).
//
// Unlike the higher-level clipsearch service, this package focuses on the individual clip entity
// and its properties within the system.
package clip
