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
  policy, run `migrate_to_systemd.sh`, enable/disable services, run
  `systemctl daemon-reload`, rotate credentials, or repair ownership/mode of
  `/etc/pipelinegen/pipelinegen.env`. These are provisioning or change-window
  activities, not daily shortcuts.

See [`scripts/systemd/README.md`](../../scripts/systemd/README.md) for the
complete command matrix and safe local/systemd configuration flow.
