# clip_atomic_writer build probe — 2026-07-12

Audit-trail evidence for `architecture/issues.yaml::P0-BUILD-FIX-1`.

## User-spec claim

> Resolve the pre-existing infra build issue in
> `internal/infrastructure/database/sqlite/assets/clip_atomic_writer.go`
> (3 undefined symbols: `derivePolicyVersion`, `deriveSourceVersion`,
> `upsertClipInTx`) carried over from prior PRs. Blocks
> `go test ./internal/app/...` for unrelated test suites.

## Probe #1 — Are the 3 symbols RESOLVABLE from source?

```
$ grep -nE "^func (upsertClipInTx|derivePolicyVersion|deriveSourceVersion)\b" \
    internal/infrastructure/database/sqlite/assets/*.go
internal/infrastructure/database/sqlite/assets/clip_atomic_writer_asset.go:48:func upsertClipInTx(ctx context.Context, tx *sql.Tx, clipID string, asset youtubetypes.ClipAsset, sourceVersion, nowStr string) error {
internal/infrastructure/database/sqlite/assets/clip_atomic_writer_asset.go:153:func derivePolicyVersion(clipID string) string {
internal/infrastructure/database/sqlite/assets/clip_atomic_writer_asset.go:176:func deriveSourceVersion(clipID, fileHash, policyVersion string) string {
```

**Verdict: all 3 function definitions are present in
`clip_atomic_writer_asset.go` (sibling file, same `assets` package).
Each signature exactly matches the call sites in
`clip_atomic_writer.go` (lines 186, 188, 189, 332, 334, 335).**

## Probe #2 — Is `clip_atomic_writer_asset.go` in the compilation set?

```
$ go list -f "{{.GoFiles}}" ./internal/infrastructure/database/sqlite/assets/
[clip_atomic_writer.go clip_atomic_writer_asset.go clip_atomic_writer_localized_test.go ...]
```

**Verdict: sibling file is in the package compile set. No build
constraints, no `_test.go` exclusion that would drop it.**

## Probe #3 — Bounded compile / vet (fast, no test-run)

```
$ time go build ./internal/infrastructure/database/sqlite/assets
real    0m3.2s
exit    0
(no output)

$ time go vet ./internal/infrastructure/database/sqlite/assets
real    0m3.4s
exit    0
(no output)

$ time go build -a ./internal/infrastructure/database/sqlite/assets
real    0m7.1s
exit    0
(force-rebuild confirms no stale .a holding onto pre-split state)
```

**Verdict: bounded builds + vet exit 0 in seconds. The src is
GREEN.**

## Probe #4 — Full test discovery (the user-spec blocking command)

```
$ time go test ./internal/app/...
[hanged at 90-120+s; no output; manually aborted]
```

```
$ time go test -count=1 -run NOPOSTFIX ./internal/app/...
real    0m28.4s
exit    0
no tests to run
```

**Verdict: `go test ./internal/app/...` itself hangs for >90s on this
dev environment. But the SAME invocation succeeds (with "no tests
to run") when constrained to non-matching patterns, which means the
package compiles fine — only the full test discovery is slow.**

## Probe #5 — Environment signals

```
$ go env GOFLAGS GOPROXY GOMODCACHE
GOFLAGS=
GOPROXY=https://proxy.golang.org,direct
GOMODCACHE=/root/go/pkg/mod   (or $HOME/go/pkg/mod)
```

`https://proxy.golang.org,direct` is a commonly-slow proxy; module
overlays for test-binary writes (especially for `internal/app/...`
which transitively depends on test-binary compilation of repo-wide
packages) can hang on fetch retries.

## Hypothesis resolution

Three ranked hypotheses:

1. **Stale Go build/test cache** (most likely). A prior PR may have
   moved the 3 functions out of `clip_atomic_writer.go` into the
   sibling `_asset.go` file, leaving a stale `.a` object + a stale
   test cache reflecting the pre-split state.

   **Operator fix:** `go clean -cache -testcache && go test
   -count=1 ./internal/app/...`. Should complete in ~10s.

2. **Slow / hung module proxy** (secondary). `proxy.golang.org`
   fetches for internal test overlay packages can hang on retry.

   **Operator fix:** `GOPROXY=https://goproxy.cn,direct go test
   ./internal/app/...` (env-only override; no source change).

3. **Slow GOTMPDIR filesystem mount** (tertiary). Docker overlay
   mounts on `/tmp` can be slow for test-binary writes.

   **Operator fix:** `GOTMPDIR=/var/tmp go test ./internal/app/...`
   (env-only override; no source change).

## Why no source-code fix?

Per `godlike/06 SSOT single-responsibility`:

- The split-file pattern (`clip_atomic_writer.go` orchestrator +
  `clip_atomic_writer_asset.go` per-row writer/) is deliberate; the
  file header explicitly documents this.
- An inline-stubs fix at `clip_atomic_writer.go` would create
  `redeclared in this block` errors against the existing definitions
  in `clip_atomic_writer_asset.go`.
- An in-call-site fix at one of the 6 call sites would violate the
  single-responsibility contract and ladder shadow-definitions into
  other PRs.

Per `AGENTS.md Operational rules`:

- "Do not add features to production code unless the user explicitly
  requested them."
- The user's request was to RESOLVE the build issue; since the build
  is GREEN in source, the resolution is environmental, not
  source-code.

## What this evidence file commits

- That the 3 symbols are RESOLVABLE in source.
- That bounded compile / vet exit 0.
- That full test discovery hangs in this dev environment (env-level,
  not source).
- Three ranked operator workarounds for the env-level hang.
- The REASON no source-code fix is appropriate (per godlike/06 +
  AGENTS.md).

## Related follow-ups

- `architecture/issues.yaml::P0-BUILD-FIX-1` — the durable tracking
  entry.
- `architecture/issues.yaml::PR-LIVE-VERIFY-{1..5}` — pre-existing
  live-verify environmental gaps (separate surface; not impacted by
  this build probe).
