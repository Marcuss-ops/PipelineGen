# PipelineGen - Advanced Media Processing Engine

PipelineGen is a Go-based backend service that manages media processing pipelines for YouTube clips and Artlist assets. It provides AI-driven script generation, voiceover synthesis, image generation, and Google Drive synchronization.

## Features

- YouTube clip search, extraction, metadata enrichment, and Drive storage.
- Artlist ingestion for automated asset search and download.
- AI voiceover generation with async job support.
- Image generation integrations.
- SQLite-backed job queue with progress tracking and event logs.
- Google Drive synchronization for generated media assets.

## Tech Stack

- Backend: Go with Gin
- Automation helpers: Python
- Database: SQLite with WAL mode
- External tools: yt-dlp and FFmpeg
- Cloud: Google Drive API
- CI/CD: GitHub Actions

## Getting Started

### Prerequisites

- Go 1.25+
- Python 3.10+
- yt-dlp installed in your path
- FFmpeg

### Git Credential Helper Setup

> **⚠️ Security anti-pattern (PR-AUTH-CREDENTIAL-HELPER-SETUP, P0, deadline 2026-08-01):**
> pasting a GitHub Personal Access Token (PAT) into a chat window to authenticate
> `git push` is a **credential leak**: the token is captured in chat history, may
> be visible on shared screens, persists in the agent's training context, and
> cannot be reliably rotated without auditing every chat export. This anti-pattern
> has been observed in this session — do not repeat it. The **canonical** way to
> authenticate `git push` is to configure `git config --global credential.helper`
> so the OS keyring / encrypted store caches the token after the first interactive
> `git push` — no copy-paste of secrets into chat, ever.

The remote `origin` is HTTPS; every `git push` requires authentication. Configure
the OS-level credential helper **once per machine** before your first push.

#### Recommended: fine-grained Personal Access Token

GitHub deprecated classic PATs for new workflows. Generate a **fine-grained PAT**
scoped to a single repository + a single permission (Contents: Read and write)
at <https://github.com/settings/personal-access-tokens/new>:

- Resource owner: `Marcuss-ops`
- Repository access: `PipelineGen` only
- Permissions: `Contents` → `Read and write` (NO other scopes)
- Expiration: 30 days (auto-rotate cadence)

A fine-grained PAT limits the blast radius if the token is leaked (it can only
push to one repo, one permission), and auto-rotation forces periodic re-issue.

#### macOS

```bash
git config --global credential.helper osxkeychain
```

The macOS Keychain stores the GitHub PAT after the first interactive
`git push` (the OS prompts for username + token once; subsequent pushes are
silent).

#### Linux

**Recommended: in-memory cache** (no on-disk persistence, expires after 900s):

```bash
git config --global credential.helper 'cache --timeout=900'
```

For an **encrypted** alternative (preferred over the plaintext `store` helper):

- [Git Credential Manager for Unix](https://github.com/git-ecosystem/git-credential-manager) (`gcm` or `manager-core` alias)
- [`pass`](https://www.passwordstore.org/) (GPG-encrypted, requires a GPG key)

**Last resort: plaintext `store`** (writes the token to `~/.git-credentials`,
`chmod 600`):

```bash
git config --global credential.helper store
```

#### Windows

```bash
git config --global credential.helper manager
```

Uses the Windows Credential Manager. **WSL note:** Windows Subsystem for
Linux uses the **Linux** helpers (e.g. `cache` or GCM-for-Unix), NOT
`manager` — the WSL/Linux credential store is separate from the Windows
Credential Manager.

#### Verify

```bash
git config --global --get credential.helper
```

Expected output: `osxkeychain` (macOS), `cache --timeout=900` (Linux cache),
`store` (Linux last-resort), `manager` (Windows), or `gcm` (GCM-for-Unix).

#### If you already pasted a PAT in chat (recovery)

If you (or an agent acting on your behalf) have already pasted a GitHub
PAT into a chat window, the token is **leaked**. Recovery procedure
**immediately**:

1. **Rotate the token** at <https://github.com/settings/personal-access-tokens>:
   - Locate the leaked PAT → click `Delete` (NOT just revoke; the leaked copy
     is already in chat history and any other export).
   - Generate a **new fine-grained PAT** with the same scope (Contents:
     Read and write on `PipelineGen` only) → 30-day expiration.
2. **Clear cached credentials** on every machine that uses the leaked token:
   ```bash
   # macOS
   git credential-osxkeychain erase
   # Linux (cache helper — wait for timeout, or kill the cache process)
   git credential-cache exit
   # Linux (store helper)
   rm ~/.git-credentials
   # Windows
   git credential-manager erase
   ```
3. **Re-push once** with the new token — the OS will prompt for the new
   credential and cache it.

#### Alternative: SSH keys (zero-token)

If you can switch the remote to SSH, this is the most secure option (no
token at all in `git` operations):

```bash
# Generate a key (one-time)
ssh-keygen -t ed25519 -C "your_email@example.com"

# Add the public key to your GitHub account: https://github.com/settings/keys
# Then update the remote
git remote set-url origin git@github.com:Marcuss-ops/PipelineGen.git
```

For multi-account setups (work + personal), use the per-URL variant
`credential.https://github.com.helper` (forward-pointer; not common).

### Installation

```bash
git clone https://github.com/Marcuss-ops/PipelineGen.git
cd PipelineGen
cp config.example.yaml config.yaml
go build -o pipelinegen ./cmd/server/
./pipelinegen --mode all
```

## API Endpoints (Core)

- Health: `GET /health`, `GET /health?deep=true`
- Script generation: `POST /api/script/generate` (canonical)
- Deprecated script adapters: `POST /api/script/generate-from-clips`, `POST /api/script/generate-with-images`
- Job status: `GET /api/jobs/:id`
- Images: `POST /api/images/generate`
- Artlist: `POST /api/artlist/run`
- Clips: `POST /api/clips/search`, `GET /api/clips/info`
- Media assets: `GET /api/media/search`, `GET /api/media/diagnostics`
- Voiceover: `POST /api/media/voiceover/generate`
- Fullimages (still-image generation, video MP4 pipeline retired 2026-07-10): `POST /api/fullimages/image/generate`
- System: `GET /api/system/doctor`

## Job System

PipelineGen uses a unified job system for long-running operations. Jobs are stored in `data/media/media.db.sqlite` and processed by background workers. Track job progress through `/api/jobs` endpoints.

### Fullimages migration (video→image)

The pre-2026-07-10 `fullimages` surface generated MP4s via a Ken Burns
effect. The **Option B verdict (2026-07-10)** collapsed the MP4 pipeline
to a still-image pipeline:

- **Endpoint**: `POST /api/fullimages/image/generate` (was `POST /api/fullimages/video/generate`).
- **Response field**: `images[]` (was `videos[]`).
- **Response element type**: `SectionImage` (was `SectionVideo`); the
  canonical `ImagePath` field (was `VideoPath`).

For operator-side migrations, see the canonical runbook at
[`docs/operations/fullimages-migration-runbook.md`](./docs/operations/fullimages-migration-runbook.md)
and the `fullimages-migrate` admin CLI:

```bash
./admin fullimages-migrate --target-dir ~/ops/scripts --exts .sh,.py,.md,.yaml
# default = dry-run (no writes); add --apply to write the canonical text replacements

# --json output mode (automation harnesses, jq, CI pipelines, monitoring scrapers)
./admin fullimages-migrate --target-dir ~/ops/scripts --exts .sh,.py,.md,.yaml --json
```

## Project Structure

- `cmd/`: server, worker, and admin entry points.
- `internal/api/`: HTTP transport layer.
- `internal/app/`: composition root, wiring, bootstrap, and module lifecycle.
- `internal/application/`: use-case orchestration.
- `internal/domain/`: canonical contracts and domain types.
- `internal/infrastructure/`: database, Drive, media, AI, process, and external-system adapters.
- `pkg/`: leaf utility packages.
- `scripts/`: utility and AI processing scripts.
- `migrations/sqlite/`: SQLite migrations.
- `config/`: YAML configuration and presets.

## Documentation

For the meta-question "what's authoritative here?", see [`CANONICAL.md`](./CANONICAL.md).

Canonical entry points:

- [`ARCHITECTURE.md`](./ARCHITECTURE.md): system diagram, data flow, module registry, and layer ownership.
- [`AGENTS.md`](./AGENTS.md): agent rules and workflow lessons.
- [`PROJECT_GUIDE.md`](./PROJECT_GUIDE.md): quick start in Italian.
- [`docs/api/ACTIVE_API_GENERATED.md`](./docs/api/ACTIVE_API_GENERATED.md): generated snapshot of currently registered HTTP routes.
- [`architecture/current.yaml`](./architecture/current.yaml): active architecture ratchet tracker.
- [`architecture/issues.yaml`](./architecture/issues.yaml): cross-package follow-up ledger.

*Developed by the Marcuss-ops Team*
