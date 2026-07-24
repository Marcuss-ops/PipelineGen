# Verify-Main Workflow — Granular Make Targets

**Owner**: this runbook is the operator-facing canonical reference for the `make verify-main` gate and its granular sub-targets.  
**Lockstep surfaces**: `Makefile` (canonical target definitions) · `README.md` · `AGENTS.md` (Git workflow on `main`).  
**Audience**: every contributor pushing to `main`.  

---

## 1. Goal

`make verify-main` is the canonical fail-closed pre-push gate. It is intentionally split into smaller, focused targets so that:

- failures are isolated by area (domain, infrastructure, API, Artlist, images, stock, etc.);
- development loops are faster — you run only the targets that touch the code you changed;
- CI and local behavior stay identical.

---

## 2. Target reference

| Target | What it runs | When to use |
|--------|--------------|-------------|
| `verify-base` | Go version check, no-secrets audit, `gofmt`, `go mod tidy` | before every commit; cheapest gate |
| `verify-go-core` | `go test -race ./internal/domain/... ./internal/application/...` | after domain/application changes |
| `verify-go-infrastructure` | `go test -race ./internal/infrastructure/...` | after adapter/infra changes |
| `verify-go-api` | `go test -race ./internal/api/...` | after handler/routing changes |
| `verify-go-commands` | `go test -race ./cmd/... ./pkg/...` | after CLI/pkg changes |
| `verify-go-tests` | `go test -race ./tests/...` | after operational/E2E test changes |
| `verify-go` | orchestrates the five targets above, then `go vet ./...` and `go build ./...` | full Go surface check |
| `verify-architecture` | `cmd/architecture-aggregate --dry-run` + `cmd/archcheck` | after cross-cutting architectural changes |
| `verify-artlist` | Go tests for `infrastructure/artlist`, `providers/artlist`, `api/assets/artlist`; plus `node-scraper` npm test | while working on Artlist |
| `verify-images` | `go test -race` for `domain/image`, `application/images`, `api/images` | while working on images |
| `verify-stock` | `go test -race` for stock pipeline and API | while working on stock |
| `verify-main` | `verify-base` + `verify-go` + `verify-architecture` + `verify-artlist` | complete pre-push gate |

> **Note:** `verify-images` and `verify-stock` are focused development helpers and are **not** part of `verify-main`. Run them explicitly when you work on those modules.

---

## 3. Recommended workflow

### 3.1 While you are developing on a specific module

Run the target that covers the module you are touching:

```bash
# Artlist work
make verify-artlist

# Images work
make verify-images

# Stock work
make verify-stock

# Core domain/application work
make verify-go-core
```

### 3.2 Before a normal commit

Run the cheap base gate plus the module target that matches your changes:

```bash
make verify-base verify-artlist
```

### 3.3 Before pushing to `main`

Run the full pre-push gate:

```bash
make verify-main
```

Only push when `verify-main` is green. The gate is fail-closed: any non-zero step blocks the push.

---

## 4. Git workflow on `main`

Per `AGENTS.md`:

- Work directly on `main`.
- Do not create feature branches or pull requests for routine repository work.
- Before pushing, fetch and rebase on `origin/main`:

  ```bash
  git fetch origin main
  git rebase origin/main
  ```

- Never force-push.
- Push directly to `main`.
- After every push, inspect the last commits and confirm the remote contains the intended work:

  ```bash
  git log -n 5 --oneline
  ```

- Commit and push frequently after meaningful changes.

---

## 5. Troubleshooting

- **A target fails on the first error it hits.** This is by design: fix the failure and re-run the target.
- **`verify-main` is too slow for rapid iteration.** Run only the granular target that covers your change; reserve `verify-main` for the final pre-push check.
- **`verify-artlist` reports failures in `node-scraper`.** Check that Node 22 is installed and that `cd node-scraper && npm test` passes in isolation.
- **Formatting gate fails.** Run `make fmt` (alias for `go fmt ./...`) and re-run the gate.
