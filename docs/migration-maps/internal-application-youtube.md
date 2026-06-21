# Migration Map — Internal Application YouTube

> **Status**: shipped (`62ae9cd6` merge commit on `origin/main`,
> `e52bf89b` feature commit).
> **Date**: 2026-06-21.
> **Branch**: `pr/youtube-port-cascade-2026-06-21` (open at this SHA).
>
> This doc captures the migration scope for the PR1.7 port abstraction
> cascade + PR2 followup canonical DownloaderMetadata DTO cleanup applied
> to `internal/application/youtube/`. Read alongside:
> - `docs/POST_CASCADE_OPERATIONAL_READINESS.md` — the post-cascade
>   operational checklist + followups
> - `architecture/migration.yaml` (Wave 4A canonical asset model — the
>   broader umbrella)
> - `AGENTS.md` §"Modular edit patterns" — the design rules that
>   motivated the cascade

---

## What changed

### Port abstractions (new structural ports with compile-time assertions)

The 12 ports declared in `internal/application/youtube/ports.go` were
either newly added or promoted from empty-marker to structural:

| Port | Signatures | Status change |
|------|-----------|---------------|
| `ClipStorePort` | `DB()`, `Get`, `GetClip`, `GetFolder`, `Upsert`, `UpsertFolder`, `DeleteClip` | new structural — added `UpsertFolder` (PR3 followup) |
| `MonitorsStorePort` | `UpsertSource`, `IncrementProcessed` | promoted empty→structural |
| `VideoMetadataFetcherPort` | `GetVideoMetadata(ctx, url) (*DownloaderMetadata, error)` | promoted with new return type |
| `DriveFolderManagerPort` | `GetOrCreateFolder`, `UploadFileIfChanged` (*UploadResultDTO, bool, error) | promoted; new return DTO |
| `FolderMemoryPort` | `LoadManifest`, `SaveManifest`, `UpdateManifestTXT`, `ComputeManifestStats` | promoted empty→structural |
| `OllamaClientPort` | `SimpleGenerate` | promoted empty→structural |
| `SearchRunnerPort` | `SearchLive`, `GetVideoInfo` | promoted empty→structural |
| `ClipIndexerPort` | `IsEnabled`, `IndexClip` | promoted empty→structural |
| `WhisperTranscriberPort` | `TranscribeAudio(ctx, audioPath) (string, error)` | promoted empty→structural |
| `ClipFilesPort` | `UsableCachedClip(localPath) (bool, error)` | promoted empty→structural with 2-tuple return (was bool) |
| `HashServicePort` | `MD5String(data) string`, `MD5File(path) (string, error)` | promoted empty→structural |
| `SubtitleFetcherPort` | `SliceSubtitles(ctx, videoID, startSec, endSec, outputPath) error` | promoted empty→structural |

Empty-marker ports **kept** as opaque injection tokens: `SubtitleFetcher`
(now structural), `WhisperTranscriber` (now structural), `HashService`
(now structural), `TempFileManagerPort`, `YouTubeCacheStorePort`.

**Compile-time claim pattern** (12 sites, one per port):
```go
var _ application.Port = (*ConcreteAdapter)(nil)
```
A grep of `var _ <port> = (*` (per-port) is the structural-drift audit;
if the concrete stops satisfying the port signature, compile fails on
the assertion line, not at the call site.

### Canonical DTO (PR2 followup)

`YouTubeMetadataPort interface{}` was the broken proxy in
`VideoCutResult.Metadata` (a pointer-to-empty-interface caused
`ym.Description` to fail type-check). Replaced with:

- `DownloaderMetadata` (canonical DTO): `ID, Title, URL, Description,
  Duration, Uploader, UploadDate, ViewCount, Language, ThumbnailURL,
  Thumbnails []VideoThumbnail, Chapters []VideoChapter, Categories
  []string, Tags []string, CachedAt time.Time`.
- Back-compat type aliases: `VideoMetadata = DownloaderMetadata`,
  `YouTubeMetadataPort = DownloaderMetadata`. Any historical identifier
  still resolves to the concrete DTO.

### Constructor collapse (PR1.7)

`Service.SetXxx(...)` setter cascade → `NewService(ServiceDeps{...})`
single-constructor with 21-field deps struct. Composition in
`internal/app/composition.go::BuildDomainBundle` now passes a
structured literal instead of chaining 11 setters.

Key behavioural changes:
- `VideoCutResult.Metadata` now `*DownloaderMetadata` (was broken).
- `Service.assetProcessing` + `Service.assetVersions` re-added as
  PR2-followup nil-safe parallel writers (initial commit incorrectly
  deleted them).
- Dropped: `s.disp == nil && s.indexer != nil { ... }` orphan guard
  in `segment.go`. `disp` was the previous outbox dispatcher concept;
  PR1.6 made `AssetRepository.Upsert` the single canonical writer, so
  the guard's left half was dead.

### Dead-code deletion

- `internal/infrastructure/youtube/ports.go::MetadataFetcherPort`
  deleted (zero external references confirmed via grep;
  `MetadataFetcherAdapter` already satisfies
  `youtubedto.VideoMetadataFetcherPort`).
- `internal/app/youtube_adapters.go` — `youtubeadapter` and `ytinfra`
  imports removed (unused after dead-port deletion).

### DTO additions

- `YouTubeCacheEntry{ VideoID, MetadataJSON string }` —
  `internal/application/youtube/ports.go`.
- (Tracked as MEDIUM followup in `docs/POST_CASCADE_OPERATIONAL_READINESS.md`
  §2 item 7 — DTO should relocate to
  `internal/infrastructure/database/sqlite/assets/youtube_cache.go`
  per Pattern 8.)

### Test file rewrites

- `internal/application/youtube/service_test.go` — both
  `NewService(cfg, log, nil, pipeline, nil, nil, nil, nil)`
  8-positional-arg calls migrated to `NewService(ServiceDeps{ ... })`
  struct literal.
- `internal/infrastructure/youtube/metadata_test.go` —
  `var raw YouTubeMetadata` → `var raw ytDLPJSON` (3 tests) +
  `raw.ThumbnailURL` → `raw.Thumbnail` (1 assertion). The test now
  covers the local JSON unmarshal target (the exported unparsed
  shape) rather than the deleted type.

### Documentation

- `service.go` package-doc previously said "Legacy fields
  `assetProcessing` + `assetVersions` have been removed" — corrected
  to "PR2 cascade followup: legacy optional fields were re-added on
  ServiceDeps + Service (NOT removed)".
- `service.go::md5File` now logs `Debug` on port error before falling
  back to the local helper (observability improvement).

---

## Files affected (12 modified)

```
internal/application/youtube/ports.go                 — full rewrite (canonical DTO + 12 structural ports)
internal/application/youtube/service.go                — single constructor; ServiceDeps 21 fields
internal/application/youtube/types.go                 — dropped legacy VideoMetadata/Chapter/Thumbnail struct forms
internal/application/youtube/metadata_persist.go      — 6 helpers take *DownloaderMetadata
internal/application/youtube/enrichment.go             — drops internal/infrastructure/downloader import; routes via s.metaFetcher
internal/application/youtube/enrichment_skipped.go    — uses *VideoCutResult shape
internal/application/youtube/extractor_segments.go     — routes via metaFetcher (download import dropped)
internal/application/youtube/extractor_drive.go       — UpsertFolder routes via s.clips
internal/application/youtube/service_test.go          — 2x NewService migrated to ServiceDeps literal
internal/infrastructure/youtube/metadata.go           — returns canonical *youtubedto.DownloaderMetadata
internal/infrastructure/youtube/metadata_test.go      — YouTubeMetadata → ytDLPJSON; ThumbnailURL → Thumbnail
internal/infrastructure/youtube/ports.go              — dead-code local MetadataFetcherPort deleted (zero refs)
internal/infrastructure/youtube/videopipeline_adapter.go  — converts videomuscles.YouTubeMetadata → *DownloaderMetadata
internal/app/youtube_adapters.go                      — dead-code adapters dropped; searchRunnerStub added; YouTubeCacheEntry consumer
internal/app/composition.go                           — ServiceDeps{...} literal (13 of 21 fields wired; rest bare nil)
```

(Files counted: 15 — the PR touched 15 files, including 3 that had
str_replace adjustments post-merge.)

---

## How to extend the cascade area

### Adding a new structural port

1. Declare the port in `internal/application/youtube/ports.go` with
   concrete method signatures (NOT empty marker). The type-band comment
   above the port explains the rationale.
2. Add the port to `ServiceDeps` struct + `Service` struct + the
   `NewService` assignment block, **all three** (per AGENTS.md
   Pattern 6).
3. Wire the concrete adapter at
   `internal/app/composition.go::BuildDomainBundle`, either:
   - As bare `nil:` (acceptable for not-yet-wired optional ports), OR
   - As a real `*Concrete` value via a new adapter constructor at
     `internal/app/youtube_adapters.go`. **Never** as `(*Concrete)(nil)`
     cast — typed-nil panics escape the `if s.x != nil` idiom.
4. Add `var _ application.Port = (*Concrete)(nil)` at the bottom of
   the adapter file. Compile fails on signature drift.
5. If the port has a `ValidateDeps` requirement (e.g. must be non-nil
   for the service to function), document it in the package-doc.

### Adding a new field to `DownloaderMetadata`

1. Add the field with `json:"..."` tag + `omitempty` if optional.
2. Update `internal/infrastructure/youtube/metadata.go` to populate it
   from `raw.<yt-dlp-json-field>` in the same function.
3. If the field is a typed slice (e.g. `Thumbnails` is `[]VideoThumbnail`),
   define the element type in `ports.go` (avoid anonymous structs).
4. Re-run `go test -c ./internal/infrastructure/youtube/` and
   `./internal/application/youtube/` to catch any test that was
   hard-coding against the anonymous-struct shape.

### Re-routing an existing `s.x.Method(...)` call through a port

Look at the symbol table:
- `s.x` is the consumer (Service struct field).
- `s.x.Method(...)` is the call site.
- The replacement `port.Method(...)` is the new call.

Search `internal/application/youtube/` for the call first; nuke
`internal/infrastructure/<concrete>/` importers only after every call is
routed through the port (greppable via `rg '\\bs\\.<field>\\.' internal/application/youtube/`).

---

## Verification gate used (post-scope)

```bash
# These ALL must be GREEN before the cascade commit lands:
go vet ./internal/application/youtube/... ./internal/infrastructure/youtube/... \
       ./internal/app/... ./internal/domain/asset/... ./cmd/server/...

go build ./internal/application/youtube/... ./internal/infrastructure/youtube/... \
         ./internal/app/... ./internal/domain/asset/... ./cmd/server/...

go test -c -o /dev/null ./internal/application/youtube/
go test -c -o /dev/null ./internal/infrastructure/youtube/
```

`go test ./...` full sweep is **deliberately out of scope** for the
cascade gate — see `docs/POST_CASCADE_OPERATIONAL_READINESS.md` §3 for
the 7 pre-existing package failures investigation.

---

## Latent risks post-cascade (3 BLOCKING)

These are NOT fixed in the cascade commit; they are tracked for separate
followup PRs (see the broader operational readiness doc):

1. **Thumbnails drift** — `metadata.go::GetVideoMetadata` still sets
   `Thumbnails: nil`. Downstream consumers (Qdrant re-indexers, front-end
   hires-fallback logic) silently lose multi-size thumbnail hints.
   *[Fix candidate: translate `raw.Thumbnails` in the same function.]*

2. **searchRunnerStub silent-empty** — `ctx.Err()` not checked; cancel
   storms produce `[]` results.
   *[Fix candidate: 1-line ctx.Err() guard at top of stub methods.]*

3. **Typed-nil panic risk** — `if s.x != nil` does NOT protect against
   `(*Concrete)(nil)` casts at composition-time.
   *[Fix candidate: `pkg/portutil/isNilPort[T]` utility + audit
   composition.go for typed-nil usage.]*

---

*Last updated: 2026-06-21 (post-ship).*
