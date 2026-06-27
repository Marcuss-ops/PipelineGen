// Package ports holds the application-layer port interfaces the
// youtube package consumes (e.g. ClipStorePort, SearchRunnerPort,
// DriveFolderManagerPort). The structural 16-port surface AND the
// DTOs (DownloaderMetadata, VideoCutRequest/Result, SearchLiveResult,
// UploadResultDTO) live here per AGENTS.md Pattern 0 + AGENTS.md §
// "compile-time assertions to catch drift".
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): existing youtube/ports/
// sub-package content stays 1:1 — kind ft. already aligns with our
// 7-way taxonomy.
//
// Import discipline:
//   - MUST NOT import other youtube/ sub-packages.
//   - MAY import internal/domain/asset and stdlib-only.
package ports
