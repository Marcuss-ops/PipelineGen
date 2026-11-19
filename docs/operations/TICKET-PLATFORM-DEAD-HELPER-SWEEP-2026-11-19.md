# TICKET — Platform infrastructure: dead registry methods, loaders and adapters (2026-11-19)

- **Status:** OPEN (2026-11-19) — 61 unreachable functions across 47 files under `internal/platform`
- **Severity:** P2 — registry and loader APIs that no module registers or loads
- **Owner:** platform infra owners by package
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

The largest groups are `httpserver` registries (`Registry.Register`, `Find`,
`GetEnabled`, `RuntimeModules`, `Server.GetRouter`), `ai/vlm` (3),
`checksum` (3 provider-MD5 helpers), `rustworker` (3 resource-limiter and
process-runner methods), `sqlite/twinddl` (3 normalizers), `ollama/prompts`
(4 loaders), `httpserver/middleware` (5), `embeddings` (3), `instaeditcalendar`
(2), `media/rustexec` (3), `renderinggen` (2), and single-file helpers in
`filesystem`, `ptrutil`, `process`, `stager`, `drive`, `downloader`, `config`,
`assets/materializer`, `audio`, `observability`, `overlays`, `postgres/media`
and `sqlite/*`.

Several are introspection APIs (registry listings) that look supported but are
not; others are loaders for configuration files no longer shipped. The registry
methods are the ones to settle first: either the runtime registers modules
through them or the map is built another way and the methods go.

## Scope (measured)

| File | # |
|---|---:|
| `internal/platform/ai/vlm/client.go` | 3 |
| `internal/platform/checksum/checksum.go` | 3 |
| `internal/platform/httpserver/module_base.go` | 3 |
| `internal/platform/rustworker/worker.go` | 3 |
| `internal/platform/sqlite/twinddl/generate.go` | 3 |
| `internal/platform/embeddings/contract_probe.go` | 2 |
| `internal/platform/instaeditcalendar/client.go` | 2 |
| `internal/platform/ollama/prompts/config.go` | 2 |
| `internal/platform/ollama/prompts/manifest.go` | 2 |
| `internal/platform/ai/reranker/client.go` | 1 |
| `internal/platform/assets/materializer/materializer.go` | 1 |
| `internal/platform/audio/worker_protocol.go` | 1 |
| `internal/platform/config/defaults.go` | 1 |
| `internal/platform/embeddings/http_text_embedder.go` | 1 |
| `internal/platform/filesystem/pathutil.go` | 1 |
| `internal/platform/httpserver/middleware/idempotency.go` | 1 |
| `internal/platform/httpserver/middleware/middleware_feature_flags.go` | 1 |
| `internal/platform/httpserver/middleware/middleware_logger.go` | 1 |
| `internal/platform/httpserver/middleware/middleware_workspace_scope.go` | 1 |
| `internal/platform/httpserver/middleware/ports.go` | 1 |
| `internal/platform/httpserver/runtime_descriptor.go` | 1 |
| `internal/platform/httpserver/server.go` | 1 |
| `internal/platform/media/rustexec/client.go` | 1 |
| `internal/platform/media/rustexec/executor.go` | 1 |
| `internal/platform/media/rustexec/observed_executor.go` | 1 |
| `internal/platform/observability/metrics_runreports.go` | 1 |
| `internal/platform/ollama/adapters/person_image_collage.go` | 1 |
| `internal/platform/ollama/adapters/person_image_scorer.go` | 1 |
| `internal/platform/ollama/client/admission_metrics.go` | 1 |
| `internal/platform/ollama/client/websearch.go` | 1 |
| `internal/platform/ollama/types/utils.go` | 1 |
| `internal/platform/overlays/gpu_gate.go` | 1 |
| `internal/platform/postgres/media/media_searcher.go` | 1 |
| `internal/platform/process/tools_checker.go` | 1 |
| `internal/platform/ptrutil/ptrutil.go` | 1 |
| `internal/platform/rendercacheinfo/info.go` | 1 |
| `internal/platform/renderinggen/font_assets.go` | 1 |
| `internal/platform/sqlite/assets/youtubediscoveries/youtube_discoveries_helpers.go` | 1 |
| `internal/platform/sqlite/backup.go` | 1 |
| `internal/platform/sqlite/controlplane/verifier.go` | 1 |
| `internal/platform/sqlite/executionsteps/store.go` | 1 |
| `internal/platform/sqlite/migrations_verify.go` | 1 |
| `internal/platform/sqlite/outboxevents/pool.go` | 1 |
| `internal/platform/sqlite/outboxevents/supersede.go` | 1 |
| `internal/platform/sqlite/performance/projection.go` | 1 |
| `internal/platform/sqlite/statematrix/statematrix.go` | 1 |
| `internal/platform/stager/http_source_stager.go` | 1 |
| **Total** | **61** |

Key symbols: `httpserver.Registry.Register/Find/GetEnabled`, `rustworker.ResourceLimiter.Acquire`, `ollama/prompts.Load`

## Plan

1. Settle `httpserver.Registry`: if the production module set is built by
   another path, delete the four methods and `Server.GetRouter`; otherwise wire
   and test the runtime descriptor.
2. Delete the dead loaders (`ollama/prompts`, `config`, `twinddl`) and their
   tests, unless a config file in the repo still needs them.
3. Sweep the remaining single-file helpers package by package, one commit each.
4. Re-measure after each commit; keep `go test ./internal/platform/...` green.

## Acceptance criteria

- [ ] 0 unreachable functions under `internal/platform` (excluding the
      test-support ticket's files).
- [ ] No registry API claims support that the runtime path does not use.
- [ ] `go build ./...`, `go vet ./internal/platform/...` and
      `go test ./internal/platform/... -count=1` are green.
- [ ] The global deadcode guard records the new count.

## Verification

```sh
deadcode -test=false ./... | grep '^internal/platform/'
go test ./internal/platform/... -count=1
```
