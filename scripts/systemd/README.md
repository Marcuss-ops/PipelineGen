# scripts/systemd/

Idempotent migration from unstable nohup/tmux-managed processes to
**systemd-managed services** with auto-restart on crash.

> ### Retired helper scripts (2026-09-13)
>
> Commit `7e6965aab` ("purge 94% shell + 87% python dust") deleted several
> helpers this document used to reference. They are **not** in the tree:
> `scripts/systemd/migrate_to_systemd.sh`, `scripts/systemd/sudoers/install_operator_access.sh`
> (+ its `_test.sh`), `scripts/systemd/pipelinegenctl_test.sh`, `scripts/start_daemon.sh`
> and `scripts/rotate_token.sh`. Any command below that names one of them must be
> performed manually:
>
> | Former helper | Manual replacement |
> |---|---|
> | `install_operator_access.sh --install/--check` | edit + `sudo visudo -cf` the policy, then `sudo install -m 0440 -o root -g root scripts/systemd/sudoers/pipelinegen-operator /etc/sudoers.d/pipelinegen-operator` |
> | `migrate_to_systemd.sh` | stop the manual processes, `sudo install` the unit files, `sudo systemctl daemon-reload`, `sudo systemctl enable --now pipelinegen.service pipelinegen-worker.service` |
> | `rotate_token.sh` | generate a fresh 64-hex value, replace `VELOX_ADMIN_TOKEN=` in `/etc/pipelinegen/pipelinegen.env`, `sudo systemctl restart pipelinegen`, verify the restarted PID environment and `make auth-check` |
> | `pipelinegenctl_test.sh` | none — run `scripts/systemd/pipelinegenctl verify` on the host |
>
> `scripts/systemd/pipelinegenctl` (daily status/logs/restart surface) and the
> unit files remain the current, tracked interface.

PipelineGen server and worker are host-native services. Docker Compose is used
only for supporting infrastructure (Qdrant and SearXNG), so application
changes no longer require `docker compose build`.

## TL;DR

The systemd unit files at `/etc/systemd/system/{pipelinegen,pipelinegen-worker}.service`
are managed independently from the supporting Compose infrastructure.

> `scripts/systemd/migrate_to_systemd.sh` no longer exists (deleted by commit
> `7e6965aab`). Perform the migration manually, in this order.

```bash
# 1. Stop the manual nohup/tmux processes for the server and worker
#    (kill the PIDs you started by hand; do not touch unrelated processes).

# 2. Ask the operator to install/refresh the unit files, with password:
sudo install -m 0644 scripts/systemd/pipelinegen.service /etc/systemd/system/pipelinegen.service
sudo install -m 0644 scripts/systemd/pipelinegen-worker.service /etc/systemd/system/pipelinegen-worker.service
sudo systemctl daemon-reload
sudo systemctl enable --now pipelinegen.service pipelinegen-worker.service

# 3. Verify
systemctl is-active pipelinegen.service pipelinegen-worker.service
```

## Daily commands — no interactive password

Use these commands for routine operation. They do not require an interactive
password or a TTY:

```bash
scripts/systemd/pipelinegenctl status
scripts/systemd/pipelinegenctl verify
scripts/systemd/pipelinegenctl logs
scripts/systemd/pipelinegenctl restart
scripts/systemd/pipelinegenctl restart-verify
```

| Command | Purpose | Privilege behavior |
|---|---|---|
| `status` | Check whether `pipelinegen.service` is active | No sudo |
| `verify` | Wait for active service and probe `/ready` | No sudo |
| `logs` | Show bounded, sanitized journal output | No sudo |
| `restart` | Restart only `pipelinegen.service`, then wait active | `sudo -n`; restricted rule, no prompt |
| `restart-verify` | Restart, verify `/ready`, and run the Drive canary | `sudo -n`; output is only `PASS` or `FAIL` |

`restart` uses only `sudo -n systemctl restart pipelinegen.service` and fails
closed when the restricted NOPASSWD rule is not installed. The other commands
do not use sudo. No daily command accepts a password argument or reads the
secret file directly.

`restart-verify` additionally runs the authenticated Drive canary against
`/api/admin/drive/canary-upload` with the canonical `folder_alias` value
`Boxe`. Credentials are loaded only by `scripts/with-velox-auth`; the token
and canary response are kept inside the helper boundary. This command emits
only one verdict line — `PASS` or `FAIL` — and never prints the response body,
Drive IDs, Drive URLs, or token material.

## Administrative operations — explicit sudo required

The following are host-administration tasks, not daily operation. Perform them
only during provisioning, migration, credential rotation, or an intentional
unit/configuration change. They may require the operator's normal sudo
password and should not be automated by pasting credentials into a shell:

| Administrative task | Command or procedure |
|---|---|
| Install restricted daily access | manually `sudo install -m 0440 -o root -g root scripts/systemd/sudoers/pipelinegen-operator /etc/sudoers.d/pipelinegen-operator` (the former `install_operator_access.sh` helper was deleted by commit `7e6965aab`) |
| Validate policy without changing host | `sudo visudo -cf scripts/systemd/sudoers/pipelinegen-operator` (read-only check) |
| Migrate manually started services | stop the hand-started PIDs, `sudo install` the unit files, `sudo systemctl daemon-reload`, `sudo systemctl enable --now ...` (the former `migrate_to_systemd.sh` was deleted by commit `7e6965aab`) |
| Reload changed unit/drop-in files | `sudo systemctl daemon-reload` |
| Enable/start services after migration | `sudo systemctl enable --now pipelinegen.service pipelinegen-worker.service` |
| Rotate credentials | no rotation helper exists in the tree (`scripts/rotate_token.sh` was deleted by commit `7e6965aab`): generate a fresh 64-hex value, replace `VELOX_ADMIN_TOKEN=` in `/etc/pipelinegen/pipelinegen.env`, `sudo systemctl restart pipelinegen`, verify the restarted PID environment and `make auth-check` |
| Repair secret-file ownership/mode | `sudo chown root:pipelinegen-agents /etc/pipelinegen/pipelinegen.env` and `sudo chmod 0640 /etc/pipelinegen/pipelinegen.env` |

Do not grant `NOPASSWD: ALL`, wildcard `systemctl` access, or access to other
services. There is no installer helper in the tree any more: the caller must
already have root authorization and must validate the policy before
installation. (The former `install_operator_access.sh`, which rendered,
validated and installed the policy, was deleted by commit `7e6965aab`.)

### Restricted operator policy

The versioned policy in `sudoers/pipelinegen-operator` grants the configured
operator only these exact commands as root:

```text
/usr/bin/systemctl restart pipelinegen.service
/usr/bin/systemctl start pipelinegen.service
/usr/bin/systemctl stop pipelinegen.service
```

It does **not** grant `status`, `enable`, `disable`, `daemon-reload`, wildcard
service names, or a root shell. The
policy is intentionally separate from the one-time systemd migration, which
may require broader administrative commands.

Validate the checked-in policy without changing the host (read-only, no sudo
needed for the parse):

```bash
sudo visudo -cf scripts/systemd/sudoers/pipelinegen-operator
```

Install it on the deployment host only after obtaining the host's normal root
authorization through the operator's own process. There is no helper script:

```bash
sudo install -m 0440 -o root -g root \
  scripts/systemd/sudoers/pipelinegen-operator \
  /etc/sudoers.d/pipelinegen-operator
```

The versioned policy grants **only** the three exact commands above; it must
contain no includes, wildcard commands, or unrelated services. `visudo -cf`
must exit 0 before the file is installed. (The former
`install_operator_access.sh` and `install_operator_access_test.sh` were deleted
by commit `7e6965aab`.)

## Safe local configuration flow

For a workstation or a controlled development host:

1. Copy `config.example.yaml` to `config.yaml`; keep credentials out of that
   file and out of the repository.
2. Use the canonical secret file only on a host configured for it:
   `/etc/pipelinegen/pipelinegen.env`, mode `0640`, owner
   `root:pipelinegen-agents`.
3. Validate the loader boundary without printing the value:

   ```bash
   scripts/with-velox-auth bash -c 'test -n "$VELOX_ADMIN_TOKEN"'
   ```

4. Start a local binary through the wrapper when auth is needed:

   ```bash
   scripts/with-velox-auth ./bin/pipelinegen --mode all
   ```

5. Prefer `pipelinegenctl verify` or `restart-verify` for a managed service;
   do not source the env file manually, put the token in command arguments, or
   capture API responses containing Drive metadata.

If the canonical file is absent or invalid, stop and fix host provisioning;
do not create a fallback token or downgrade permissions to `0644`.

## Optional environment variables

- `PIPELINEGEN_BASE_URL` — local HTTP base URL (default `http://127.0.0.1:8000`)
- `PIPELINEGEN_READY_TIMEOUT` — readiness wait in seconds (default `60`)
- `PIPELINEGEN_RESTART_TIMEOUT` — bounded restart timeout (default `30`)
- `PIPELINEGEN_LOG_LINES` — sanitized journal lines (default `80`)
- `WITH_VELOX_AUTH_BIN` — auth wrapper override for isolated tests (default `scripts/with-velox-auth`)
- `JQ_BIN` — jq command override for isolated tests (default `jq`)

The former isolated contract test `scripts/systemd/pipelinegenctl_test.sh`
was deleted by commit `7e6965aab`; exercise `scripts/systemd/pipelinegenctl`
directly against a host (`status` / `verify`).

## Why this exists

**Symptom** (observed during E2E verification, 2026-07-07/08): the
`pipelinegen` and `pipelinegen-worker` processes were launched via
`nohup ./pipelinegen --mode all &` and detached. They run inside a
parent shell that may exit (logout, SSH disconnect, terminal close),
causing the children to either receive SIGHUP or be reparented to PID 1
without auto-restart. During multi-minute E2E tests, the service
disappeared and the test had to be retried.

**Root cause**: the host migration previously documented a retired scraper unit;
that unit is no longer part of the repository. PipelineGen services are
managed independently from external provider endpoints.

**The fix has 2 layers**:
1. **Manual systemd migration** (this directory) — stop the hand-started
   processes, install the unit files, enable the services, and verify the
   post-state. The former wrapper `migrate_to_systemd.sh` was deleted by
   commit `7e6965aab`, so each step is performed by hand (see TL;DR above).
2. **`PR-SYSTEMD-RESTART-SUDO-NOPASSDEP`** (architecture/current.yaml)
   — long-term fix: add a NOPASSWD sudoers entry so the operator does
   not need to type a password for routine restarts. This is a separate
   operator action (not automatable by the agent).

## Unit file state (post-migration)

| Service                  | Restart        | RestartSec | Status   |
|--------------------------|----------------|------------|----------|
| `pipelinegen.service`    | `always`       | 3s         | active   |
| `pipelinegen-worker.service` | `on-failure` | 10s | active |

The existing drop-in files in `/etc/systemd/system/pipelinegen.service.d/`
must be **preserved** during the manual migration (do not remove them):

- `fase1_override.conf` — `VELOX_FEATURE_STOCK_PIPELINE_ENABLED=true`
- `stock_flag.conf` — stock pipeline feature flag
- `hmac.conf` — `VELOX_DELIVERY_HMAC_SECRET`
- `webhook.conf` — `VELOX_BASE_URL`
- `worker-token.conf` — `VELOX_WORKER_TOKEN`

## Historical migration procedure (script retired)

The former `migrate_to_systemd.sh` was **idempotent** and safe to re-run; it
was deleted by commit `7e6965aab`. Its steps are retained here as the manual
checklist:

| Step | Action |
|------|--------|
| 0 | Pre-flight: project root, binary, `.env`, `systemctl` present |
| 1 | Detect manual processes via `pgrep` + tmux sessions |
| 2 | Operator confirmation |
| 3 | Kill tmux sessions + send SIGTERM to manual PIDs (SIGKILL fallback) |
| 4 | Run the `sudo systemctl ...` commands |
| 5 | Post-condition: verify services active + ports bound + /ready 200 |

## Alternatives considered (and why we did NOT pick them)

| Alternative | Why NOT |
|-------------|---------|
| **User-level systemd unit** (`~/.config/systemd/user/...`) | Dies at user logout unless `loginctl enable-linger pierone` is set. Duplicates the system-level unit. |
| **Supervisord** | New dependency, separate daemon to manage. Overkill when the existing systemd units are already correct. |
| **Docker container with restart policy** | Requires migrating the binary to a container. Heavy change for a 2-line `Restart=` fix. |
| **Nohup wrapper with health-check loop** | Not a real service. Same instability on parent exit. |
| **tmux-based restart loop** | Still depends on tmux session surviving reboots. Workaround, not a fix. |

## Long-term follow-ups (forward-pointers)

- **`PR-SYSTEMD-RESTART-SUDO-NOPASSDEP`** (architecture/current.yaml#E2E-VERIFICATION-BLOCKED-2026-07-07)
  The versioned policy and validator now live under `scripts/systemd/sudoers/`.
  The operator must install it on the deployment host. It allows only exact
  restart/start/stop commands for `pipelinegen.service`; status, readiness,
  and journal access remain unprivileged.

- **`PR-PIPELINEGEN-LOGROTATE`**
  Configure `journalctl --vacuum-time=7d` or a `logrotate` rule for
  `/var/log/pipelinegen/` (or the journal) to prevent disk fill on
  long-running deployments.

- **`PR-PIPELINEGEN-STARTUP-HEALTH-CHECK`**
  Add `ExecStartPost=/usr/bin/curl --fail http://127.0.0.1:8000/ready`
  to the unit file so systemd marks the service `failed` if it
  crashes within 30s of startup (e.g. config error).

## Verification

```bash
# After running the migration + operator sudo commands:
systemctl is-active pipelinegen.service    # expect: active
systemctl is-active pipelinegen.service pipelinegen-worker.service # expect: active
systemctl show pipelinegen.service | grep -E 'Restart=|RestartSec=|MainPID='
# expect: Restart=always, RestartSec=3, MainPID=<nonzero>

# Simulate a crash + verify auto-restart:
kill -9 $(pgrep -f 'pipelinegen --mode all')
sleep 6
systemctl is-active pipelinegen.service    # expect: active
pgrep -f 'pipelinegen --mode all'         # expect: a new PID
```

## References

- `architecture/current.yaml#E2E-VERIFICATION-BLOCKED-2026-07-07` — the
  original E2E verification audit-pin that surfaced the service
  instability
- `architecture/current.yaml#PR-SYSTEMD-RESTART-SUDO-NOPASSDEP` — the
  forward-pointer for the NOPASSWD sudoers entry
- `AGENTS.md` § "Active Concerns" item 8 (heavy AI-generated codebase)
  — the operational context for this fix
- `scripts/start_daemon.sh` — the original `nohup`-based launcher that the
  migration replaced; it was deleted by commit `7e6965aab`, so environments
  without systemd must launch the binary manually under `scripts/with-velox-auth`
