# Verification gates workflow

`make verify-main` is the canonical fail-closed pre-push gate. It is headless
and CPU-oriented: it does not require Chrome, Drive, Qdrant, or a live scraper.

This doc owns the **headless gate family (tiers 1–3)**, the development loop,
and the operator/credential boundaries. The **live / end-to-end layer** (tier-4
retirement record + the 10-step live battery) lives in
[`verify-release-and-live.md`](verify-release-and-live.md).

## Gate family

The gates form one explicit escalation chain:

| Gate | Composition | Purpose | External/live services |
|---|---|---|---|
| `make verify-fast` | foundation + static + build | Local development loop | None; headless |
| `make verify-main` | `foundation + static + changed-components + verify-architecture` | Daily fail-closed pre-push gate | None; headless |
| `make verify-race` | `foundation + all registered components (race)` | Explicit race-detector gate | None; headless |
| `make verify-full` | `verify-main + verify-race` | Complete headless gate | None; headless |
| `make verify-release` | `verify-full + verify-integration` | Pre-deploy gate, including integration tests | No live browser/Drive/Qdrant battery |

`verify-main` is the only one of these gates wired into the normal pre-push
hook. `verify-race`, `verify-full`, and `verify-release` are explicit heavier
gates and are not implicit dependencies of `verify-main`.

Area-specific gates:

- `make verify-main`: foundation, static checks, the registry-driven
  changed-component gate, and architecture checks. Foundation/static
  prerequisites are direct shared dependencies and are executed once per
  aggregate Make invocation. It intentionally excludes the complete race and
  Node suites so it remains suitable for routine pushes.
- `make test-main-stock`: diagnostic foundation, static analysis, architecture,
  and Stock unit-level checks. The authoritative Stock gates are the two
  headless levels `make verify-stock-unit` and `make verify-stock-integration`
  (`verify-stock-live` / `verify-stock-release` were retired 2026-09-13 with
  their shell drivers).
- `make verify-main-clip`: foundation, static analysis, standard targeted
  tests for the canonical Clip domain/application/API packages, and
  architecture checks. Use it for Clip-focused changes without running the full
  project unit suite or depending on an unrelated in-progress adapter
  decomposition.
- `make verify-media-intelligence`, `make verify-media-architecture`: focused
  media-plane gates (see `make/verify.mk` for the current composition).
- `make verify-race`: explicit race-tested Go packages plus all registered
  components through the shared component runner.
- `make verify-full`: `verify-main` plus `verify-race`. GNU Make deduplicates
  shared prerequisites such as foundation. (The former
  `verify-clean-checkout-build` leg was retired 2026-09-13 with its deleted
  shell driver.)
- `make verify-release`: `verify-full` plus the integration suite
  (`./tests/...`).

The target definitions live in `make/*.mk`, included by the root `Makefile`.
This document describes the contract; the Make fragments are the executable
source of truth — verify a composition with `grep -nE '^verify-release:'
make/verify.mk` rather than trusting a hardcoded line number.

`verify-main` uses the registry-driven `verify-changed-components` runner for
changed-component selection. The older `verify-changed` shell target remains
available as a compatibility/diagnostic command for direct package checks; it
is not an additional dependency of the pre-push gate and must not be treated as
a second source of truth for component ownership. Component path ownership and
the invalidation set are documented in
[`component-verification.md`](component-verification.md).

## Tier 3 — `make verify-release`

### When to run

- **After every merge commit lands on `main`** (post-merge verification of the
  integration surface).
- **Before triggering the deploy job** (final pre-deploy gate).
- **On a fresh clone or after `git pull origin main`** — catches drift between
  the pushed state and the deployed branch.

### What it costs

A few minutes for the inherited `verify-main` chain (headless tier 2), plus
several minutes more for `verify-integration` (Go tests under `./tests/...` —
some suites exercise cross-package integration surfaces and may depend on
Drive / Qdrant / scraper fixtures). These are **approximate budgets**; measure
on the actual operational host before relying on them for scheduling.

### What to do if RED (fail-closed)

Per AGENTS.md fail-closed + "never represent absence as success":

- **DO NOT proceed to deploy.**
- Identify the failing sub-gate. `verify-release` fails atomically — the
  sub-gate that printed the first non-zero exit is the culprit. Re-run each
  sub-gate individually:

```bash
make verify-main          # tier 2: foundation + static + changed components + architecture
make verify-race          # explicit race gate
make verify-integration   # ./tests/... suite
```

- Fix the failing gate and re-run the whole chain. There is no bypass flag.

## Verification rules

All gates fail closed. `verify-unit` covers Go unit packages and excludes
`./tests/...`; operational and external-service tests belong to
`verify-integration` or the live gate.

During development run the modified package tests, then `make verify-fast`.
Run `make verify-main` once after all changes are complete. Do not bypass the
pre-push hook with `git push --no-verify`.

Live checks must obtain credentials through `scripts/with-velox-auth`; never
print or hard-code `VELOX_ADMIN_TOKEN`. The full auth contract for live
surfaces is in [`verify-release-and-live.md`](verify-release-and-live.md).

## Local configuration and operator boundaries

For local development, copy `config.example.yaml` to `config.yaml` and keep
that file limited to non-secret configuration. Do not add tokens, OAuth files,
cookies, private keys, or Drive IDs to the repository. When an authenticated
local command is required, use the canonical wrapper as the credential
boundary:

```bash
scripts/with-velox-auth bash -c 'test -n "$VELOX_ADMIN_TOKEN"'
scripts/with-velox-auth ./bin/pipelinegen --mode all
```

The wrapper reads and validates the host-managed
`/etc/pipelinegen/pipelinegen.env` (mode `0640`, owner `root:pipelinegen-agents`)
and exports the token only to its child command. It must not be replaced by
`cat`/`source` instructions, repository-local token files, command-line token
arguments, or printed token checks. If the file is absent or invalid, stop and
repair provisioning rather than creating a fallback secret or weakening its
permissions.

For a systemd-managed host, distinguish daily operations from administration:

- **Daily, no interactive password**: use
  `scripts/systemd/pipelinegenctl status`, `verify`, `logs`, `restart`, or
  `restart-verify`. Only restart uses the pre-installed restricted
  `sudo -n` rule; `restart-verify` prints only `PASS` or `FAIL`.
- **Administrative, explicit sudo**: install or change the restricted sudoers
  policy, migrate manually started services, enable/disable services, run
  `systemctl daemon-reload`, rotate credentials, or repair ownership/mode of
  `/etc/pipelinegen/pipelinegen.env`. These are provisioning or change-window
  activities, not daily shortcuts.

See [`scripts/systemd/README.md`](../../scripts/systemd/README.md) for the
complete command matrix and safe local/systemd configuration flow.

## SSOT cross-references

- [`verify-release-and-live.md`](verify-release-and-live.md) — live / E2E
  coverage, tier-4 retirement record, live auth contract.
- [`component-verification.md`](component-verification.md) — component
  registry, path ownership, invalidation set.
- `AGENTS.md` — operational rules, gate hierarchy, pre-push contract.
- `make/verify.mk`, `make/verify.components.mk` — executable source of truth.
- `scripts/hooks/pre-push` — the pre-push wiring of `make verify-main`.
