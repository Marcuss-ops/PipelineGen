# SCRIPT-DOCS-REACT-ONLINE-TEST-2026-07-09 — Piano d'Azione

**Ship date**: 2026-07-09 | **Deadline**: 2026-07-16 | **Status**: `pending`
**Owner capability**: `internal/api/script-docs/` + `internal/infrastructure/database/sqlite/scripts/research_cache.go`

## §0 — Honest Status Snapshot (godlike/07 NO-FAKE-AVAILABILITY)

L'endpoint `POST /api/script-docs/generate` **esiste** nel codebase ed è montato correttamente.
Tuttavia, oggi risponde **sempre 503** perché:

1. `ReActPort` è `nil` al composition root (`internal/app/registry_public_modules.go:278`: `Port: nil`)
2. Non esiste un adapter Python ReAct bridge che implementa `ReActPort`
3. Il `research_cache` repository esiste ma **nessun consumer lo chiama** — la cache è puro dead code

**Per far passare i 10 test serve implementare 3 cose in quest'ordine:**
1. Python ReAct bridge (`scripts/bridges/reAct_agent.py`) che fa ricerca online + sintesi
2. Go adapter che wrappa il bridge Python e implementa `ReActPort` con cache integrata
3. Composition-root wiring in `registerScriptDocs`

## §1 — 5 PR incrementali (ordine esatto, direct-to-main)

| # | PR ID | Cosa fa | File | Deadline |
|---|-------|---------|------|----------|
| 1 | `PR-SCRIPTDOCS-REACT-BRIDGE` | Python ReAct agent bridge: `scripts/bridges/reAct_agent.py` con ricerca web (DuckDuckGo/SerpAPI), sintesi LLM, output `{result, status, steps_taken}` | `scripts/bridges/reAct_agent.py` (NEW ~200 LoC) | 2026-07-11 |
| 2 | `PR-SCRIPTDOCS-GO-ADAPTER` | Go adapter `internal/infrastructure/react/adapter.go` che wrappa `os/exec` Python bridge + integra `research_cache` (GetResearchCache → cache hit ritorna subito; miss → esegue bridge → SaveResearchCache) | `internal/infrastructure/react/adapter.go` (NEW ~250 LoC) + `adapter_test.go` (NEW ~150 LoC) | 2026-07-12 |
| 3 | `PR-SCRIPTDOCS-COMPOSITION-WIRE` | Wiring in `registerScriptDocs`: `ReActPort` concreto invece di `nil` + `VELOX_FEATURE_SCRIPT_DOCS_ENABLED=true` nel dev config | `internal/app/registry_public_modules.go` (+5 LoC) | 2026-07-12 |
| 4 | `PR-SCRIPTDOCS-10STEP-TEST-SUITE` | Shell smoke `tests/operational/script_docs_online_smoke.sh` che esegue i 10 test del piano operativo | `tests/operational/script_docs_online_smoke.sh` (NEW ~350 LoC) | 2026-07-13 |
| 5 | `PR-SCRIPTDOCS-CACHE-CONCURRENCY-FIX` | Fix race condition: `sync.Mutex` su `SaveResearchCache` per evitare `database is locked` sotto carico parallelo (test 8) | `internal/infrastructure/react/adapter.go` (+10 LoC) | 2026-07-14 |

## §2 — PR-1: Python ReAct Bridge

**File**: `scripts/bridges/reAct_agent.py` (NEW)

```python
#!/usr/bin/env python3
"""
ReAct agent bridge for /api/script-docs/generate.
Implements: web search → synthesize → return {result, status, steps_taken}.
Input: JSON on stdin {topic, context, max_steps}.
Output: JSON on stdout {result, status, steps_taken}.
"""
```

**Contratto**:
- Input: `{"topic": "...", "context": "...", "max_steps": 6}` via stdin
- Output: `{"result": "...", "status": "ok"|"partial"|"error", "steps_taken": 3}` via stdout
- Exit code 0 = success, non-zero = error (stderr catturato dal Go adapter)
- Timeout: 120s (il Go adapter usa `context.WithTimeout`)

**Dipendenze Python** (già installate nel container per `PR-FIX-PYTHON-BRIDGE-DEPS`):
- `requests` — HTTP client per ricerca web
- `sentence-transformers` — (opzionale) embedding per similarity

**Ricerca web**: DuckDuckGo HTML scrape (gratis, no API key). Fallback: SerpAPI se configurata.

**Anti-allucinazione**: se 0 risultati trovati, il bridge DEVE ritornare `"non ho trovato fonti affidabili"` — MAI inventare URL o date.

## §3 — PR-2: Go Adapter con Cache

**File**: `internal/infrastructure/react/adapter.go` (NEW)

```go
package react

// Adapter implements scriptdocs.ReActPort.
type Adapter struct {
    scriptPath string       // path to scripts/bridges/reAct_agent.py
    cache      CachePort    // research_cache repository
    mu         sync.Mutex   // serialises cache writes (test 8)
}

func (a *Adapter) Generate(ctx context.Context, req scriptdocs.ReActRequest) (scriptdocs.ReActResponse, error) {
    // 1. Compute cache key from req
    key := cacheKey(req.Topic, req.MaxSteps)

    // 2. Check cache (fast path)
    if cached, _ := a.cache.GetResearchCache(ctx, key); cached != "" {
        a.cache.TouchResearchCache(ctx, key)
        return parseResponse(cached), nil
    }

    // 3. Cache miss → execute Python bridge
    output, err := a.runPythonBridge(ctx, req)
    if err != nil {
        return scriptdocs.ReActResponse{}, err
    }

    // 4. Save to cache
    resp := parseResponse(output)
    a.cache.SaveResearchCache(ctx, key, req.Topic, detectLanguage(req.Topic), req.MaxSteps, output)

    return resp, nil
}
```

**Cache key** (deterministico, byte-stable per idempotenza):
```
SHA256(topic + "|" + strconv.Itoa(maxSteps))[:16]
```

**TTL**: `GetResearchCache` già filtra `last_used > datetime('now', '-7 days')`.

**Concorrenza**: `sync.Mutex` serializza le scritture su `SaveResearchCache` per prevenire `database is locked`.

## §4 — PR-3: Composition-Root Wiring

**File**: `internal/app/registry_public_modules.go` → `registerScriptDocs`

```go
func registerScriptDocs(registry *module.Registry, log *zap.Logger, cfg *config.Config) error {
    var port scriptdocsapi.ReActPort
    if cfg.Features.ScriptDocsEnabled {
        // Wire the Go adapter that wraps the Python ReAct bridge.
        adapter, err := react.NewAdapter(react.AdapterConfig{
            ScriptPath: filepath.Join(cfg.Paths.PythonScriptsDir, "bridges", "reAct_agent.py"),
            Cache:      scriptsRepo.NewResearchCacheAdapter(root.Repos.ScriptsRepo),
            Logger:     log,
        })
        if err != nil {
            log.Warn("script-docs: ReAct adapter construction failed, route returns 503", zap.Error(err))
        } else {
            port = adapter
        }
    }

    scriptDocsDesc, err := scriptdocsapi.Build(scriptdocsapi.Dependencies{
        Port:        port,  // was nil
        EnabledFunc: func() bool { return cfg.Features.ScriptDocsEnabled },
        ModuleOpts:  nil,
        Logger:      log,
    })
    // ...rest unchanged
}
```

**Config**: l'operatore deve impostare:
```bash
export VELOX_FEATURE_SCRIPT_DOCS_ENABLED=true
```

## §5 — PR-4: Shell Smoke (10 Test)

**File**: `tests/operational/script_docs_online_smoke.sh` (NEW, ~350 LoC)

Esegue i 10 test incollati dall'utente (2026-07-09):

| # | Test | Assertion | Exit |
|---|------|-----------|------|
| 1 | Endpoint esiste | HTTP 400 (non 404, non 503) con `{}` | 1 su 404/503 |
| 2 | LLM online reale | `status=ok`, `steps_taken>0`, `result` ha fonti | 1 su fallimento |
| 3 | Cache: seconda chiamata più veloce | `TIME(2) < TIME(1)/2` | 1 se stesso tempo |
| 4 | DB: `research_cache` ha record | `chars>500`, `last_used` recente | 1 se vuoto |
| 5 | `last_used` si aggiorna | `created_at` uguale, `last_used` cambia | 1 se no update |
| 6 | Anti-allucinazione | `result` contiene "non ho trovato" / "nessuna fonte" | 1 se inventa |
| 7 | Cache per lingua diversa | IT ≠ EN record nel DB | 1 se sovrascrive |
| 8 | Concorrenza 5 richieste parallele | Nessun 500, nessun `database is locked` | 1 su errore |
| 9 | TTL 7 giorni | Cache vecchia → richiama bridge (più lento) | 1 se usa cache stantia |
| 10 | Sweep cache >30 giorni | Record vecchio eliminato dopo sweep | 1 se ancora presente |

**Pre-flight**:
```bash
export BASE="http://127.0.0.1:8080"
export VELOX_FEATURE_SCRIPT_DOCS_ENABLED=true
```

## §6 — PR-5: Concurrency Fix

Il `sync.Mutex` su `SaveResearchCache` (PR-2) previene race condition SQLite.
Test 8 verifica che 5 richieste parallele non causino `database is locked`.

Opzionale: WAL mode + `busy_timeout=5000` già attivi nel DB set.

## §7 — Verification Gates (per ogni PR)

```bash
# PR-1: Python bridge
python3 scripts/bridges/reAct_agent.py < test_input.json  # deve funzionare standalone

# PR-2: Go adapter
go vet ./internal/infrastructure/react/...
go test -short ./internal/infrastructure/react/...

# PR-3: Composition wiring
go build ./cmd/server/
curl -s -X POST http://127.0.0.1:8080/api/script-docs/generate \
  -H "Content-Type: application/json" \
  -d '{"topic":"test"}'  # deve restituire 200 o 500, NON 503

# PR-4: Shell smoke
bash -n tests/operational/script_docs_online_smoke.sh
bash tests/operational/script_docs_online_smoke.sh  # esecuzione reale

# PR-5: Concurrency
go test -race ./internal/infrastructure/react/...
```

## §8 — Honest Scope-Lock (godlike/07)

- **NON incluso**: implementazione del ReAct loop reale (ragionamento multi-step). Il bridge fa 1 chiamata LLM con risultati di ricerca.
- **NON incluso**: SerpAPI key. DuckDuckGo scrape è il default (gratis, no key).
- **NON incluso**: TTL configurabile. Hardcoded a 7 giorni (come da specifica `research_cache`).
- **NON incluso**: logging/metrics Prometheus sul bridge Python.

## §9 — Wave-Flip Criterion

La wave `SCRIPT-DOCS-REACT-ONLINE-TEST-2026-07-09` flips a `status: shipped` quando:
- [ ] PR-1..5 tutti `status: shipped` su `origin/main`
- [ ] `tests/operational/script_docs_online_smoke.sh` passa 10/10
- [ ] `VELOX_FEATURE_SCRIPT_DOCS_ENABLED=true` nel config di dev

## §10 — Cross-References (godlike/06 SSOT)

- `internal/api/script-docs/handler.go` — SOLE canonical owner del ReActPort typed contract
- `internal/infrastructure/database/sqlite/scripts/research_cache.go` — SOLE canonical owner della cache SQLite
- `internal/app/registry_public_modules.go::registerScriptDocs` — SOLE canonical owner del composition-root wiring
- `internal/platform/config/types_misc.go:92` — SOLE canonical owner del feature flag `ScriptDocsEnabled`
- `AGENTS.md` § Script Generation Endpoints — documenta il contratto 503 pre-CUTOVER

## §11 — Lifecycle Audit-Trail

- 2026-07-09: Piano d'azione creato da Marcuss-ops (specifica test incollata). Stato: `pending`.
- Le 5 PR atterrano incrementalmente su `main` via AGENTS.md Git-Lesson-2 (direct-to-main, no branches, no `--force`).

---

**Co-authored-by**: PipelineGen Agent <agent@pipelinegen.local>
