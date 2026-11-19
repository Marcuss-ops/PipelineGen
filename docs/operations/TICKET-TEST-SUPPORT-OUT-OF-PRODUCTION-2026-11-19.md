# TICKET — Test support declared in production files (2026-11-19)

- **Status:** OPEN (2026-11-19) — 26 test-only functions compiled into production files, plus the whole `mutationprobe` package
- **Severity:** P2 — production files declare helpers only test binaries can use
- **Owner:** platform test infrastructure
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

`internal/platform/mutationprobe` is a whole package (7 functions) that nothing
imports: `Enabled`, `Run`, `runProbe`, `runChildTest`, `buildFailure`,
`moduleRoot`, `tail`. `internal/platform/sqlite/testdb.go` adds 5 (`NewTestDB`,
`NewTestDBWithSchema`, `NewMigratedTestDB`, `testMigrationsDir`, `MustExec`), and
`internal/capabilities/scripts/usecase/testsupport/doubles.go` adds 7 test
doubles declared in a non-`_test.go` file.

The rest are `ForTest` reset hooks left in production files:
`SetDefaultNowForTests`, `resetYouTubeGateForTest`, `resetUploadChunkForTest`,
`resetEnvKeepAliveForTest`, `resetObjectPresenceCacheForTest`,
`resetClipEvidenceFieldCharsForTest`, `resetSegmentLayoutForTest`.

The sweep ticket's rule applies: no production file may declare a helper that
only a test calls. These files compile into the binary and are invisible to the
`-test=false` reachability of their own symbols.

## Scope (measured)

| File | # |
|---|---:|
| `internal/capabilities/scripts/usecase/testsupport/doubles.go` | 7 |
| `internal/platform/mutationprobe/mutationprobe.go` | 7 |
| `internal/platform/sqlite/testdb.go` | 5 |
| `internal/capabilities/assets/monitor/types_dto.go` | 1 |
| `internal/capabilities/scripts/usecase/gencore/engine_prompt.go` | 1 |
| `internal/capabilities/scripts/usecase/gencore/segment_prompt_layout.go` | 1 |
| `internal/platform/downloader/youtube_gate.go` | 1 |
| `internal/platform/drive/uploader_chunk.go` | 1 |
| `internal/platform/ollama/client/client_keepalive.go` | 1 |
| `internal/platform/renderinggen/queue_asset_prefetch.go` | 1 |
| **Total** | **26** |

Key symbols: `mutationprobe.Run`, `sqlite.NewMigratedTestDB`, `resetYouTubeGateForTest`

## Plan

1. `mutationprobe`: `rg` the import path; if nothing imports it, delete the
   package. If a CI target does, move it under `tools/` and call it explicitly.
2. `sqlite/testdb.go`: move the constructors into `testdb_test.go` (same
   package) or into an `internal/platform/sqlite/sqltest` package imported only
   by tests; keep `go test` working for the packages that use them.
3. `testsupport/doubles.go`: rename to `doubles_test.go` if only tests in that
   package use it; otherwise move it behind a build tag and document the import.
4. Reset hooks: move each into the matching `_test.go`, keeping the same package
   so the test can call it.

## Acceptance criteria

- [ ] `mutationprobe` is deleted or moved to an explicitly test-only location.
- [ ] No non-test file declares a `*ForTest(s)` helper or a test double.
- [ ] `go build ./...` and `go test ./internal/platform/... ./internal/capabilities/scripts/... -count=1`
      are green.
- [ ] The global deadcode guard records the new count.

## Verification

```sh
deadcode -test=false ./... | grep -E 'mutationprobe|sqlite/testdb.go|testsupport|ForTest'
go test ./internal/platform/... ./internal/capabilities/scripts/... -count=1
```
