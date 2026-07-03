#!/usr/bin/env python3
"""Wave 3 CONTRACT closure edits for P0 #3 audit 2026-07-03 — text-mode.

Uses Python's DEFAULT text-mode open() (no `newline='', no `'rb'`/`'wb'`).
On Windows, Python's text mode auto-translates CRLF bytes to LF strings
on read AND LF strings to CRLF bytes on write (via os.linesep logic).
This handles the file's CRLF encoding end-to-end with a single LF
anchor pattern, no manual `\n`↔`\r\n` toil required.
"""
import sys

# ── 1. Edit architecture/current.yaml: flip PR-SCRIPTING-CANONICAL-FRAMEWORK ──
PATH1 = 'architecture/current.yaml'
OLD_BLOCK = '    - id: PR-SCRIPTING-CANONICAL-FRAMEWORK\n      status: pending\n      owner_capability: internal/application/scripts\n      deadline: 2026-08-15'
NEW_BLOCK = '    - id: PR-SCRIPTING-CANONICAL-FRAMEWORK\n      status: shipped\n      owner_capability: internal/application/scripts\n      deadline: 2026-07-03'

with open(PATH1, 'r', encoding='utf-8') as f:
    content1 = f.read()
if OLD_BLOCK not in content1:
    print(f'ERROR: PR-SCRIPTING-CANONICAL-FRAMEWORK anchor not found in {PATH1}', file=sys.stderr)
    sys.exit(2)
if content1.count(OLD_BLOCK) != 1:
    print(f'ERROR: anchor matched {content1.count(OLD_BLOCK)} times (expected exactly 1)', file=sys.stderr)
    sys.exit(3)
content1_new = content1.replace(OLD_BLOCK, NEW_BLOCK)
with open(PATH1, 'w', encoding='utf-8') as f:
    f.write(content1_new)
print(f'OK: {PATH1} flipped status (pending->shipped) + deadline (2026-08-15->2026-07-03)')

# ── 2. Edit architecture/deprecations.yaml: insert P0-3-GENERATION-RESPONSE entry ──
PATH2 = 'architecture/deprecations.yaml'
ANCHOR = '  # \u2500\u2500 0. PR-VOICEOVER-LEGACY-ENDPOINTS-410 \u2014 voiceover typed-port seam HTTP 410 surface removed \u2500\u2500'
NEW_ENTRY = '''  # \u2500\u2500 17. P0-3-GENERATION-RESPONSE \u2014 internal/application/generation/response.go physical git-rm \u2500\u2500
  # Wave 3 CONTRACT of audit 2026-07-03 P0 #3 (Single canonical scripting framework,
  # July 2026). internal/application/scripts/apiutil is the canonical SSOT for the
  # wire envelope (Mode / ModeSync / ModeAsync / Response[T] / Sync[T] / Async[T])
  # per godlike/06 "one owner per fact". The legacy carrier
  # internal/application/generation/response.go is git-rm'd post-Wave-1-BACKFILL
  # migration of the 3 envelope callers (books/process, books/process_drive,
  # lessons/generate). Wave 2 CUTOVER's ErrDeprecatedWireEnvelope fail-closed
  # was deliberately skipped (user-jump from BACKFILL to CONTRACT): with a
  # planned git-rm the deprecation semantics are moot \u2014 the file physically
  # disappears and no consumer can reach it. Per godlike/07 \u00a7"Migration
  # sequence", zero pre-removal callers + canonical replacement present +
  # physical-removal target \u2192 EXPAND\u2192BACKFILL\u2192CONTRACT (skip CUTOVER).
  # \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500
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
      physically remove (git-rm) per godlike/07 \u00a7"No fake availability".
    introduction_date: 2026-05
    removal_date: 2026-07-03
    tracking_issue: "PR-SCRIPTING-CANONICAL-FRAMEWORK (linked in architecture/current.yaml#linked_issues[PR-SCRIPTING-CANONICAL-FRAMEWORK], status shipped / deadline 2026-07-03). Wave 0 EXPAND canonical home at commit 66b93a4f. Wave 1 BACKFILL 3-callsite migration (origin/main tip). Wave 3 CONTRACT this record."
    compatibility_test: |
      Five-layer defence for the git-rm shape:
        (a) `git ls-files internal/application/generation/response.go` returns 0.
        (b) `rg 'generation\\.Response\\b|generation\\.Sync\\b|generation\\.Async\\b'
            --type go internal/` returns zero hits post-Wave-1-BACKFILL migration.
        (c) `go build ./...` exits 0 post-removal.
        (d) `go vet ./internal/application/...` exits 0.
        (e) apiutil package continues to compile clean; Wave 0 EXPAND's
            byte-identical shape preserved through BACKFILL type-aliases
            and into CONTRACT physical removal.
    usage_metric: |
      Pre-Wave-3: 1 legacy file live + 0 production envelope-symbol callers.
      Post-Wave-3: 0 (whole-file git-rm). Net delta = -1 file git-rm'd,
      +0 canonical surface edits, +1 deprecation record.
    migration_phase: CONTRACT
    status: removed
    notes: |
      Single-pulse CONTRACT from BACKFILL per user-jump (audit 2026-07-03
      explicit decision). Per godlike/07 \u00a7"Migration sequence":
      zero pre-removal callers + canonical replacement + planned
      physical removal \u2192 direct EXPAND\u2192BACKFILL\u2192CONTRACT (skip CUTOVER).
      Wave 2 CUTOVER (`ErrDeprecatedWireEnvelope` fail-closed marker)
      was deliberately skipped: with a git-rm target, there is no
      one to fail-closed. The godlike/07 \u00a7"No fake availability" rule
      is satisfied because the canonical replacement (apiutil.*) already
      serves all 3 callers.

      Cross-refs:
        - godlike/06 \u00a7"One owner per fact" : apiutil owns the wire envelope.
        - godlike/07 \u00a7"No fake availability" : canonical surface continuous from Wave 0.
        - godlike/07 \u00a7"Migration sequence" : skip CUTOVER was valid.
        - AGENTS.md Pattern 0 : apiutil is the typed envelope surface.

      Sibling-record delineation:
        - registry_public_modules.go still imports generation for capability
          composition (Build/Dependencies/HandlerFunc), NOT envelope symbols.
          Forward-pointer: future wave may collapse the `generation` capability
          package; OUT OF SCOPE for this record.

'''

with open(PATH2, 'r', encoding='utf-8') as f:
    content2 = f.read()
if ANCHOR not in content2:
    print(f'ERROR: anchor not found in {PATH2}', file=sys.stderr)
    sys.exit(4)
if content2.count(ANCHOR) != 1:
    print(f'ERROR: anchor matched {content2.count(ANCHOR)} times (expected exactly 1)', file=sys.stderr)
    sys.exit(5)
content2_new = content2.replace(ANCHOR, NEW_ENTRY + ANCHOR)
with open(PATH2, 'w', encoding='utf-8') as f:
    f.write(content2_new)
print(f'OK: {PATH2} P0-3-GENERATION-RESPONSE entry inserted before "0. PR-VOICEOVER-LEGACY-ENDPOINTS-410"')

# ── 3. Verify both edits applied ──
with open(PATH1, 'r', encoding='utf-8') as f:
    verify1 = f.read()
assert 'PR-SCRIPTING-CANONICAL-FRAMEWORK\n      status: shipped' in verify1, "current.yaml verification FAILED"
assert OLD_BLOCK not in verify1, "old anchor still present in current.yaml"
print('OK: verification current.yaml passes (status shipped + new deadline)')

with open(PATH2, 'r', encoding='utf-8') as f:
    verify2 = f.read()
assert '- id: P0-3-GENERATION-RESPONSE' in verify2, "deprecations.yaml entry not found"
assert 'migration_phase: CONTRACT\n    status: removed' in verify2, "deprecations.yaml status: removed not found"
print('OK: verification deprecations.yaml passes (P0-3 entry + status: removed)')

# ── 4. Print byte-level line ending sanity check ──
import subprocess
print('---LINE-COUNTS-AFTER---')
print(subprocess.check_output(['wc', '-l', PATH1, PATH2]).decode())
# Quick CRLF preservation check
with open(PATH1, 'rb') as f:
    raw = f.read()
crlf_count = raw.count(b'\r\n')
print(f'current.yaml CRLF count: {crlf_count} (should be ~2088)')
with open(PATH2, 'rb') as f:
    raw = f.read()
crlf_count = raw.count(b'\r\n')
print(f'deprecations.yaml CRLF count: {crlf_count} (should be ~3300+)')
print('---DONE---')
