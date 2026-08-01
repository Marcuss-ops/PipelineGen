# Verify-Main Refactor — Acceptance (July 2026)

The pre-push verification chain on `main` has been split into four
fail-closed tiers: `verify-fast` (foundation + static, ~30s),
`verify-main` (headless daily gate — `verify-push + verify-node-native +
verify-architecture`, with standard Go tests and no full race/npm suite;
no browser/Drive/Qdrant live), `verify-race` (explicit race-tested Go
packages), `verify-full` (verify-main + verify-race + verify-node-tests),
`verify-release` (pre-deploy, = `verify-full + verify-integration` ONLY),
and `verify-live` (post-deploy, the ONLY gate requiring the
full operational stack via `verify-{images,script,vidrush,artlist}-live`).
`verify-main` does NOT pull any script under `tests/operational/`,
so operators iterating on `/detail` or `/search` can run
`make verify-artlist-{startup,stream,search,download,pipeline,drive,index,
cache,errors}` to debug a single gate without paying the full battery
cost. All five invariants validated on `main`: (a) `verify-main` excludes
`tests/operational/artlist/`; (b) `verify-main` is headless
(no browser/Drive/Qdrant live); (c) `SMOKE_DRY_RUN=1 make
verify-artlist-stream` completes as a single-gate debug probe;
(d) `verify-release = verify-main + verify-integration` ONLY;
(e) `verify-live` is the sole gate pulling the four live batteries.
