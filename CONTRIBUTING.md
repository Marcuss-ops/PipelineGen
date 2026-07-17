# Contributing to PipelineGen

This document captures git-workflow patterns that **complement** (but never
override) [`AGENTS.md`](AGENTS.md). Scope division per godlike/06 SSOT
(one canonical owner per fact):

- **`AGENTS.md`** governs the **workflow-level policy** (canonical): `never
  force-push by default`, `work directly on main`, `make verify-main
  before pushing`, etc.
- **`CONTRIBUTING.md` is canonical for the pattern-level implementation**
  within its scope: the `git commit --fixup=<SHA>` immediate remediation
  + the autosquash + `--force-with-lease` opt-in bundling
  (user-explicit consent, see §"Bug-fix pattern: `git commit --fixup=<SHA>`"
  below).

For non-negotiable workflow rules, see `AGENTS.md` §"Git workflow" + §"Operational rules".
For the document-to-fact ownership map, see [`CANONICAL.md`](CANONICAL.md).

If any prose here conflicts with `AGENTS.md`, `AGENTS.md` wins per
`CANONICAL.md` §"Conflict rule".

## Quick start

```bash
# 1. Read the canonical rules first.
cat AGENTS.md CANONICAL.md

# 2. Local sanity (per AGENTS.md "Run make verify-main before pushing").
make verify-main

# 3. Work directly on main (default workflow).
git fetch origin
git rebase origin/main

# 4. Push without force (per AGENTS.md "never force-push" by default).
git push origin main

# 5. After every push, confirm the commit landed.
git log -n 5 --oneline && git ls-remote origin main
```

`make install-hooks` once per fresh clone to enable the pre-commit
fail-closed gate.

## Git workflow

The non-negotiable rules live in [`AGENTS.md`](AGENTS.md) §"Git workflow":

- Work directly on `main`. No feature branches, no PRs for routine
  repository work.
- Before push: `git fetch` + `git rebase origin/main`.
- **Never force-push by default.** Push directly to `main` with `git
  push origin main`.
- After push: confirm via `git log -n 5 --oneline` + `git ls-remote
  origin main`.

`CONTRIBUTING.md` does NOT relax these rules. It adds ONE opt-in mechanism
for cleanly bundling a fix with its broken commit (below).

## Bug-fix pattern: `git commit --fixup=<SHA>`

When you detect a broken commit already on `origin/main` (e.g., post-push
`go build` failure, missing `Test` prefix on a test func, orphan-body
regression after a script-based split, `-checkout --ours` reversed during
rebase), use the canonical fixup pattern to mark intent to bundle a fix
with the broken commit. **This is the immediate default remediation —
no force-push required.**

```bash
# 1. Identify the broken commit on origin/main: <BROKEN_SHA>.
# 2. Apply your fix on top of the broken commit. The broken tree is
#    already your working tree — just edit the files in place; do NOT
#    `git checkout <BROKEN_SHA> -- <files>` (which would clobber your
#    in-progress edits).
# ...make the necessary fix changes...
# 3. Mark intent: produce a `fixup! <subject>` commit referencing <BROKEN_SHA>.
git add <touched-files>
git commit --fixup=<BROKEN_SHA>
# 4. Push the fixup! commit normally (no force-push required).
git push origin main
```

The `--fixup` commit lands as a NORMAL commit on main. It is structurally
benign until the user opts in to bundle (see next section). The default
post-state — two sibling commits on `origin/main` — is AGENTS.md-compliant
and matches the precedent from the post-push fixes of 2026-07
(`fix(qdrant): correct parent boundaries post-Step-3 trim orphan bodies`,
`fix(qdrant): repair Step-4 post-rebase resolution parent ...`, etc.).

### Opt-in: bundle via autosquash + `--force-with-lease`

**The opt-in gate is user-explicit consent.** A human user must
explicitly say "yes, bundle this" — auto-agents MUST NOT default to
`--force-with-lease`. When the user (or a user-explicit consent record)
authorizes the bundling, opt in:

```bash
# Bundle the fixup! into the broken commit (local rewrite; no remote touch).
GIT_SEQUENCE_EDITOR=true git rebase --interactive --autosquash <upstream>

# Push the rewrite EXPLICITLY (user-explicit consent recorded above).
git push --force-with-lease origin main
```

**Safety:** `--force-with-lease` (NOT `--force`) refuses to overwrite the
remote if it has moved on since you last fetched. Confirms you're not
killing concurrent work.

**Default is still no-force-push:** use opt-in ONLY when the bundling is
the explicit user-requested outcome (per user-consent gate above). The
default keeps history traceable, without public-history rewrite, and is
AGENTS.md-compliant. The opt-in cost is fewer commits + one
`--force-with-lease` push.

## Process-gap tripwires (do NOT repeat)

These patterns caused post-push broken commits in 2026; each is recorded
in `architecture/deprecations/` (the sharded records directory per
`architecture/deprecations/index.yaml`) + `architecture/issues.yaml`:

1. **Python / regex split scripts** — orphan function bodies at the file
   scope. Use a SYMBOL-AWARE tool (`ast-grep v0.44.1+` from npm
   `@ast-grep/cli`, or `gopls` symbols). NEVER regex-based signature-only
   removal.
2. **`sed`-based signature strip without matching body-end** — leaves
   SIG-only removal. Use literal-line removal via `str_replace` tools,
   copying verbatim from a prior `read_files` capture.
3. **Missing `Test` prefix on test funcs** — `go test ./...` will not run
   them. The pre-commit hook `scripts/hooks/pre-commit` (gates
   `^func Test` discipline in touched `_test.go` files) catches this.
   Install via `make install-hooks`.
4. **`git rebase --continue` after `git checkout --ours` confusion** —
   in REBASE (not merge), `--ours` is the UPSTREAM commit; `--theirs` is
   the commit being applied. They are REVERSED relative to `git merge`
   semantics. Always `cat <file>` and run `go build ...` BEFORE
   `git rebase --continue`; if the wrong side was kept, abort and start
   over with `git checkout --theirs`.
5. **Force-pushing without `--force-with-lease`** — kills concurrent
   work. AGENTS.md rule. Use `--force-with-lease` **only with explicit
   user consent** (per §"Opt-in: bundle via autosquash" user-consent gate)
   for fixup! autosquash bundling — never default-apply as an autonomous
   agent.

When you encounter any of these, the **immediate remediation** is `git
commit --fixup=<SHA>` (above) — NOT a separate orphan `--fix` commit.
The fixup marks intent; the autosquash bundling is the user-explicit
follow-up.

## CI gates + hooks (canonical, fail-closed)

- **`scripts/hooks/pre-commit`** — fail-closed gate that runs `go build`,
  `go vet`, and `_test.go` test-prefix discipline on touched packages.
  Hook path is canonical and version-controlled at `scripts/hooks/pre-commit`;
  install via `make install-hooks` (registers `core.hooksPath = scripts/hooks`).
  Bypass with `PIPELINEGEN_SKIP_PRECOMMIT=1` (loud-on-stdout, paired with
  immediate follow-up `fixup!` autosquash).
- **`scripts/ci-architectural-checks.sh`** — broad architectural gate
  suite. Some pre-existing findings may surface unrelated to the current
  edit; cross-reference before fixing.
- **`cmd/archcheck`** — produces the per-run structural report
  (`scripts/archcheck/current_report.json`; gitignored).
- **`scripts/archcheck/deprecations_validator.go`** — hard-fails on
  duplicate IDs across `architecture/deprecations/records/*.yaml`
  (cross-shard via `architecture/deprecations/index.yaml`; the
  validator's default path is the sharded directory, not the
  now-removed single file), expired `removal_date` for non-removed
  records, missing required keys per record.

For audit-pin migration notes (e.g., test funcs renamed or moved across
test-split boundaries): `grep -c <old-symbol> architecture/deprecations/records/<bucket>.yaml`
MUST return ≥1 hit so the rename is grep-discoverable. The canonical
retro-fix pattern appends a `← MIGRATED (PR-STEP-NN, commit <SHA>) →`
annotation block in the relevant YAML field (see
`architecture/deprecations/records/voiceover.yaml:52-55` — the (d) audit-pin block
for `TestParentAggregator_TriggeredOnlyAfterWaitingChildren` (parent aggregator
eligibility gate, 3 sub-cases: waiting_children → process / succeeded → no-op /
cancelled → no-op) — for the canonical example spanning the
voiceover/jobs/* Step 5 split migration of `TestAcceptance_CancelParent_AggregatorSkips`
into `parent_aggregator_eligibility_test.go::TestParentEligibility_TriggeredOnlyAfterWaitingChildren::t.Run("C. cancelled → aggregator skips")`).

## When in doubt

1. Read [`AGENTS.md`](AGENTS.md) (§"Git workflow" + §"Operational rules" +
   §"Documentation rule").
2. Read [`CANONICAL.md`](CANONICAL.md) (which doc owns which fact; the
   "Conflict rule" precedence).
3. Run `make verify-main` before pushing.
4. If your change is a test-split sibling pattern, copy the file VERBATIM
   from the parent source via `read_files` (no shell/regex heuristics) —
   verify with `ast-grep run --pattern 'func $X($$$ARGS) $_ { $$$BODY }'
   --lang go --json` post-write for census.
5. If you've authored a broken commit already on `origin/main`, the
   immediate remediation is `git commit --fixup=<SHA>` (NOT a separate
   orphan `--fix` commit).
6. If you do not have a `Test`-prefixed func in a `_test.go` file, the
   pre-commit hook will block the commit — fix the prefix BEFORE
   continuing.

For docs: prefer adding to the canonical location. If unsure which doc, see
[`CANONICAL.md`](CANONICAL.md) §"Canonical sources" table.

For ops: see `docs/operations/` for canonical runbooks. `README.md` only
links to them.

## Forward-pointer (forthcoming AGENTS.md amendment)

`AGENTS.md` §"Git workflow" will be amended in a separate PR to
explicitly acknowledge the `fixup!` + autosquash + `--force-with-lease`
opt-in pattern described above. Until that amendment lands, this
document is the canonical source for the pattern itself (per the
godlike/06 SSOT scope division at the document head).

A reader checking `AGENTS.md` for the rule should be cross-linked here
via `see CONTRIBUTING.md §"Bug-fix pattern: git commit --fixup=<SHA>"`;
that amendment is the canonical bridge between the two docs.
