# Collo di bottiglia delle traduzioni — diagnosi e fix (18 settembre 2026)

Sintomo: le traduzioni della pipeline `refactored` (10 lingue per run) sono lente.
Verdetto: **non è "Ollama è lento"**. Il traduttore veloce locale (Argos) era
spento, e il lavoro rimasto sull'unica istanza LLM ci arrivava da 5 pool
indipendenti contro un tetto server di 3 richieste in volo, con una chiamata
LLM **per cue** di sottotitolo.

## 1. Causa dominante — Argos non era disponibile

- `config/multilingual.yaml` → `translation_provider: argos` (default: Argos
  primario + Ollama fallback).
- `config.yaml` → `argos_python_bin: .venv-argos/bin/python3`, ma `.venv-argos`
  **non esisteva**: il log diceva
  `ArgosTranslator unavailable; using Ollama-only translation`
  (`argos bridge unavailable: .venv-argos/bin/python3 not on PATH`).
- Effetto: ogni scena/cue/lingua pagava un forward LLM che doveva costare ~0 su
  CPU.

Inoltre il tool di install (`scripts/tools/argos_install_models.py`) e l'adapter
indicavano `ARGOS_PACKAGE_DIR` (singolare), mentre argostranslate ≥ 1.9 legge
`ARGOS_PACKAGES_DIR` (plurale): un install "verde" lasciava comunque il sidecar
senza modelli.

## 2. Colli di bottiglia strutturali

| # | Collo di bottiglia | Evidenza (pre-fix) |
|---|---|---|
| 1 | Tetto di concorrenza = 1 lato Argos | `argos_server_translator.go`: `a.mu.Lock()` tenuto per l'intera HTTP request + un `GET /health` **a ogni** `Translate` |
| 2 | Stesso serializzatore dentro il sidecar | `argos_bridge/server.py`: `_translate_lock` globale attorno a ogni traduzione |
| 3 | Una chiamata LLM **per cue** | `assets/texttracks/cue_translate.go`: `Translate` per cue, con ~130 token di system prompt per 10-20 token utili |
| 4 | Traduzione voiceover seriale | `processor_voiceover.go`: doppio loop lingua→scena senza concorrenza; un solo fallimento scartava l'intera lingua |
| 5 | Nessun budget unico vs `OLLAMA_NUM_PARALLEL=3` | pool a 3/4 (`npm`/cue/materializer/NLP) tutti sullo stesso runner |
| 6 | Transport HTTP di default (`MaxIdleConnsPerHost=2`) | `platform/ollama/client/client_core.go`, `argos_server_translator.go` |
| 7 | `SetTranslationConcurrency` non esposto | già chiuso in precedenza (`config.yaml` → `scripts.translation_concurrency: 3`) |

## 3. Fix applicati

1. **Argos runtime reale**: `.venv-argos` creato da `scripts/requirements-argos.txt`
   (argostranslate 1.9.6) e 18 pacchetti `en↔{it,pl,ru,de,es,pt,fr,tr,id}`
   installati in `data/argos-packages` (repo-locale, non nella home).
2. **SSOT della directory modelli**: `paths.argos_package_dir` (default
   `data/argos-packages`) → `ArgosServerConfig.PackageDir` → `ARGOS_PACKAGES_DIR`
   nel child; il tool di install usa la STESSA variabile e **fallisce** se
   argostranslate risolve una directory diversa.
3. **Adapter Argos senza serializzazione artificiale**:
   `startMu` guarda solo il ciclo di vita (spawn/health/stop), `sem` limita la
   concorrenza; niente `/health` per chiamata (la liveness la conferma la
   richiesta stessa, `invalidate` → **un** respawn alla chiamata successiva).
4. **Sidecar Python parallelo**: `BoundedSemaphore(ARGOS_SERVER_CONCURRENCY,
   default 4)` al posto del lock globale; `/health` riporta `concurrency`.
5. **Batching dei cue** (nuovo `BatchTranslationPort`, opzionale):
   - `platform/ollama/generate_translation_batch.go`: una `/api/chat` per chunk
     (default 12), JSON-mode, cache-aware, contratto validato (id richiesti,
     niente id ignoti/duplicati/mancanti, testo non vuoto) con errore tipato
     `ErrBatchTranslationContract`;
   - `capabilities/translation/batch.go` + `ollama_translator_batch.go`: il port
     applicativo e la sua implementazione;
   - `cue_translate.go`: chunk → 1 chiamata per chunk, fallback **per-cue** solo
     per il chunk fallito (`SetBatchChunkSize(0)` disabilita il path batched).
6. **Voiceover per-lingua concorrente + isolamento per scena**:
   `translateScenesForLanguage` (fan-out bounded a 4) e skip **solo** delle
   scene la cui traduzione è fallita (mai pass-through del testo sorgente).
7. **Transport HTTP**: `MaxIdleConnsPerHost` allineato alla larghezza dei pool
   (client Ollama e client sidecar), timeout invariato.
8. **Budget di output per-cue** (`platform/ollama/generate_translation.go`):
   il vecchio budget era `4×caratteri` con **floor 512** token, e il floor era
   costo puro: misurato, per un cue di 5 parole il modello generava 294-497
   token (usava tutto il budget). Ora `translationOutputBudget` = ~2× i token
   del sorgente + 64, **floor 96**, cap 4096 (funzione pura, pinnata da test).
9. **Un solo knob per la larghezza del fan-out di traduzione**
   (`wiring.resolveCueTranslationConcurrency`): il fan-out per-cue usa
   `scripts.translation_concurrency`, lo stesso valore che limita la fase
   scene×lingua, invece di avere una seconda larghezza indipendente.

## 4. Misure (host: RTX A4000, gemma4:e2b)

12 cue di sottotitolo reali, modello residente, stesso `num_ctx=16384`:

| Percorso | Costo per cue | Note |
|---|---|---|
| Ollama per-cue (pre-fix) | **5.5 – 8.0 s** | `eval_count` 294–497 token per un cue di 5 parole |
| Ollama batched (post-fix) | **~0.95 s** | 12 cue in 10.9–12.0 s, `eval_count` 746–782, 12/12 id parsati |
| Argos locale (post-fix) | **0.10 – 0.25 s** | 12 cue in 1.2–3.0 s (`en→it` 99 ms/cue) |

Guadagno: **6–8×** dal solo batching (e 12× meno richieste, quindi 12× meno
pressione sui 3 slot), **~30–60×** dal ripristino di Argos come primario.
Il contratto JSON ha anche un effetto sul budget di output: ~62 token/cue
contro 294–497 del percorso per-cue.

Nota di misura: il primo preflight del percorso per-cue aveva dato 32.6 s per
un singolo cue (modello freddo); a caldo il valore onesto è 5.5–8.0 s.

Evidenza collaterale raccolta durante il secondo giro: a fine sessione
`ollama ps` mostrava `gemma4:e4b` residente al posto di `gemma4:e2b`
(`OLLAMA_MAX_LOADED_MODELS=1`): due consumer che chiedono modelli diversi si
sfrattano a vicenda e ognuno paga il reload del proprio modello. È la
manifestazione pratica del punto 5 della tabella (nessun budget unico fra i
pool), non un difetto del percorso di traduzione.

## 5. Verifica

```bash
# runtime Argos + modelli
ls data/argos-packages | wc -l                      # 18
VELOX_E2E_ARGOS_LIVE=1 go test ./tests/e2e \
  -run TestLiveArgosSidecarTranslatesRegistryLanguages -count=1 -v

# unit (adapter, sidecar, batching, budget, voiceover, transport)
go test ./internal/capabilities/translation/... ./internal/platform/ollama/... \
  ./internal/capabilities/assets/texttracks/... \
  ./internal/capabilities/scripts/adapters/... ./internal/app/wiring/... -count=1

# gate di repo (verde: script/ollama/translation/research/stock rilanciati)
make verify-agent
```

Attivazione in produzione: il binario è stato ricompilato in `bin/pipelinegen`;
serve solo un `systemctl restart pipelinegen.service` (sudo). Al boot il log deve
riportare `ArgosTranslator wired as primary translation provider (Ollama
fallback)` e **non** più `ArgosTranslator unavailable`.

## 6. Follow-up residui (non bloccanti)

- `OLLAMA_NUM_PARALLEL=3` e `OLLAMA_MAX_LOADED_MODELS=1` restano il tetto reale.
  La larghezza dei due percorsi di traduzione ora è unica
  (`scripts.translation_concurrency`), ma un semaforo condiviso fra i pool
  script/NLP/cue/materializer non esiste ancora — e il caso `e2b`/`e4b`
  osservato mostra che due consumer con modelli diversi si sfrattano.
- La cache L1/L2 vive dentro `ollama.Generator`: con Argos primario non c'è
  cache testuale dei risultati Argos. Non è un collo di bottiglia (0.10–0.25
  s/cue misurati) ed è già coperto a livello persistente dal gate
  `translation_key` del materializer.

## 7. Nota di sessione — workspace condiviso

Durante questo lavoro un ALTRO workstream stava editando lo stesso workspace
(localized render: `localization/`, `cliprender/`, `runner_render_units.go`,
`runner_phase_script.go`). Due test del componente `script`
(`TestExpectedRenderUnitsCountsFixedMultilingualFanout`,
`TestRunner_SourceClipsAudioNone_LocalizedRenderFanout`) fallivano per quel WIP
in corso e sono tornati verdi quando quel workstream ha finito (verificato con
un worktree su HEAD: a HEAD erano verdi, quindi la regressione non era di
questo lavoro). Nessun file di quel workstream è stato toccato qui.
