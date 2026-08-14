# Component verification

This is the operator guide for the registry-driven verification system. The
executable sources of truth are:

- `config/verify-components.json` — component paths, Go packages,
  dependencies, timeout budgets, race policy, and optional test commands.
- `config/verify-pipelines.json` — aggregate pipeline component sets and
  optional operational commands.
- `scripts/ci/verify-component.py` — shared component runner.
- `scripts/ci/verify-all-components.py` — fast/race all-component entry point.
- `scripts/ci/verify-pipeline.py` — aggregate pipeline runner.
- `scripts/ci/verify-changed-components.py` — changed-file ownership and
  impacted-component selection.
- `make/verify.components.mk` — thin Make aliases.
- `make/verify.mk` — aggregate gate composition.
- `scripts/hooks/pre-push` — fail-closed push boundary.

Do not duplicate component ownership or command logic in individual Make
recipes. Add or adjust registry data and keep the Make targets declarative.

## Component targets

Each component target delegates to the shared runner. Dependencies are resolved
in dependency-first order and shared commands are executed once per runner
invocation.

| Target | Registry component | Scope |
|---|---|---|
| `make verify-script` | `script` | Script domain, application, and API |
| `make test-stock-component` | `stock` | Stock providers, pipeline, and API diagnostic component runner |
| `make verify-clips` | `clips` | Clip domain, application, and API |
| `make verify-drive` | `drive` | Drive domain and infrastructure |
| `make verify-research` | `research` | Research resolver, web fetcher, and research persistence |
| `make verify-qdrant` | `qdrant` | Qdrant domain, application, and infrastructure |
| `make verify-indexing` | `indexing` | Indexing application and infrastructure |
| `make verify-docs` | `docs` | Document generation, document APIs, and script-docs |
| `make verify-voiceover` | `voiceover` | Voiceover domain, application, and API |
| `make verify-database` | `database` | SQLite/database infrastructure and adapters |
| `make verify-jobs` | `jobs` | Job application, API, and SQLite job persistence |
| `make verify-images` | `images` | Image domain, application, and API |
| `make verify-translation` | `translation` | Translation services and script adapters |
| `make verify-timeline` | `timeline` | Media/timeline processing and storage API |
| `make verify-storage` | `storage` | Storage application, API, and Drive storage adapter |
| `make verify-api` | `api` | HTTP middleware and transport contracts |
| `make verify-ollama` | `ollama` | Ollama client and structured-output adapters |
| `make verify-youtube` | `youtube` | YouTube domain, sourcing, providers, API, and infrastructure |
| `make verify-artlist` | `artlist` | Artlist provider, infrastructure, and API |
| `make verify-node-scraper` | `node-scraper` | Node scraper tests and scraper integration package |

The registry also exposes aggregate component targets:

```bash
make verify-components       # all registered components, fast mode
make verify-race-components  # all registered components, race mode
make verify-changed-components
make verify-race-stock       # race for one component
make verify-race-qdrant

# aggregate pipelines
make test-pipeline-stock-only
make verify-pipeline-clip-only
make verify-pipeline-research
make verify-pipeline-document
make verify-pipeline-voiceover
make verify-pipeline-script
```

`verify-changed-components` maps committed, staged, unstaged, and untracked
non-ignored files to registry paths. Dependencies are added by the shared
runner. An unmapped file fails closed when the script is called directly. The
Make aggregate opts into `--run-all-when-unmapped`, which verifies every
currently registered component while preserving the unmapped-file information
in the report until registry coverage is expanded.

## Modes and gates

The component runner defaults to `fast` mode. `--race` selects race mode for
components whose registry entry has `race_enabled: true`.

Direct runner examples:

```bash
python3 scripts/ci/verify-component.py research
python3 scripts/ci/verify-component.py qdrant --race
python3 scripts/ci/verify-component.py --all --dry-run
python3 scripts/ci/verify-changed-components.py --dry-run
python3 scripts/ci/verify-changed-components.py --race --report /tmp/changed.json
```

The aggregate gates are intentionally separate:

| Gate | Composition | Use |
|---|---|---|
| `make verify-fast` | Foundation + static checks | Fast development loop |
| `make verify-main` | Foundation + static + changed components + architecture | Normal fail-closed pre-push gate |
| `make verify-race` | Foundation + all registered components in race mode | Explicit concurrency/race validation |
| `make verify-full` | `verify-main` + `verify-race` + full Node tests | Complete headless verification |
| `make verify-release` | `verify-full` + integration tests | Pre-deploy certification |

Foundation and shared prerequisites are Make dependencies, not recipes copied
into component targets. GNU Make executes a prerequisite once within an
aggregate invocation; component targets never call `verify-fast`.

Live operational batteries such as `make verify-live` and the individual
`*-live` targets are separate from the headless component gates. They may
require the server, Drive, Qdrant, Chrome, scraper, or authenticated
credentials. Use `scripts/with-velox-auth` for the canonical token boundary;
never print, hard-code, or source the token from a repository-local file.

## Timeouts and failure behavior

Each registry component has a positive `timeout_seconds` budget. The runner:

1. applies the component deadline to its commands;
2. terminates timed-out subprocess process groups;
3. marks the component `TIMEOUT`;
4. returns exit code `124` for timeout failures; and
5. prints a diagnostic such as:

```text
VERIFY_COMPONENT_TIMEOUT component=qdrant duration=600s
```

Other command failures return a non-zero exit code. Failed dependencies block
dependent components, and no failure is converted into a successful no-op.
Live tests are skipped unless `--include-live` is explicitly supplied.

## Reports and diagnostics

The component runner writes its atomic JSON report to:

```text
artifacts/verify/latest.json
```

The changed-component runner writes:

```text
artifacts/verify/changed-components.json
```

Reports contain the mode, requested and resolved components, dependency order,
commands, per-command status and duration, component status, skipped
components, and the final `PASS`/`FAIL` result. Command output is not copied
into the JSON artifact, preventing credentials and noisy logs from being
persisted there. Failure diagnostics printed to the terminal are redacted for
common token formats.

Useful inspection commands:

```bash
python3 -m json.tool artifacts/verify/latest.json
python3 -m json.tool artifacts/verify/changed-components.json
```

Reports under `artifacts/` are run artifacts, not source-of-truth
configuration. Do not commit credentials, tokens, cookies, or private keys.

## Verification cache

The component runner keeps a content-addressed cache of successful
deterministic runs under:

```text
.cache/pipelinegen/verify/<component>/<fingerprint>.json
```

The directory is Git-ignored and fully rebuildable: it is local tooling state,
never business state, and can be deleted safely at any time.

### What is cached

Only `PASS` results are ever stored. `FAIL`, `TIMEOUT`, and `CANCELLED`
outcomes are never written, so a failing gate is re-run on the next invocation
instead of being remembered as a reason to skip it. Records are written
atomically (temp file + `fsync` + rename).

### Fingerprint and invalidation

A component's cache key is a SHA-256 fingerprint of its exact inputs:

- the working-tree content of every registered `paths` entry (committed,
  staged, and untracked files alike — never just `git rev-parse HEAD`);
- the source directories of every `go_packages` entry, including test files
  and packages that live outside the registered paths;
- the fingerprints of its dependencies (transitively);
- `go.mod` and `go.sum` when the component runs Go packages;
- `package.json` / `package-lock.json` when it runs Node tests;
- the exact command list, `race_enabled`, and timeout budgets;
- the Go/Node/Python toolchain versions, `GOOS`/`GOARCH`, the verification
  mode (`fast` vs `race`), and the fingerprint schema version.

Changing source, tests, a dependency, `go.mod`/`go.sum`, a command, the
registry definition, the toolchain, or the mode produces a different
fingerprint and invalidates that entry. A brand-new untracked file under a
registered path participates in the hash.

### Cache hits

A hit requires a stored `PASS` record whose fingerprint exactly matches the
current fingerprint and whose cache schema is compatible. On a hit the
component's commands are not re-run; the runner reports:

```text
status=CACHED_PASS
cache_hit=true
original_duration_ms=<previous duration>
```

`CACHED_PASS` aggregates as `PASS` for the overall result and for dependency
resolution. A miss, a corrupt entry, a schema mismatch, or a non-`PASS` record
all fail closed and re-run the component. Live gates are declared
`cacheable: false` in the registry and are never cached.

### VERIFY CACHE summary

At the end of a component run the runner prints a cache summary and stores it
in the JSON report under `cache_summary`:

```text
VERIFY CACHE

hits=12
misses=2
executed=2
saved_ms=643000

audio              HIT   saved 44.9s
rendering          MISS  39.4s
```

`hits` counts `CACHED_PASS` gates, `misses` (equal to `executed`) counts gates
whose commands actually ran, and `saved_ms` is the wall-clock time avoided by
the hits. `BLOCKED` components are neither hits nor executions.

### Relationship to the pre-push whole-repo cache

This per-component cache is distinct from the pre-push hook's whole-repo
`.cache/verify/<fingerprint>.ok` shortcut. The hook cache skips `verify-main`
entirely when the whole repository fingerprint is unchanged; the component
cache skips only the unaffected components within a changed repository. Both
are optimizations over the same fail-closed gate and never weaken it.

## Pre-push contract

The version-controlled hook is `scripts/hooks/pre-push`. Install the canonical
hook path in a fresh clone with:

```bash
make install-hooks
```

For a normal push, the hook:

1. requires the checked-out branch to be `main`;
2. rejects detached HEAD and all other local branches;
3. computes the verification fingerprint;
4. reuses a matching `.cache/verify/<fingerprint>.ok` result when available;
5. otherwise runs exactly `make verify-main`;
6. blocks the push on any non-zero result; and
7. points to both JSON reports when verification finishes.

The cache is only valid for an identical source fingerprint. It is not a
replacement for the fail-closed gate and must not be manually fabricated.
On a cache hit, the report paths printed by the hook refer to the latest
verification artifacts associated with that fingerprint; they do not mean a
new verification was executed in that hook invocation. The hook does not run
`verify-race`, `verify-full`, `verify-release`, or live batteries automatically.

Do not use `git push --no-verify` or diagnostic flags as the normal workflow.
A green run with `SKIP_FORMAT=1` is not equivalent to the standard gate. If an
environmental emergency requires a bypass, repair the underlying gate and
follow the repository remediation policy immediately.

## Main-only workflow: no feature branches

Routine repository work is performed directly on `main`:

```bash
git fetch origin
git status --short --branch
# make the focused change and run its package-specific tests
make verify-fast
make verify-main
git add <focused-files>
git commit -m "<focused message>"
git push origin main
git log -n 5 --oneline
git ls-remote origin main
```

Do not create or publish feature branches, and do not use pull requests for routine repository work.
Do not force-push. Before pushing, fetch and rebase on `origin/main` if the
local branch is behind. Preserve unrelated local changes; stage and commit
only the files belonging to the current task.

For the canonical workflow rules and any conflict resolution, see
[`AGENTS.md`](../../AGENTS.md) and [`CONTRIBUTING.md`](../../CONTRIBUTING.md).
For the detailed tier-1/tier-2 gate contract, see
[`verify-main-workflow.md`](verify-main-workflow.md). For release and live
batteries, see [`verify-release-and-live.md`](verify-release-and-live.md).
