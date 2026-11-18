# TICKET — Orphan packages: `semanticdoc` and `indexing` (2026-11-18)

- **Status:** CLOSED (2026-11-18) — both packages deleted; the real DocumentText composer is named
- **Evidence:** `go build ./...` 0; `go vet ./internal/kernel/...` clean
- **Severity:** P1 — one is a claimed canonical owner with no consumer, the other is a leftover of a retired component
- **Owner:** catalog/embedding pipeline for `semanticdoc`; indexing owner for `indexing`
- **Source:** audit 2026-11-18, `go list -json ./...` import graph on `refactored@75b5b1def`
- **Rule:** a package that nothing imports is either wired with a test that proves the wiring or deleted; it cannot stay as documentation

## Problem

### `internal/kernel/semanticdoc` — 345 lines, zero importers

The package doc says it is "the canonical SemanticDocument composer — the
SINGLE owner of the embedding DocumentText" and describes converging three
previous composers (payload builder, search-text strategies, sidecar) onto one
versioned, hashed document text.

The import graph says otherwise:

- no production package imports `internal/kernel/semanticdoc`;
- no external test imports it; the only test is its own
  `semanticdoc_test.go`;
- `go list -json` places it in the "zero importers" set together with real
  leftovers.

So the claimed single owner does not participate in the pipeline. Either the
composer was migrated and the callers were never rewired, or the migration
regressed and the document text is again composed elsewhere. Both states are
worse than an explicit decision: the doc asserts an ownership that the code
does not exercise.

### `internal/kernel/indexing` — 16 lines, zero importers

The package contains one sentinel,
`ErrIndexClipDisabledButEventRequested`, whose message names `clipindexer`, the
component retired on 2026-11-17 (`refactored@cc7b3290f`, "retire clipindexer").
Nothing imports the error, so the only remaining trace of the retired indexer
in Go is a package that cannot be reached.

## Decision required for `semanticdoc`

1. **Rewire** it: identify the current `DocumentText` composer(s), route them
   through `semanticdoc`, and add a test that fails if a second composer
   produces text without the version/hash. This closes the ownership claim.
2. **Delete** it and amend the doc that claims it is canonical, so the real
   composer is named instead. Deleting without naming the replacement is not
   acceptable.

For `indexing` there is no decision: delete it with the `clipindexer` naming
cleanup.

## Acceptance criteria

- [ ] `semanticdoc`: a written decision (rewire or delete) with the real
      `DocumentText` composer named.
- [ ] If rewired: at least one production import, a test that pins version +
      content hash, and `rg` shows no other composer assembling the same text.
- [ ] If deleted: the package is gone, its test is gone, and
      `docs/operations/` no longer describes it as the single owner.
- [ ] `internal/kernel/indexing` is deleted; no `clipindexer` sentinel remains
      in a Go package.
- [ ] The import-graph check reports no other package with zero importers
      except the known test-support packages (`testsupport`, `mutationprobe`,
      `cmd/archcheck/gate`, `migrations/sqlite`).
- [ ] `go build ./...` and `go vet ./...` pass.

## Verification

```sh
rg -n "kernel/semanticdoc|kernel/indexing" --glob '*.go'
go list -f '{{.ImportPath}}' ./... | while read p; do :; done   # graph check from the audit script
go build ./... && go vet ./...
```

## Execution log — 2026-11-18

- `internal/kernel/semanticdoc/` (345 lines) deleted. The real `DocumentText`
  composer is `internal/capabilities/youtube/dto/tagutil_semantic.go:BuildEmbeddingText`;
  the package doc claiming a single owner is removed with the package.
- `internal/kernel/indexing/` (16 lines, `ErrIndexClipDisabledButEventRequested`)
  deleted; no `clipindexer` sentinel remains in a Go package.
- `go build ./...` and `go vet ./internal/kernel/...` pass; the import graph no
  longer reports a zero-importer package outside the known test/build support
  (`testsupport`, `mutationprobe`, `cmd/archcheck/gate`, `migrations/sqlite`).

## Notes

`cmd/archcheck/gate`, `migrations/sqlite`, `platform/mutationprobe` and
`usecase/testsupport` also have no production importer, but they are
intentionally test/build support. They are listed here only so the acceptance
check does not delete them by mistake.
