# DataServer — Action Plan (2026-07-03)

> **Status**: 17 commit già atterrati su `main` (HEAD: `700751088`). Il piano sotto
> organizza il lavoro completato in 7 aree + 3 verifiche rimanenti. Le azioni
> di follow-up sono task cliccabili per completare il ciclo.

## Riepilogo del lavoro completato (17 commit)

| # | Area | Commit stimati | File chiave | Gate superato |
|---|------|---------------|-------------|---------------|
| 1 | Timeline voiceover corretta | ~4 | `enqueue_clips.go` | 6 scenari di test coperti |
| 2 | Delivery plan validato prima del rendering | ~3 | Handler enqueue | 5 condizioni di errore coperte |
| 3 | Unità worker canonica singola | ~2 | `canonical_worker_runtime.yml` | Convergenza systemd/container |
| 4 | Ansible reso convergente (7 fasi) | ~4 | Playbook deploy | 5 correzioni applicate |
| 5 | Permessi e stato mutabile | ~1 | `docker-compose` / mount | `/opt/velox/current` read-only |
| 6 | Source of truth Ansible | ~1 | `ansible_hosts` SQLite | Inventory statici rimossi |
| 7 | Provider Drive e YouTube — resolver comune | ~2 | `artifact_path.go` | 3 test: canonico, fallback, ambiguo |

---

## Action Plan — Fasi rimanenti

### Fase A: Verifica completa Go

- [ ] **A1 — `gofmt` check su tutti i file Go modificati**
  ```bash
  gofmt -l DataServer/...
  ```
- [ ] **A2 — `go vet` sull'intero albero DataServer**
  ```bash
  cd DataServer && go vet ./...
  ```
- [ ] **A3 — `go test -short ./...` su DataServer**
  ```bash
  cd DataServer && go test -short ./... 2>&1 | tee test-output.log
  ```
- [ ] **A4 — `go build` dei binari canonici**
  ```bash
  cd DataServer && go build ./cmd/...
  ```

### Fase B: Deploy Ansible dry-run

- [ ] **B1 — Parsing YAML di tutti i playbook modificati**
  ```bash
  python3 -c "import yaml; yaml.safe_load(open('DataServer/data/ansible/playbooks/deploy.yml'))"
  ```
- [ ] **B2 — `ansible-playbook --check` su inventory reale**
  ```bash
  ansible-playbook -i inventory.ini deploy.yml --check --diff
  ```
- [ ] **B3 — Verifica sintattica script auto-update**
  ```bash
  bash -n DataServer/data/ansible/playbooks/files/auto_update.sh
  ```

### Fase C: Verifica runtime worker

- [ ] **C1 — Health check del worker canonico**
  ```bash
  curl -s http://<worker>:8080/health | jq .
  ```
- [ ] **C2 — Verifica container naming `velox-worker-<hostname>`**
  ```bash
  docker ps --filter "name=velox-worker" --format "{{.Names}}"
  ```
- [ ] **C3 — Verifica mount read-only su `/app`**
  ```bash
  docker inspect velox-worker-<hostname> | jq '.[0].Mounts[] | select(.Destination=="/app") | .Mode'
  ```
- [ ] **C4 — Verifica stato runtime sotto `/var/lib/velox/workers/`**
  ```bash
  ls -la /var/lib/velox/workers/<hostname>/{work,config,cache,blobs,assets_cache}
  ```

### Fase D: CI gate & audit trail

- [ ] **D1 — Verifica che GitHub Actions abbia girato sul nuovo HEAD**
  ```bash
  gh run list --branch main --limit 5
  ```
- [ ] **D2 — Se CI non presente, esegui `go test -race ./...` localmente**
- [ ] **D3 — `git diff --stat` tra HEAD iniziale e `700751088` per audit**

---

## Regole operative (da AGENTS.md)

- **NO BRANCH** — tutto diretto su `main`
- **NO `--force`** su `origin/main`
- **Push incrementale** dopo ogni commit auto-sufficiente
- **Co-authored-by** trailer obbligatorio per commit agent
- **Rebase, non merge** se `origin/main` avanza durante la finestra di commit
