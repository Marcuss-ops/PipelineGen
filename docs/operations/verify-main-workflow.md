# Verify-main workflow

`make verify-main` is the canonical fail-closed pre-push gate. It is
headless and CPU-oriented: it does not require Chrome, Drive, Qdrant, or a
live scraper.

## Gate family

- `make verify-fast`: foundation, static analysis, and build for the local
  development loop.
- `make verify-main`: `verify-push`, race-tested unit packages, Node tests,
  and architecture checks.
- `make verify-release`: `verify-main` plus the integration suite.
- `make verify-artlist-live`, `make verify-images-live`,
  `make verify-script-live`, and `make verify-vidrush-live`: authenticated
  live batteries, each with its own operational script.

The target definitions live in `make/*.mk`, included by the root
`Makefile`. This document describes the contract; the Make fragments are the
executable source of truth.

## Verification rules

All gates fail closed. `verify-unit` covers Go unit packages and excludes
`./tests/...`; operational and external-service tests belong to
`verify-integration` or the live batteries. JavaScript uses Node's built-in
test runner through `node-scraper/package.json` and `make test-js`.

During development run the modified package tests, then `make verify-fast`.
Run `make verify-main` once after all changes are complete. Do not bypass the
pre-push hook with `git push --no-verify`.

Live checks must obtain credentials through `scripts/with-velox-auth`; never
print or hard-code `VELOX_ADMIN_TOKEN`.
