# PR-VO-A bundle — voiceover P0 hardening (June 2026)

Six sequential commits that close the canonical P0 voiceover risks
identified in the brutal care plan. Each commit is independently
revertable; **this document is the authoritative meta-index** for
"what A1..A6 do, collectively, when treated as one bundle".
See [ARCHITECTURE.md §6 Persistence](../../ARCHITECTURE.md#6-persistence)
and [AGENTS.md Git-Lesson-2](../../AGENTS.md#git-lesson-2-june-2026--direct-to-main-workflow)
for the direct-to-main workflow context.

## Per-commit index

| #       | Commit     | Subject                                                  | Files (scope)                                                                        |
| ------- | ---------- | -------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| A1      | `e149e1ab` | capture canonical TTS voice, drop `VoiceProfile`         | `internal/domain/voiceover/`, `internal/application/voiceover/`                     |
| A2      | `54165df5` | replace-safe atomic swap (staging → tx → cleanup)        | `internal/application/voiceover/`                                                    |
| A3      | `66cf597e` | outbox-based Qdrant indexing (tx-atomic enqueue)         | `internal/infrastructure/database/sqlite/outbox/`, `internal/application/voiceover/` |
| A4      | `52886556` | path-traversal guard on `SubfolderName` (segment + Rel lock) | `pkg/pathutil/`, `internal/application/voiceover/`                              |
| A5+A6   | `602114bc` | Promo accounting correctness + strict translator         | `internal/application/workflow/promo/`, `internal/application/voiceover/`            |

> **Audit-trail note**: row subjects are the **subject SUFFIX** taken
> from each commit's subject line, i.e. the content after stripping
> the `feat(voiceover): PR-VO-A<n> — ` prefix. Verify with
> `git show -s --format=%s <sha> | sed -E 's/^feat\([^)]+\): PR-VO-[A-Z0-9+]+ — //'`.
> Any drift between this table and the commit history is a
> PR-documentale inconsistency to fix, not a soft-update preference.

## Cumulative risk coverage

| Risk category                                                                       | Closed by          | Status     |
| ----------------------------------------------------------------------------------- | ------------------ | ---------- |
| Atomic state transitions (replace)                                                  | A2                 | ✅ closed  |
| Atomic handoff of metadata to non-SQLite derived-projection sinks (Qdrant; replaces legacy goroutine + crash window) | A3 | ✅ closed  |
| Implicit TTS voice identity                                                         | A1                 | ✅ closed  |
| Path traversal on user-supplied folder                                              | A4                 | ✅ closed  |
| Silent accounting failures                                                          | A5+A6              | ✅ closed  |
| False "all OK" responses                                                            | A5+A6              | ✅ closed  |

> A previous row listing "Non-atomic crash window between DB commit and
> external index update" has been merged into the Qdrant-handoff row
> above: both describe a single sequencing risk — the canonical SQLite
> tx commits before the Qdrant index updates, and the index handoff
> depended on a goroutine launched OUTSIDE the tx. A3 closes it once.

## Per-PR contract details

### A1 — Canonical TTS voice (`e149e1ab`)

Captures the canonical TTS voice name at the canonical infer point and
drops the `VoiceProfile` struct field that allowed field-name drift.

### A2 — Replace-safe atomic swap (`54165df5`)

`swapVoiceoverRow` now wraps staging → tx → cleanup in a single SQLite
transaction. The staging file is removed INSIDE the tx so a crash
mid-way leaves either the old row + new file (replace mode off) or the
new row + new file + no orphan staging file (replace on).

### A3 — Outbox-based Qdrant indexing (`66cf597e`)

Reverts the legacy `concurrent.SafeGoFunc("voiceover-indexing", ...)` goroutine
that ran BEFORE the SQLite tx committed. Replaces with a typed
`TxOutboxEnqueuer` port and a `Dispatcher.EnqueueIndexEvent(ctx, tx, assetID)`
helper called INSIDE the same tx. The outbox row and the voiceover row
now have identical commit fate, so the previous crash window
(DB-committed but Qdrant never indexed) is closed.

### A4 — Path-traversal guard on `SubfolderName` (`52886556`)

Added `pkg/pathutil.SanitizeSubfolderSegment(name)` (single-path-segment
sanitizer: rejects `.`/`..`, leading or embedded `/`/`\\`, NUL byte,
length>200 bytes) and `pkg/pathutil.EnsureWithinDir(root, path)` (post-join
`filepath.Rel` escape guard). `internal/application/voiceover/types.go`
gained `(d *DestinationRequest) Validate() error` delegating to the
sanitizer. Two-layer defense: fail-fast in `GenerateBatch`, defense-in-depth
in `resolveDestination` and `processLanguage`.

### A5+A6 — Promo accounting + strict translator (`602114bc`)

`Response.Total == len(targets)` (was `len(translations)`). `Response.Failed`
counts both translation AND voiceover failures. `Response.OK = (Failed == 0)`.
Per-language `Result.OK/Error` reflect actual outcome.

Translator failures now use typed sentinels:
`ErrTranslationFailed`, `ErrTranslationEmpty`, `ErrVoiceoverFailed`.
Default (strict) mode: translation failure always populates a `Result`
entry, increments `Failed`, flips `OK=false`. Lenient mode
(`AllowUntranslated=true`): LITERAL semantics — translation failure
silently drops `Failed`/Result/OK-flip (true opt-in allow). Voiceover
failure is NEVER gated by `AllowUntranslated` because by then the TTS
engine has already spent quota.

## Tests pinned by the bundle

| PR  | Test file                                                            | Cases |
| --- | -------------------------------------------------------------------- | ----- |
| A1  | domain/voiceover + application/voiceover                             | voice identity canonicalization |
| A2  | internal/application/voiceover/service_test.go                      | replace-tx atomicity, crash-window |
| A3  | internal/infrastructure/database/sqlite/outbox/*_test.go + voiceover service tests | tx-roundtrip, payload byte-identity |
| A4  | pkg/pathutil/pathutil_test.go + voiceover/types_test.go + service_test.go | SanitizeSubfolderSegment attack-vector coverage + GenerateBatch/Validate integration |
| A5+A6 | internal/application/workflow/promo/generate_test.go + voiceover/promopayload_test.go | 13 tests: strict + literal-lenient + dry-run + subset + sealed-sentinel prefix lock |

## Architectural patterns reaffirmed

1. **Direct-to-main, doc-only bundle (Git-Lesson-2)** — never rewrite history to
   bundle land commits; point readers to this meta-doc instead.
2. **AGENTS.md Pattern 0** — narrow Go-structural port with compile-time
   assertion (e.g. `TxOutboxEnqueuer` in `internal/infrastructure/database/sqlite/outbox`).
3. **AGENTS.md Pattern 8** — thin API transport: voiceover handler
   propagates `AllowUntranslated` from JSON wire straight to
   `promo.Request` with no business orchestration in the handler body.
4. **AGENTS.md godlike-07 (zero legacy)** — A2→A3 cutover is the canonical
   EXPAND/CUTOVER pair; the legacy goroutine-based indexing survives
   only as a documented compat shim, not as a parallel implementation.
5. **AGENTS.md godlike-06 (data/config ownership)** — A3 confirms that
   Qdrant projection is a derived view; the SQLite row commits first,
   the outbox row commits in the same tx, the Qdrant update is async
   + idempotent.

## Future P1/P2 work (post-bundle)

- **PR-VO-B1+B2** — Drive upload split (Processor=local, Lifecycle=upload) +
  metadata/group propagation through `processLanguage` and `resolveDestination`.
- **PR-VO-B3** — Sync dedupe by `drive_file_id` + locale regex parser
  (en_US support + case-insensitive lookup).
- **PR-VO-C1** — Unify `/api/voiceover/generate-with-group` into
  `/api/voiceover/generate` via `destination.kind = "group"` option;
  deprecate old endpoint with `Deprecation` + `Sunset` warning headers
  (RFC 8594) for 90-day transition window.

## References

- Canonical architecture: [`ARCHITECTURE.md`](../../ARCHITECTURE.md)
- Agent-facing rules: [`AGENTS.md`](../../AGENTS.md)
- Qdrant projection doctrine: [`docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#qdrant-projection`](../architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md)
- Zero legacy doctrine (verify path at HEAD): `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md`
