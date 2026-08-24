// Package adapters wires the canonical external service adapters
// (yt-dlp subprocess, YouTube Data API, qdrant vector-store,
// hashtag normalizers, segment-analysis) to the youtube pipeline.
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): tagutil (hashtag
// normalization), segments (segment analysis computation),
// metadata (clip-metadata extraction) each have ~3-5 files that
// FOLD into this package as the new home for the canonical
// "compute" surface of the youtube pipeline.
//
// Import discipline:
//   - MAY import contracts/, dto/, ports/, usecase/.
//   - MAY import external infrastructure under godlike/06 §"Single
//     capability ownership" strictness.
package adapters
