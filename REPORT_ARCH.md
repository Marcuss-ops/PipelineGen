# Architectural Violations Heatmap (Production Code Only)

This report lists packages in `internal/application` and `internal/api` that import `internal/infrastructure`, along with files and exact line references to `*sql.DB`, `net/http`, `os`, and `RootFolderOverride`.

**Scope:** production Go files only (`*_test.go` excluded).
**Severity scoring:**
- 🔴 **3 — Structural/Field**: infrastructure type embedded in an struct or global state
- 🟠 **2 — Function signature**: infrastructure type leaked into application-layer function signatures
- 🟡 **1 — Business logic**: direct usage in application logic (imports excluded for `net/http`)

## Summary

| Package | *sql.DB | net/http | os | RootFolderOverride | Total Weight |
|---------|---------|----------|----|-------------------|--------------|
| `internal/api` | 0 | 3 | 1 | 0 | 4 |
| `internal/api/admin` | 0 | 1 | 0 | 0 | 1 |
| `internal/api/assets/clips` | 0 | 0 | 7 | 0 | 7 |
| `internal/api/middleware` | 2 | 17 | 0 | 0 | 19 |
| `internal/api/transport` | 0 | 7 | 0 | 0 | 7 |
| `internal/application/assets/artifacts` | 0 | 0 | 3 | 0 | 3 |
| `internal/application/assets/delivery` | 0 | 1 | 0 | 4 | 5 |
| `internal/application/assets/ingest` | 0 | 2 | 5 | 0 | 9 |
| `internal/application/assets/lifecycle` | 0 | 0 | 0 | 2 | 2 |
| `internal/application/assets/providers/artlist` | 3 | 0 | 3 | 0 | 10 |
| `internal/application/assets/providers/stock/enrichment` | 5 | 0 | 0 | 0 | 8 |
| `internal/application/assets/providers/stock/stockpipeline` | 2 | 0 | 20 | 1 | 25 |
| `internal/application/assets/sourcing/youtube` | 0 | 0 | 0 | 1 | 1 |
| `internal/application/assets/verification` | 0 | 0 | 8 | 0 | 8 |
| `internal/application/assets/videomuscles` | 0 | 0 | 7 | 0 | 7 |
| `internal/application/books` | 2 | 0 | 0 | 0 | 5 |
| `internal/application/clips` | 0 | 0 | 3 | 0 | 3 |
| `internal/application/images` | 0 | 4 | 4 | 1 | 11 |
| `internal/application/iobinder` | 0 | 0 | 5 | 0 | 5 |
| `internal/application/jobs` | 0 | 0 | 2 | 0 | 2 |
| `internal/application/jobs/assets` | 0 | 4 | 5 | 0 | 11 |
| `internal/application/jobs/finalizer` | 3 | 0 | 0 | 0 | 6 |
| `internal/application/jobs/iobinder` | 0 | 0 | 8 | 0 | 8 |
| `internal/application/jobs/outbox` | 6 | 10 | 12 | 0 | 38 |
| `internal/application/jobs/outbox/metadataexport` | 1 | 0 | 1 | 0 | 2 |
| `internal/application/jobs/worker` | 0 | 0 | 2 | 0 | 2 |
| `internal/application/qdrant/maintenance` | 1 | 0 | 0 | 0 | 3 |
| `internal/application/qdrant/reconciler` | 0 | 0 | 1 | 0 | 1 |
| `internal/application/transcripts` | 0 | 0 | 4 | 0 | 4 |
| `internal/application/voiceover` | 1 | 0 | 1 | 3 | 5 |
| `internal/application/voiceover/persistence` | 1 | 0 | 0 | 0 | 1 |
| `internal/application/workerdoctor` | 0 | 5 | 0 | 0 | 5 |
| `internal/application/youtube` | 0 | 0 | 8 | 0 | 8 |
| `internal/application/youtube/adapters` | 0 | 0 | 4 | 0 | 4 |
| `internal/application/youtube/usecase` | 0 | 0 | 8 | 0 | 8 |

**Total packages:** 35  
**Total weighted severity:** 248  
**Total references:** 215

## Detailed Findings

### internal/api

#### *sql.DB

No references found.

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/api/routes.go:235` | `c.Redirect(http.StatusMovedPermanently, "/health")` |
| 🟡 business_logic (1) | `internal/api/routes.go:294` | `metricsHandler := gin.WrapH(promhttp.Handler())` |
| 🟡 business_logic (1) | `internal/api/routes.go:298` | `c.AbortWithStatus(http.StatusUnauthorized)` |

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/api/routes.go:295` | `if token := os.Getenv("METRICS_AUTH_TOKEN"); token != "" {` |

#### RootFolderOverride

No references found.

### internal/api/admin

#### *sql.DB

No references found.

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/api/admin/handler_workers_cert.go:134` | `c.JSON(http.StatusNotFound, gin.H{` |

#### os

No references found.

#### RootFolderOverride

No references found.

### internal/api/assets/clips

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/api/assets/clips/clip_action.go:45` | `if info, statErr := os.Stat(result.LocalPath); statErr == nil && !info.IsDir() {` |
| 🟡 business_logic (1) | `internal/api/assets/clips/bulk_upload_transport.go:319` | `// osStat is a thin os.Stat indirection so the file can be unit-tested` |
| 🟡 business_logic (1) | `internal/api/assets/clips/bulk_upload_transport.go:320` | `// without filesystem shenanigans. Production wiring uses os.Stat directly.` |
| 🟡 business_logic (1) | `internal/api/assets/clips/bulk_upload_transport.go:322` | `info, err := os.Stat(path)` |
| 🟡 business_logic (1) | `internal/api/assets/clips/bulk_upload_transport.go:329` | `// osDirEntry is the minimal subset of os.FileInfo the transport` |
| 🟡 business_logic (1) | `internal/api/assets/clips/bulk_upload_transport.go:330` | `// uses (just IsDir). Production implements via os.Stat; tests can mock.` |
| 🟡 business_logic (1) | `internal/api/assets/clips/bulk_upload_transport.go:336` | `fi os.FileInfo` |

#### RootFolderOverride

No references found.

### internal/api/middleware

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/api/middleware/middleware_logger.go:14` | `// longer holds *sql.DB — the SQLite-backed implementation lives in` |
| 🟡 business_logic (1) | `internal/api/middleware/middleware_logger.go:133` | `// Persist via the typed sink (no raw *sql.DB in this layer).` |

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:143` | `apiutilError(c, http.StatusBadRequest,` |
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:148` | `apiutilError(c, http.StatusBadRequest,` |
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:161` | `apiutilError(c, http.StatusBadRequest,` |
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:175` | `apiutilError(c, http.StatusInternalServerError,` |
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:204` | `apiutilError(c, http.StatusInternalServerError,` |
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:210` | `apiutilError(c, http.StatusConflict,` |
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:224` | `apiutilError(c, http.StatusUnprocessableEntity,` |
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:269` | `statusCode:     http.StatusOK,` |
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:285` | `t.statusCode = http.StatusOK` |
| 🟡 business_logic (1) | `internal/api/middleware/idempotency.go:297` | `t.statusCode = http.StatusOK` |
| 🟡 business_logic (1) | `internal/api/middleware/middleware_middleware.go:59` | `c.JSON(http.StatusUnauthorized, gin.H{` |
| 🟡 business_logic (1) | `internal/api/middleware/middleware_middleware.go:126` | `c.JSON(http.StatusInternalServerError, gin.H{` |
| 🟡 business_logic (1) | `internal/api/middleware/middleware_middleware.go:142` | `c.JSON(http.StatusInternalServerError, gin.H{` |
| 🟡 business_logic (1) | `internal/api/middleware/middleware_middleware.go:193` | `c.JSON(http.StatusUnauthorized, gin.H{` |
| 🟡 business_logic (1) | `internal/api/middleware/middleware_middleware.go:213` | `c.JSON(http.StatusInternalServerError, gin.H{` |
| 🟡 business_logic (1) | `internal/api/middleware/admin_token.go:61` | `c.JSON(http.StatusInternalServerError, gin.H{` |
| 🟡 business_logic (1) | `internal/api/middleware/admin_token.go:89` | `c.JSON(http.StatusUnauthorized, gin.H{` |

#### os

No references found.

#### RootFolderOverride

No references found.

### internal/api/transport

#### *sql.DB

No references found.

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/api/transport/qdrant_health.go:79` | `c.JSON(http.StatusServiceUnavailable, gin.H{` |
| 🟡 business_logic (1) | `internal/api/transport/qdrant_health.go:90` | `c.JSON(http.StatusServiceUnavailable, gin.H{` |
| 🟡 business_logic (1) | `internal/api/transport/qdrant_health.go:97` | `c.JSON(http.StatusOK, gin.H{` |
| 🟡 business_logic (1) | `internal/api/transport/qdrant_health.go:109` | `c.JSON(http.StatusServiceUnavailable, gin.H{` |
| 🟡 business_logic (1) | `internal/api/transport/qdrant_health.go:117` | `c.JSON(http.StatusServiceUnavailable, gin.H{` |
| 🟡 business_logic (1) | `internal/api/transport/qdrant_health.go:193` | `status := http.StatusOK` |
| 🟡 business_logic (1) | `internal/api/transport/qdrant_health.go:195` | `status = http.StatusServiceUnavailable` |

#### os

No references found.

#### RootFolderOverride

No references found.

### internal/application/assets/artifacts

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/artifacts/finalizer.go:58` | `if _, err := os.Stat(rec.LocalPath); os.IsNotExist(err) {` |
| 🟡 business_logic (1) | `internal/application/assets/artifacts/finalizer.go:272` | `_ = os.WriteFile(metaPath, data, 0644)` |
| 🟡 business_logic (1) | `internal/application/assets/artifacts/finalizer.go:277` | `data, err := os.ReadFile(path)` |

#### RootFolderOverride

No references found.

### internal/application/assets/delivery

#### *sql.DB

No references found.

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/delivery/drive_validator_metrics.go:47` | `// surfaced via api/routes.go::/metrics (promhttp.Handler()).` |

#### os

No references found.

#### RootFolderOverride

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/delivery/types.go:222` | `// falls back to RootFolderOverride (legacy admin escape hatch)` |
| 🟡 business_logic (1) | `internal/application/assets/delivery/types.go:226` | `// RootFolderOverride, when non-empty, overrides the root folder ID` |
| 🟡 business_logic (1) | `internal/application/assets/delivery/types.go:233` | `// internal/application/** that pass RootFolderOverride should` |
| 🟡 business_logic (1) | `internal/application/assets/delivery/types.go:239` | `RootFolderOverride string `json:"root_folder_override,omitempty"`` |

### internal/application/assets/ingest

#### *sql.DB

No references found.

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🔴 struct_field (3) | `internal/application/assets/ingest/service.go:33` | `client     *http.Client` |
| 🟡 business_logic (1) | `internal/application/assets/ingest/service.go:59` | `client:      &http.Client{Timeout: 90 * time.Second},` |

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/ingest/service.go:94` | `if info, statErr := os.Stat(localPath); statErr == nil && info.IsDir() {` |
| 🟡 business_logic (1) | `internal/application/assets/ingest/image.go:38` | `if err := os.MkdirAll(fullDir, 0o755); err != nil {` |
| 🟡 business_logic (1) | `internal/application/assets/ingest/image.go:52` | `in, err := os.Open(sourcePath)` |
| 🟡 business_logic (1) | `internal/application/assets/ingest/image.go:58` | `out, err := os.Create(dstPath)` |
| 🟡 business_logic (1) | `internal/application/assets/ingest/image.go:65` | `_ = os.Remove(dstPath)` |

#### RootFolderOverride

No references found.

### internal/application/assets/lifecycle

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

No references found.

#### RootFolderOverride

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/lifecycle/service.go:160` | `// through RootFolderOverride (back-compat escape hatch for` |
| 🟡 business_logic (1) | `internal/application/assets/lifecycle/service.go:166` | `// PR-P12-LIFECYCLE-SEMANTIC (July 2026): RootFolderOverride` |

### internal/application/assets/providers/artlist

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🔴 struct_field (3) | `internal/application/assets/providers/artlist/service.go:113` | `MainDB            *sql.DB` |
| 🔴 struct_field (3) | `internal/application/assets/providers/artlist/service.go:168` | `mainDB *sql.DB` |
| 🟡 business_logic (1) | `internal/application/assets/providers/artlist/ports.go:10` | `//   - No port method accepts *sql.DB, *drive.Uploader, *clipindexer.Service,` |

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/providers/artlist/semantic_enricher_enrich.go:114` | `if data, err := os.ReadFile(localMetaPath); err == nil {` |
| 🟡 business_logic (1) | `internal/application/assets/providers/artlist/semantic_enricher_enrich.go:157` | `_ = os.WriteFile(localMetaPath, data, 0644)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/artlist/run_orchestrator_stages.go:465` | `fi, err := os.Stat(path)` |

#### RootFolderOverride

No references found.

### internal/application/assets/providers/stock/enrichment

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/enrichment/outbox_emitter.go:83` | `// The struct holds a *sql.DB (NOT a *outboxevents.Repository)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/enrichment/outbox_emitter.go:93` | `// db is the canonical *sql.DB handle. The composition root` |
| 🔴 struct_field (3) | `internal/application/assets/providers/stock/enrichment/outbox_emitter.go:96` | `db *sql.DB` |
| 🟠 function_signature (2) | `internal/application/assets/providers/stock/enrichment/outbox_emitter.go:107` | `func NewOutboxBackedAssetPublishedEmitter(db *sql.DB, log *zap.Logger) (*outboxBackedAssetPublishedEmitter, error) {` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/enrichment/handler.go:68` | `//     *sql.DB, reads media_assets by id, updates metadata_json)` |

#### net/http

No references found.

#### os

No references found.

#### RootFolderOverride

No references found.

### internal/application/assets/providers/stock/stockpipeline

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/service_types.go:131` | `// DB is the canonical *sql.DB handle (media.db.sqlite). STATO` |
| 🔴 struct_field (3) | `internal/application/assets/providers/stock/stockpipeline/service_types.go:137` | `DB *sql.DB` |

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:104` | `tmpDir, err := os.MkdirTemp(s.svc.cfg.Storage.TempPath(), "stock_stage_")` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:115` | `os.RemoveAll(tmpDir)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:143` | `if _, err := os.Stat(stagedPath); err != nil {` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:146` | `if fi, err := os.Stat(stagedPath); err == nil {` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:156` | `srcFile, err := os.Open(stagedPath)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:159` | `dstFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:178` | `fi, statErr := os.Stat(outputPath)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:211` | `os.RemoveAll(tmpDir)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:218` | `os.RemoveAll(tmpDir)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:222` | `fi, statErr := os.Stat(resolved)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:224` | `os.RemoveAll(tmpDir)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:243` | `return os.RemoveAll(dir)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:291` | `f, createErr := os.Create(outputPath)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:306` | `fi, statErr := os.Stat(outputPath)` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/process.go:6` | `// verified outputs with `os.Stat`. All of this leaked FFmpeg knowledge into` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/process.go:111` | `if info, statErr := os.Stat(actualPath); statErr == nil {` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/util.go:57` | `if _, err := os.Stat(basePath); err == nil {` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/util.go:60` | `if _, err := os.Stat(basePath + ".mp4"); err == nil {` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/util.go:63` | `if _, err := os.Stat(basePath + ".mkv"); err == nil {` |
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/util.go:66` | `if _, err := os.Stat(basePath + ".webm"); err == nil {` |

#### RootFolderOverride

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/providers/stock/stockpipeline/util.go:14` | `// delivery.PublishRequest{Group: seg, RootFolderOverride: currentID})`` |

### internal/application/assets/sourcing/youtube

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

No references found.

#### RootFolderOverride

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/sourcing/youtube/adapters.go:82` | `// PR-P12-YOUTUBE-LEGACY-RETIRE (July 2026): RootFolderOverride` |

### internal/application/assets/verification

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/verification/verified.go:44` | `//	LocalPath      ← staged.StagedArtifact.LocalPath  (VERIFIED via os.Stat)` |
| 🟡 business_logic (1) | `internal/application/assets/verification/verified.go:46` | `//	SizeBytes      ← staged.StagedArtifact.SizeBytes (VERIFIED via os.Stat)` |
| 🟡 business_logic (1) | `internal/application/assets/verification/verified.go:68` | `// Verifier runs the on-disk integrity gate. It calls os.Stat (cheap-first` |
| 🟡 business_logic (1) | `internal/application/assets/verification/verified.go:102` | `//   - os.Stat err                        → wrapped fmt.Errorf("...stat...: %w", err)` |
| 🟡 business_logic (1) | `internal/application/assets/verification/verified.go:134` | `fi, err := os.Stat(sa.LocalPath)` |
| 🟡 business_logic (1) | `internal/application/assets/verification/errors.go:4` | `// for the 2 drift detections: SHA-256 recompute miss, and os.Stat size miss.` |
| 🟡 business_logic (1) | `internal/application/assets/verification/errors.go:31` | `// ErrStagedSizeMismatch is returned when the on-disk os.Stat size differs` |
| 🟡 business_logic (1) | `internal/application/assets/verification/errors.go:38` | `"verification: staged SizeBytes does not match os.Stat size",` |

#### RootFolderOverride

No references found.

### internal/application/assets/videomuscles

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/assets/videomuscles/youtube_pipeline.go:82` | `if err := os.MkdirAll(videoDir, 0755); err != nil {` |
| 🟡 business_logic (1) | `internal/application/assets/videomuscles/youtube_pipeline.go:219` | `if _, wmErr := os.Stat(watermarkPath); wmErr == nil {` |
| 🟡 business_logic (1) | `internal/application/assets/videomuscles/youtube_pipeline.go:234` | `os.Remove(watermarkedPath)` |
| 🟡 business_logic (1) | `internal/application/assets/videomuscles/youtube_pipeline.go:236` | `os.Remove(outputPath)` |
| 🟡 business_logic (1) | `internal/application/assets/videomuscles/youtube_pipeline.go:237` | `os.Rename(watermarkedPath, outputPath)` |
| 🟡 business_logic (1) | `internal/application/assets/videomuscles/youtube_pipeline.go:244` | `_ = os.Remove(rawFile)` |
| 🟡 business_logic (1) | `internal/application/assets/videomuscles/youtube_pipeline.go:259` | `_, err := os.Stat(cookiesPath)` |

#### RootFolderOverride

No references found.

### internal/application/books

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🔴 struct_field (3) | `internal/application/books/service.go:116` | `db                *sql.DB` |
| 🟠 function_signature (2) | `internal/application/books/service.go:148` | `func NewService(cfg *Config, db *sql.DB, driveFolder string, log *zap.Logger, voiceoverExecutor voiceover.VoiceoverItemExecutor, publisher PublisherPort, reader drive.Reader, transformer BookTransformer) *Service {` |

#### net/http

No references found.

#### os

No references found.

#### RootFolderOverride

No references found.

### internal/application/clips

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/clips/upload_helpers.go:139` | `//  4. Defer os.Remove(metaTempPath) so a network hang or early` |
| 🟡 business_logic (1) | `internal/application/clips/upload_helpers.go:269` | `if err := os.WriteFile(metaTempPath, jsonBytes, 0644); err != nil {` |
| 🟡 business_logic (1) | `internal/application/clips/upload_helpers.go:280` | `if rmErr := os.Remove(metaTempPath); rmErr != nil && !os.IsNotExist(rmErr) {` |

#### RootFolderOverride

No references found.

### internal/application/images

#### *sql.DB

No references found.

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/images/service.go:107` | `client:        &http.Client{Timeout: 10 * time.Minute},` |
| 🔴 struct_field (3) | `internal/application/images/storage_service.go:34` | `client        *http.Client` |
| 🟡 business_logic (1) | `internal/application/images/storage_download.go:20` | `req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)` |
| 🟡 business_logic (1) | `internal/application/images/storage_download.go:30` | `if resp.StatusCode != http.StatusOK {` |

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/images/storage_drive.go:96` | `if _, err := os.Stat(filePath); err != nil {` |
| 🟡 business_logic (1) | `internal/application/images/storage_drive.go:176` | `if err := os.Remove(filePath); err != nil {` |
| 🟡 business_logic (1) | `internal/application/images/storage_drive.go:243` | `defer os.Remove(audioPath)` |
| 🟡 business_logic (1) | `internal/application/images/storage_download.go:60` | `if _, statErr := os.Stat(filePath); statErr == nil {` |

#### RootFolderOverride

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/images/storage_service.go:80` | `RootFolderOverride: req.DriveRootOverride,` |

### internal/application/iobinder

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/iobinder/doc.go:3` | `// directly import `database/sql` or call `os.Open` / `sql.Open`.` |
| 🟡 business_logic (1) | `internal/application/iobinder/doc.go:8` | `// the 52 known violations (16 os.Open-family + 0 sql.Open + 36` |
| 🟡 business_logic (1) | `internal/application/iobinder/doc.go:10` | `// os.Open-family hits break down as 10 actual `os.Open(...)` call` |
| 🟡 business_logic (1) | `internal/application/iobinder/doc.go:11` | `// sites + 3 `os.OpenFile(...)` call sites + 3 comment references` |
| 🟡 business_logic (1) | `internal/application/iobinder/doc.go:22` | `//   - PR-REFACTOR-P0-IO-BINDER-FS: replace inline os.Open calls in` |

#### RootFolderOverride

No references found.

### internal/application/jobs

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/worker.go:71` | `host, err := os.Hostname()` |
| 🟡 business_logic (1) | `internal/application/jobs/worker.go:75` | `workerIDPrefix = fmt.Sprintf("%s_%d", host, os.Getpid())` |

#### RootFolderOverride

No references found.

### internal/application/jobs/assets

#### *sql.DB

No references found.

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🔴 struct_field (3) | `internal/application/jobs/assets/service.go:28` | `httpClient    *http.Client` |
| 🟡 business_logic (1) | `internal/application/jobs/assets/service.go:59` | `httpClient:    &http.Client{Timeout: 2 * time.Minute},` |
| 🟡 business_logic (1) | `internal/application/jobs/assets/service.go:271` | `req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizeDownloadURL(rawURL), nil)` |
| 🟡 business_logic (1) | `internal/application/jobs/assets/service.go:345` | `func filenameFromResponse(resp *http.Response, fallbackURL string) string {` |

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/assets/service.go:51` | `uploadRoot = filepath.Join(os.TempDir(), "pipelinegen", "worker-uploads")` |
| 🟡 business_logic (1) | `internal/application/jobs/assets/service.go:83` | `f, err := os.Open(rec.LocalPath)` |
| 🟡 business_logic (1) | `internal/application/jobs/assets/service.go:133` | `if err := os.MkdirAll(filepath.Join(s.uploadRoot, assetID), 0o755); err != nil {` |
| 🟡 business_logic (1) | `internal/application/jobs/assets/service.go:156` | `if err := os.MkdirAll(dir, 0o755); err != nil {` |
| 🟡 business_logic (1) | `internal/application/jobs/assets/service.go:160` | `f, err := os.Create(dst)` |

#### RootFolderOverride

No references found.

### internal/application/jobs/finalizer

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/finalizer/job_finalizer.go:66` | `// It holds a *sql.DB (to open transactions), an *outboxevents.Repository` |
| 🔴 struct_field (3) | `internal/application/jobs/finalizer/job_finalizer.go:70` | `db      *sql.DB` |
| 🟠 function_signature (2) | `internal/application/jobs/finalizer/job_finalizer.go:80` | `func New(db *sql.DB, outbox *outboxevents.Repository, assetTx finalization.AssetFinalizerTx, log *zap.Logger) *Finalizer {` |

#### net/http

No references found.

#### os

No references found.

#### RootFolderOverride

No references found.

### internal/application/jobs/iobinder

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/iobinder/doc.go:7` | `//	Remove synchronous os.ReadFile / os.Open in hot paths; lift to` |
| 🟡 business_logic (1) | `internal/application/jobs/iobinder/doc.go:14` | `// os.ReadFile hits: 0` |
| 🟡 business_logic (1) | `internal/application/jobs/iobinder/doc.go:15` | `// os.Open hits: 1 — internal/application/jobs/assets/service.go:83` |
| 🟡 business_logic (1) | `internal/application/jobs/iobinder/doc.go:21` | `// The 1 os.Open hit is documented in the test's exceptionList as the` |
| 🟡 business_logic (1) | `internal/application/jobs/iobinder/doc.go:29` | `// but are NOT covered by `os.ReadFile|os.Open` and are out of scope for` |
| 🟡 business_logic (1) | `internal/application/jobs/iobinder/doc.go:31` | `//   - 2 os.Stat calls (internal/application/jobs/worker/runner_upload.go:128, 160)` |
| 🟡 business_logic (1) | `internal/application/jobs/iobinder/doc.go:33` | `//   - 2 os.Create calls (internal/application/jobs/assets/service.go:160,` |
| 🟡 business_logic (1) | `internal/application/jobs/iobinder/doc.go:42` | `//	PR-IOBINDER-P2-DOWNLOAD: migrate Service.Download's per-asset os.Open to` |

#### RootFolderOverride

No references found.

### internal/application/jobs/outbox

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/outbox/registry.go:21` | `// *sql.DB and a *http.Client (both documented as safe for concurrent` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/registry.go:40` | `// DB: *sql.DB backing store. Required for the DeliveryHandler` |
| 🔴 struct_field (3) | `internal/application/jobs/outbox/registry.go:106` | `DB                     *sql.DB` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/registry.go:279` | `// wire time. The application package no longer touches *sql.DB or os.` |
| 🔴 struct_field (3) | `internal/application/jobs/outbox/delivery.go:169` | `db          *sql.DB` |
| 🟠 function_signature (2) | `internal/application/jobs/outbox/delivery.go:189` | `func NewDeliveryHandler(log *zap.Logger, client *http.Client, db *sql.DB, hmacSecrets [][]byte, insecureDev bool) *DeliveryHandler {` |

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/outbox/registry.go:21` | `// *sql.DB and a *http.Client (both documented as safe for concurrent` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/registry.go:49` | `// HTTPClient: *http.Client used by DeliveryHandler for outbound POSTs.` |
| 🔴 struct_field (3) | `internal/application/jobs/outbox/registry.go:107` | `HTTPClient             *http.Client` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/drive_delete.go:438` | `// with Code == http.StatusNotFound. DriveDeleteHandler uses this to` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/drive_delete.go:464` | `return ge.Code == http.StatusNotFound` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/delivery.go:44` | `// handler is concurrency-safe (http.Client documented safe for` |
| 🔴 struct_field (3) | `internal/application/jobs/outbox/delivery.go:168` | `client      *http.Client` |
| 🟠 function_signature (2) | `internal/application/jobs/outbox/delivery.go:189` | `func NewDeliveryHandler(log *zap.Logger, client *http.Client, db *sql.DB, hmacSecrets [][]byte, insecureDev bool) *DeliveryHandler {` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/delivery.go:194` | `client = &http.Client{Timeout: defaultDeliveryTimeout}` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/delivery.go:309` | `httpReq, err := http.NewRequestWithContext(postCtx, http.MethodPost, req.Destination.DestinationID, bytes.NewReader(body))` |

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:35` | `//  5. Local file remove: os.Remove per path; os.IsNotExist swallowed` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:105` | `//     CleanedPath). The handler attempts os.Remove on each;` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:106` | `//     os.IsNotExist is silently swallowed for idempotency; the` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:175` | `//     (os.IsNotExist swallowed).` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:333` | `// os.Remove is stdlib, idempotent at the syscall layer, and the` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:337` | `// kernel, not the test). os.IsNotExist is SILENTLY SWALLOWED for` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:349` | `rmErr := os.Remove(p)` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:350` | `if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:351` | `// os.Remove failures that are NOT "file already gone"` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/voiceover_cleanup.go:359` | `return fmt.Errorf("voiceover.cleanup.requested os.Remove(%s): %w", p, rmErr)` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/registry.go:103` | `// local file removal branch still runs because os.Remove is` |
| 🟡 business_logic (1) | `internal/application/jobs/outbox/registry.go:304` | `// still runs local file removal via stdlib os.Remove). This` |

#### RootFolderOverride

No references found.

### internal/application/jobs/outbox/metadataexport

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/outbox/metadataexport/handler.go:57` | `// Signature change vs the pre-Step-2 constructor `(log, *sql.DB, dir)`:` |

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/outbox/metadataexport/ports.go:81` | `//     .tmp + os.Rename inside the same directory` |

#### RootFolderOverride

No references found.

### internal/application/jobs/worker

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/jobs/worker/tools.go:215` | `if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {` |
| 🟡 business_logic (1) | `internal/application/jobs/worker/tools.go:218` | `f, err := os.Create(dst)` |

#### RootFolderOverride

No references found.

### internal/application/qdrant/maintenance

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🔴 struct_field (3) | `internal/application/qdrant/maintenance/service.go:95` | `sqliteDB   *sql.DB` |

#### net/http

No references found.

#### os

No references found.

#### RootFolderOverride

No references found.

### internal/application/qdrant/reconciler

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/qdrant/reconciler/ports.go:104` | `// returned verbatim from json.MarshalIndent and os.WriteFile.` |

#### RootFolderOverride

No references found.

### internal/application/transcripts

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/transcripts/ytdlp_subtitles.go:372` | `tempDir, err := os.MkdirTemp("", "ytdlp_subs_*")` |
| 🟡 business_logic (1) | `internal/application/transcripts/ytdlp_subtitles.go:376` | `defer os.RemoveAll(tempDir)` |
| 🟡 business_logic (1) | `internal/application/transcripts/ytdlp_subtitles.go:409` | `entries, err := os.ReadDir(tempDir)` |
| 🟡 business_logic (1) | `internal/application/transcripts/ytdlp_subtitles.go:424` | `vttData, err := os.ReadFile(vttPath)` |

#### RootFolderOverride

No references found.

### internal/application/voiceover

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/voiceover/upload_intent.go:269` | `// run on the *sql.DB autocommitt handle, NOT on the tx-bound` |

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/voiceover/process.go:393` | `if err := os.MkdirAll(outputDir, 0755); err != nil {` |

#### RootFolderOverride

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/voiceover/upload_intent.go:193` | `// preserved via `PublishRequest.RootFolderOverride` to keep` |
| 🟡 business_logic (1) | `internal/application/voiceover/upload_intent.go:293` | `// `PublishRequest.RootFolderOverride` to keep byte-equivalent` |
| 🟡 business_logic (1) | `internal/application/voiceover/upload_intent.go:307` | `// DRIVE-IS-DRIVE (July 2026): RootFolderOverride REMOVED.` |

### internal/application/voiceover/persistence

#### *sql.DB

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/voiceover/persistence/repository.go:34` | `// *sql.DB.BeginTx(ctx, nil): default isolation (deferred),` |

#### net/http

No references found.

#### os

No references found.

#### RootFolderOverride

No references found.

### internal/application/workerdoctor

#### *sql.DB

No references found.

#### net/http

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/workerdoctor/default_probes.go:44` | `// HTTPDoFunc is http.Client.Do's signature. Replaced in tests to` |
| 🟡 business_logic (1) | `internal/application/workerdoctor/default_probes.go:45` | `// avoid network. Default = a fresh http.Client with a 5-second timeout` |
| 🟡 business_logic (1) | `internal/application/workerdoctor/default_probes.go:47` | `type HTTPDoFunc func(req *http.Request) (*http.Response, error)` |
| 🟡 business_logic (1) | `internal/application/workerdoctor/default_probes.go:62` | `// a 5s http.Client with no redirect following. The 5s timeout matches` |
| 🟡 business_logic (1) | `internal/application/workerdoctor/default_probes.go:68` | `HTTPDo: (&http.Client{` |

#### os

No references found.

#### RootFolderOverride

No references found.

### internal/application/youtube

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/youtube/stager_adapter.go:57` | `tempDir, err := os.MkdirTemp(s.tempPath, "yt_stage_")` |
| 🟡 business_logic (1) | `internal/application/youtube/stager_adapter.go:74` | `os.RemoveAll(tempDir)` |
| 🟡 business_logic (1) | `internal/application/youtube/stager_adapter.go:80` | `os.RemoveAll(tempDir)` |
| 🟡 business_logic (1) | `internal/application/youtube/stager_adapter.go:84` | `fi, statErr := os.Stat(localPath)` |
| 🟡 business_logic (1) | `internal/application/youtube/stager_adapter.go:86` | `os.RemoveAll(tempDir)` |
| 🟡 business_logic (1) | `internal/application/youtube/stager_adapter.go:90` | `os.RemoveAll(tempDir)` |
| 🟡 business_logic (1) | `internal/application/youtube/stager_adapter.go:116` | `return os.RemoveAll(dir)` |
| 🟡 business_logic (1) | `internal/application/youtube/stager_adapter.go:123` | `_, err := os.Stat(path)` |

#### RootFolderOverride

No references found.

### internal/application/youtube/adapters

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/youtube/adapters/segment_finder.go:130` | `tempDir, err := os.MkdirTemp("", "subs_segments_*")` |
| 🟡 business_logic (1) | `internal/application/youtube/adapters/segment_finder.go:135` | `defer os.RemoveAll(tempDir)` |
| 🟡 business_logic (1) | `internal/application/youtube/adapters/segment_finder.go:154` | `dirEntries, err := os.ReadDir(tempDir)` |
| 🟡 business_logic (1) | `internal/application/youtube/adapters/segment_finder.go:175` | `vttData, err := os.ReadFile(vttPath)` |

#### RootFolderOverride

No references found.

### internal/application/youtube/usecase

#### *sql.DB

No references found.

#### net/http

No references found.

#### os

| Severity | File:Line | Line Content |
|----------|-----------|--------------|
| 🟡 business_logic (1) | `internal/application/youtube/usecase/process_segment_step6to9.go:8` | `//     `os.WriteFile` of the *.txt tempfile) lives ONLY here. NOTE:` |
| 🟡 business_logic (1) | `internal/application/youtube/usecase/process_segment_step6to9.go:9` | `//     the os.WriteFile(txtPath, ...) call is referenced by a` |
| 🟡 business_logic (1) | `internal/application/youtube/usecase/process_segment_step6to9.go:82` | `_ = os.WriteFile(txtPath, []byte(resolvedTranscript), 0o644)` |
| 🟡 business_logic (1) | `internal/application/youtube/usecase/process_segment_step6to9.go:106` | `// NOTE: the os.WriteFile(txtPath, ...) call below is` |
| 🟡 business_logic (1) | `internal/application/youtube/usecase/process_segment_step6to9.go:115` | `// os.Stat's it for the Qdrant transcript embedding` |
| 🟡 business_logic (1) | `internal/application/youtube/usecase/process_segment_step6to9.go:123` | `_ = os.WriteFile(txtPath, []byte(transcript), 0o644)` |
| 🟡 business_logic (1) | `internal/application/youtube/usecase/extract_important_clips.go:281` | `if fi, statErr := os.Stat(localPath); statErr != nil || (fi != nil && fi.Size() == 0) {` |
| 🟡 business_logic (1) | `internal/application/youtube/usecase/callbacks.go:311` | `_ = os.Remove(existingClip.LocalPath())` |

#### RootFolderOverride

No references found.

