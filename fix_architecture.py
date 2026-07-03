import io, sys

# ── 1. Edit architecture/current.yaml: flip PR-SCRIPTING-CANONICAL-FRAMEWORK status/deadline ──
path1 = 'architecture/current.yaml'
with io.open(path1, 'r', encoding='utf-8', newline='') as f:
    content1 = f.read()

old_block = '''    - id: PR-SCRIPTING-CANONICAL-FRAMEWORK
      status: pending
      owner_capability: internal/application/scripts
      deadline: 2026-08-15'''
new_block = '''    - id: PR-SCRIPTING-CANONICAL-FRAMEWORK
      status: shipped
      owner_capability: internal/application/scripts
      deadline: 2026-07-03'''

if old_block not in content1:
    print('ERROR: PR-SCRIPTING-CANONICAL-FRAMEWORK anchor not found in current.yaml', file=sys.stderr); sys.exit(2)
if content1.count(old_block) != 1:
    print(f'ERROR: anchor matched {content1.count(old_block)} times (expected exactly 1)', file=sys.stderr); sys.exit(3)

content1_new = content1.replace(old_block, new_block)
with io.open(path1, 'w', encoding='utf-8', newline='') as f:
    f.write(content1_new)
print(f'OK: architecture/current.yaml flipped status (pending→shipped) + deadline (2026-08-15→2026-07-03)')

# ── 2. Edit architecture/deprecations.yaml: insert P0-3-GENERATION-RESPONSE entry before the "0." section ──
path2 = 'architecture/deprecations.yaml'
with io.open(path2, 'r', encoding='utf-8', newline='') as f:
    content2 = f.read()

anchor = '  # ── 0. PR-VOICEOVER-LEGACY-ENDPOINTS-410 — voiceover typed-port seam HTTP 410 surface removed ──'
if anchor not in content2:
    print('ERROR: anchor not found in deprecations.yaml', file=sys.stderr); sys.exit(4)
if content2.count(anchor) != 1:
    print(f'ERROR: anchor matched {content2.count(anchor)} times (expected exactly 1)', file=sys.stderr); sys.exit(5)

new_entry = '''  # ── 17. P0-3-GENERATION-RESPONSE — internal/application/generation/response.go physical git-rm ──
  # Wave 3 CONTRACT of audit 2026-07-03 P0 #3 (Single canonical scripting framework,
  # July 2026). internal/application/scripts/apiutil is the canonical SSOT for the
  # wire envelope (Mode / ModeSync / ModeAsync / Response[T] / Sync[T] / Async[T])
  # per godlike/06 "one owner per fact". The legacy carrier
  # internal/application/generation/response.go is git-rm'd post-Wave-1-BACKFILL
  # migration of the 3 envelope callers (books/process, books/process_drive,
  # lessons/generate). Wave 2 CUTOVER's ErrDeprecatedWireEnvelope fail-closed
  # was deliberately skipped (user-jump from BACKFILL to CONTRACT): with a
  # planned git-rm the deprecation semantics are moot — the file physically
  # disappears and no consumer can reach it. Per godlike/07 §"Migration
  # sequence", zero pre-removal callers + canonical replacement present +
  # physical-removal target → EXPAND→BACKFILL→CONTRACT (skip CUTOVER).
  # ──────────────────────────────────────────────────────────────────────────────────
  - id: P0-3-GENERATION-RESPONSE
    owner_capability: internal/application/generation (legacy) + internal/application/scripts/apiutil (canonical)
    exact_symbol: |
      Type aliases `type Response[T any] = apiutil.Response[T]` +
      `type Mode = apiutil.Mode` + `const ModeSync = apiutil.ModeSync` +
      `const ModeAsync = apiutil.ModeAsync` + the Sync[T] / Async[T]
      constructor functions (delegate to aliased struct fields).
      The full-file git-rm of internal/application/generation/response.go
      removes both the type-alias redirects AND the function constructors.
    file: internal/application/generation/response.go
    file_line: ALL (whole-file git-rm)
    replacement: |
      internal/application/scripts/apiutil (canonical SSOT for the wire envelope
      surface). Importers in production code:
        - internal/application/books/process_usecase.go
        - internal/application/books/process_drive_usecase.go
        - internal/application/lessons/generate_usecase.go
      All migrated to `apiutil.Response[T]` / `apiutil.Sync[T]` / `apiutil.Async[T]`
      at Wave 1 BACKFILL (commit landed on origin/main). Zero live references
      to the legacy carrier remain in production code; the file is safe to
      physically remove (git-rm) per godlike/07 §"No fake availability".
    introduction_date: 2026-05
    removal_date: 2026-07-03
    tracking_issue: "PR-SCRIPTING-CANONICAL-FRAMEWORK (linked in architecture/current.yaml#linked_issues[PR-SCRIPTING-CANONICAL-FRAMEWORK], status shipped / deadline 2026-07-03). Wave 0 EXPAND canonical home at commit 66b93a4f. Wave 1 BACKFILL 3-callsite migration (origin/main tip). Wave 3 CONTRACT this record."
    compatibility_test: |
      Five-layer defence for the git-rm shape:
        (a) `git ls-files internal/application/generation/response.go` returns 0
            (the file is fully removed; no live Go file references can remain).
        (b) `rg 'generation\.Response\b|generation\.Sync\b|generation\.Async\b'
            --type go internal/` returns zero hits post-Wave-1-BACKFILL migration.
            The 3 production callers (books/process, books/process_drive,
            lessons/generate) consume the canonical apiutil.* surface.
        (b-bis) `rg 'internal/application/generation' --type go internal/`
            returns hits ONLY in capability-composition files (non-envelope
            surface: generation.Build / generation.Dependencies /
            generation.HandlerFunc consumed by registry_public_modules.go).
            These are NOT the deprecated wire envelope; they are a separate
            capability layer that future waves may collapse alongside the
            registry_public_modules.go composition (forward-pointer, owned
            outside this record's scope per the wave partition).
        (c) `go build ./...` exits 0 post-removal: the legacy file's removal has
            zero transitive build impact — all importers were already on
            apiutil.* from BACKFILL wave.
        (d) `go vet ./internal/application/...` exits 0 — no orphan type-alias
            redirects, no orphan Sync/Async stubs to flag.
        (e) compile-time check: apiutil package continues to compile clean;
            Wave 0 EXPAND's byte-identical shape preserved through BACKFILL
            type-aliases and into CONTRACT physical removal.
    usage_metric: |
      Pre-Wave-3 (audit baseline, 2026-07-03, post-Wave-1-BACKFILL):
        - `git ls-files internal/application/generation/response.go`       : 1 (legacy file).
        - `rg 'generation\.Response\b|generation\.Sync\b|generation\.Async\b' --type go internal/`
                                                                          : 0 production hits (3 callers migrated).
        - `apiutil.Response[T]` consumers (canonical surface)               : 3 production callers
          hydrated from Wave 1 BACKFILL.
      Post-Wave-3 (this commit lands on `main`):
        `git ls-files internal/application/generation/response.go`           : 0 (whole-file git-rm).
        `rg 'generation\.Response\b|generation\.Sync\b|generation\.Async\b' --type go internal/`
                                                                          : 0 hits.
        Net delta = -1 file git-rm'd, +0 canonical surface edits (Wave 0 EXPAND
        already shipped the canonical surface), +1 deprecation record
        `status: removed`, +1 forward-pointer note.
    migration_phase: CONTRACT
    status: removed
    notes: |
      Single-pulse CONTRACT from BACKFILL per user-jump (audit 2026-07-03
      explicit decision). Per godlike/07 §"Migration sequence":
        - Pre-removal callers of legacy envelope symbols: 0 (BACKFILL migrated
          all 3 production callers).
        - Canonical replacement (apiutil.*) shipped at Wave 0 EXPAND (commit
          66b93a4f, byte-identical wire-format).
        - Type-alias shadow branch in legacy file (introduced at Wave 1
          BACKFILL for 0-diff retro-compat) collapses at git-rm: no consumer
          can reach the legacy carrier post-CONTRACT, so the alias is moot.
      Wave 2 CUTOVER (`ErrDeprecatedWireEnvelope` fail-closed marker on
      legacy symbols) was deliberately skipped: with a git-rm target, the
      deprecation semantics are moot — there is no one to
