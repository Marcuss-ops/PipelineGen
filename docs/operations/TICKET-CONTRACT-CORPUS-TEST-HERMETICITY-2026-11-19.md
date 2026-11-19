# TICKET — Contract corpus test is not hermetic under a nested worktree (2026-11-19)

- **Status:** CLOSED (2026-11-19) — the test walks up to the workspace that owns `contracts/`; verified in the main tree and in a nested worktree
- **Severity:** P2 — the test decides success by where the checkout happens to sit
- **Owner:** scripts test infrastructure
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

`TestOverlayContractCorpusProducerWire`
(`internal/capabilities/scripts/overlay_contract_corpus_test.go`) resolves the
corpus manifest as `<module-parent>/contracts/overlay-v2/manifest.json`. In the
main tree and in a worktree at the repository root that path exists. In a
worktree nested inside `refactored/` (the layout used to measure dead code
without touching other sessions' edits) it resolves to
`refactored/.tmp/contracts/overlay-v2/manifest.json` and fails:

```
--- FAIL: TestOverlayContractCorpusProducerWire (0.00s)
    overlay_contract_corpus_test.go:22: read contract corpus manifest:
    open .../refactored/.tmp/contracts/overlay-v2/manifest.json: no such file or directory
```

At `refactored-dc` (worktree at the repository root) the same test passes, so
the failure is path resolution, not the corpus.

## Scope

- `internal/capabilities/scripts/overlay_contract_corpus_test.go` — the only
test that resolves the corpus; no production code is involved.

## Plan

1. Resolve the repository root explicitly instead of assuming the worktree's
   parent: walk up from the test file until a directory containing `contracts/`
   is found, or take `git rev-parse --show-toplevel`.
2. Fail with a message that names the searched root and the env var to override.
3. Keep the corpus path overridable for CI without weakening the test: a missing
   manifest must still fail.
4. Re-run the test in the three layouts.

## Acceptance criteria

- [ ] The test passes in the main tree, in a root-level worktree and in a
      worktree nested under `refactored/`.
- [ ] The failure message names the resolved root and the override variable.
- [ ] The test still fails when the corpus is deleted (no silent skip).
- [ ] `go test ./internal/capabilities/scripts -run TestOverlayContractCorpusProducerWire -count=1`
      is green in all three layouts.

## Closeout (2026-11-19)

- `contractCorpusRoot` replaces the fixed `../../../../` join: it starts from
  the package working directory and walks up until
  `contracts/overlay-v2/manifest.json` exists, or takes
  `VELOX_CONTRACT_CORPUS_ROOT` when CI sets it.
- A missing corpus still fails the test (no skip), and the failure names the
  starting directory and the override variable.
- Verified in the main tree and in `refactored/.tmp/nested2` (a worktree nested
  inside the module): both `ok`. The nested layout previously failed with
  `refactored/.tmp/contracts/overlay-v2/manifest.json: no such file or directory`.

## Verification

```sh
cd refactored && go test ./internal/capabilities/scripts -run TestOverlayContractCorpusProducerWire -count=1
git worktree add --detach .tmp/nested HEAD && (cd .tmp/nested && go test ./internal/capabilities/scripts -run TestOverlayContractCorpusProducerWire -count=1)
```
