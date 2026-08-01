# Verify-Main Refactor — Acceptance (July 2026)

The verification surface on `main` is split into six fail-closed gates:
`verify-fast` (foundation + static, ~30s), `verify-main` (headless daily
pre-push gate — foundation + static + unit-fast + changed-components +
verify-node-native + verify-architecture, with standard Go tests and no full
race/npm suite), `verify-race` (the explicit race-tested Go unit and
registered-component gate), `verify-full` (the complete headless
gate — `verify-main + verify-race + verify-node-tests`), `verify-release`
(the pre-deploy gate — `verify-full + verify-integration`), and
`verify-live` (the post-deploy gate, the only gate requiring the full
operational stack via `verify-{images,script,vidrush,artlist}-live`).
`verify-main`, `verify-race`, and `verify-full` do not require browser,
Drive, Qdrant, or live scraper services; `verify-release` adds the Go
integration suite but not the live batteries.

`verify-main` does NOT pull any script under `tests/operational/`,
so operators iterating on `/detail` or `/search` can run
`make verify-artlist-{startup,stream,search,download,pipeline,drive,index,
cache,errors}` to debug a single gate without paying the full battery
cost. The contract invariants are: (a) `verify-main` excludes
`tests/operational/artlist/`; (b) `verify-main`, `verify-race`, and
`verify-full` are headless (no browser/Drive/Qdrant live); (c)
`SMOKE_DRY_RUN=1 make verify-artlist-stream` completes as a single-gate
debug probe; (d) `verify-full = verify-main + verify-race +
verify-node-tests`; (e) `verify-release = verify-full +
verify-integration`; (f) `verify-live` is the sole gate pulling the four
live batteries.
