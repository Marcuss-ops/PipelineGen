
---

## PR-c-1 (sub-package extraction) — DEFERRED, July 2026

**Action taken this tornata:** reverted all attempted moves + sed rewire (working tree returned to HEAD-pinned clean state).

**Original premise that proved false:** the alleged `internal/artifacts -> internal/assets` cycle. In this codebase, neither `internal/artifacts/clips_adapter.go` nor `internal/artifacts/converters.go` imports `internal/assets` directly. The two-cycle-source files import:
- `internal/application/assets/mutations` (canonical dispatcher SSOT)
- `internal/domain/asset` (domain types)
- `internal/infrastructure/database/sqlite/assets` (infrastructure persistence)
None of these paths reach `internal/assets`. So the alleged transitive cycle was a misdiagnosis.

**What is still true architecturally:**
- `internal/application/assets/artifacts/` is mixed typed-domain + adapter-glue. The boundary types (`MediaRecord`, `FinalizeOptions`, `FinalizeResult`) live alongside the canonical domain types (`Status`, `Artifact`, `Repository`).
- The adapter-glue symbols (`ClipsRegistry`, `NewClipsRegistry`, `VoiceoverRecordToClip`, `ImageAssetToClip`) are defined together with the boundary types in the same package. The two concepts are mixed.
- Extracting the adapter-glue to `internal/application/assets/clipsadapter/` remains a valid code-organization move, independent of the cycle question.

**What got tried this tornata (all reverted):**
1. **Aggressive move** — move `clips_adapter.go`, `converters.go`, `finalizer.go`, `registry.go`, `service.go`, `local_blob.go`, `repository.go`, `verifier.go`, plus companion tests to `internal/application/assets/clipsadapter/`. Required moving `MediaRecord`/`FinalizeOptions`/`FinalizeResult` boundary types too. ~336 files modified. Build broke on bare-reference propagation (`VoiceoverRecordToClip`, `ImageAssetToClip`, `DriveVerifier`). Reverted.
2. **Minimal move (OPTION 2)** — move only `clips_adapter.go`, `converters.go` to `clipsadapter/`; keep boundary types in `artifacts`; qualify `MediaRecord` as `artifacts.MediaRecord` in moved file. After 2 incremental rounds, lapsed into same bare-reference issues (`source_resolver.go`, `dispatcher_fail_closed_test.go` bare referenced moved symbols; `DriveVerifier` undefined). Reverted.

**Recommended next path for PR-c-1 (single-tornata feasible):**
1. Decide on boundary-type ownership. Two valid resolutions:
   - **A:** Demote boundary types — move `MediaRecord`/`FinalizeOptions`/`FinalizeResult` to a third package (`internal/application/assets/mediarec`). Both `artifacts/` and `clipsadapter/` import from it.
   - **B:** Keep boundary types in `artifacts/`; have `clipsadapter` import `artifacts` (one-way, no cycle). `artifacts` does NOT import `clipsadapter`.
2. Move only `clips_adapter.go` + `converters.go` to `clipsadapter/` with OPTION-B qualification.
3. Run goimports -w on the moved files ONLY; do NOT run goimports on the broader repo (it auto-spreads qualifier-prefixes and creates the bare-reference chain errors).
4. Verify with `go build ./...` + `go test ./internal/application/assets/...`.
5. Single atomic commit + push to main per user rule.

**Lesson:** Sub-package extractions with cross-package type references require **explicit per-file import management**, not `goimports -w` on the whole repo. The global goimports run auto-applied `clipsadapter.X` prefix qualifiers in `artifacts/` files without adding the `clipsadapter` import, producing chain-error compilations.

---

## PR-c-3 (artifact-status fusion finalization) — VERIFIED ALREADY-DONE, July 2026

**Action taken this tornata:** zero code changes. Atomic doc-only commit certifying PR-c-3 close.

**Verifications:**
- `internal/assets/artifact.go` — DOES NOT EXIST. The entire `internal/assets/` package is gone (`find . -path '*/internal/assets*' -type d` returns no result). 0 importers of `github.com/Marcuss-ops/PipelineGen/internal/assets`.
- Bridge aliases `type X = assets.X` — ZERO occurrences in the codebase. The parity-asset.recyclerview migration to canonical sources completed by prior tornatas.
- Orphan symbols the user listed (Version, VersionRepository, Metadata, ProcessingStage) — already defined in `internal/domain/asset/{types_aux.go, store_helpers.go, asset_types.go, processor.go}` (1 definition each). They do not need aliases; their canonical location is correct.
- `Locations` — 0 definitions (no aliases needed; YAGNI to introduce).
- Build: green (`go build ./...` exit 0).

**What was implied vs what shipped:** The user's request implied a multi-file operation (git rm + doc-comment update + YAGNI aliases). All of these are no-ops on the current state. PR-c-3 is closed without code change.

**Why the user rule "no branches, commit + push directly to main, frequently" still wins:** an explicit close-commit documents the discovery + prevents future agents from re-attempting PR-c-3 against a different premise. The cost is one docs-only commit; the benefit is permanent closure of the Wave 12 follow-up FRAGMENTO (c) line item.
