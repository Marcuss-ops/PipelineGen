// Package sqlitecache hosts local cache/state that does not own authority.
//
// Intended role:
//   - fast local cache
//   - materialized lookup state
//   - resumable local indexing data
//
// Not intended role:
//   - system of record for jobs/workers/orchestration
package sqlitecache
