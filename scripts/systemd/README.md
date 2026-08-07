# scripts/systemd/

Idempotent migration from unstable nohup/tmux-managed processes to
**systemd-managed services** with auto-restart on crash.

## TL;DR

The systemd unit files at `/etc/systemd/system/{pipelinegen,artlist-scraper}.service`
**already have the right `Restart=` directives** — the existing units are
inactive because the services were started manually via `nohup ... &`
(bypassing systemd entirely). The fix is just to **stop the manual
processes and let systemd manage them**.

```bash
# 1. Kill the manual processes + delegate the sudo commands to the operator
bash scripts/systemd/migrate_to_systemd.sh
# (the script prints the exact `sudo systemctl enable --now ...` commands
#  because the host does not have NOPASSWD for systemctl)

# 2. Operator runs (with password):
sudo systemctl daemon-reload
sudo systemctl enable --now pipelinegen.service
sudo systemctl enable --now artlist-scraper.service

# 3. Verify
systemctl is-active pipelinegen.service artlist-scraper.service
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
| Install restricted daily access | `sudo scripts/systemd/sudoers/install_operator_access.sh --install` |
| Validate policy without changing host | `scripts/systemd/sudoers/install_operator_access.sh --check` |
| Migrate manually started services | `AUTO_YES=1 bash scripts/systemd/migrate_to_systemd.sh` |
| Reload changed unit/drop-in files | `sudo systemctl daemon-reload` |
| Enable/start services after migration | `sudo systemctl enable --now pipelinegen.service` and scraper unit as needed |
| Rotate credentials | `sudo scripts/rotate_token.sh` using the documented host process |
| Repair secret-file ownership/mode | `sudo chown root:pipelinegen-agents /etc/pipelinegen/pipelinegen.env` and `sudo chmod 0640 /etc/pipelinegen/pipelinegen.env` |

Do not grant `NOPASSWD: ALL`, wildcard `systemctl` access, or access to other
services. The installer does not invoke sudo itself: the caller must already
have root authorization, and the policy is validated before installation.

### Restricted operator policy

The versioned policy in `sudoers/pipelinegen-operator` grants the configured
operator only these exact commands as root:

```text
/usr/bin/systemctl restart pipelinegen.service
/usr/bin/systemctl start pipelinegen.service
/usr/bin/systemctl stop pipelinegen.service
```

It does **not** grant `status`, `enable`, `disable`, `daemon-reload`, wildcard
service names, access to `artlist-scraper.service`, or a root shell. The
policy is intentionally separate from the one-time systemd migration, which
may require broader administrative commands.

Validate the checked-in policy without changing the host:

```bash
scripts/systemd/sudoers/install_operator_access.sh --check
```

Install it on the deployment host only after obtaining the host's normal root
authorization through the operator's own process; the helper never invokes
sudo, asks for a password, reads tokens, or prints credentials:

```bash
sudo scripts/systemd/sudoers/install_operator_access.sh --install
```

The installer renders and validates the policy with `visudo`, writes only
`/etc/sudoers.d/pipelinegen-operator`, and enforces mode `0440`. It refuses
unsafe paths, policy includes, wildcard commands, unrelated services, and any
policy that is not exactly the three-command grant. Its isolated test is:

```bash
scripts/systemd/sudoers/install_operator_access_test.sh
```

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

The isolated contract test is:

```bash
scripts/systemd/pipelinegenctl_test.sh
```

## Why this exists

**Symptom** (observed during E2E verification, 2026-07-07/08): the
`pipelinegen` and `artlist-scraper` processes were launched via
`nohup ./pipelinegen --mode all &` and detached. They run inside a
parent shell that may exit (logout, SSH disconnect, terminal close),
causing the children to either receive SIGHUP or be reparented to PID 1
without auto-restart. During multi-minute E2E tests, the service
disappeared and the test had to be retried.

**Root cause**: the systemd unit files **were** configured with
`Restart=always` (pipelinegen) and `Restart=on-failure`
(artlist-scraper), but the unit state was `inactive` because nobody had
run `systemctl enable --now`. The manual nohup process shadowed the
service entry.

**The fix has 2 layers**:
1. **`migrate_to_systemd.sh`** (this directory) — stops the manual
   processes, prints the operator commands, and verifies the post-state.
2. **`PR-SYSTEMD-RESTART-SUDO-NOPASSDEP`** (architecture/current.yaml)
   — long-term fix: add a NOPASSWD sudoers entry so the operator does
   not need to type a password for routine restarts. This is a separate
   operator action (not automatable by the agent).

## Unit file state (post-migration)

| Service                  | Restart        | RestartSec | Status   |
|--------------------------|----------------|------------|----------|
| `pipelinegen.service`    | `always`       | 3s         | active   |
| `artlist-scraper.service`| `on-failure`   | 10s        | active   |

The existing drop-in files in `/etc/systemd/system/pipelinegen.service.d/`
are **preserved** by the migration script:

- `fase1_override.conf` — `VELOX_FEATURE_STOCK_PIPELINE_ENABLED=true`
- `stock_flag.conf` — stock pipeline feature flag
- `hmac.conf` — `VELOX_DELIVERY_HMAC_SECRET`
- `webhook.conf` — `VELOX_BASE_URL`
- `worker-token.conf` — `VELOX_WORKER_TOKEN`

## How the migration script works

`migrate_to_systemd.sh` is **idempotent** — safe to re-run. Each step
prints a clear marker so the operator can verify what happened.

| Step | Action |
|------|--------|
| 0 | Pre-flight: project root, binary, `.env`, `systemctl` present |
| 1 | Detect manual processes via `pgrep` + tmux sessions |
| 2 | Ask operator confirmation (skippable with `AUTO_YES=1`) |
| 3 | Kill tmux sessions + send SIGTERM to manual PIDs (SIGKILL fallback) |
| 4 | Print the `sudo systemctl ...` commands for the operator |
| 5 | Post-condition: verify services active + ports bound + /ready 200 |

By default `SKIP_SUDO=1` (host has no NOPASSWD); the script just prints
the commands. Set `SKIP_SUDO=0` once NOPASSWD is configured and the
script will execute the sudo commands itself.

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
systemctl is-active artlist-scraper.service # expect: active
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
- `scripts/start_daemon.sh` — the original `nohup`-based launcher that
  the migration replaces (kept as a fallback for environments without
  systemd)
