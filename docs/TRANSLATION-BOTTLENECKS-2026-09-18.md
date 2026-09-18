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
| 5 | Nessun budget unico vs `OLLAMA_NUM_PARALLEL=3` | pool a 3/4 (NLP/cue/materializer/script) tutti sullo stesso runner — **chiuso al terzo giro**, vedi fix 10 |
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
10. **Budget di ammissione unico verso il server Ollama**
    (`platform/ollama/client/admission.go`, terzo giro): i pool non possono
    vedersi (sono costruiti da wiring bundle diversi), ma **tutti** passano dal
    client Ollama; quindi il tetto vive lì. Un limiter per **endpoint**
    (normalizzato: `http://host:porta`), condiviso da ogni `*Client` che punta
    a quell'endpoint — l'LLM client, l'embed client, lo stock enrichment e il
    creator client condividono un'unica quota.
    - coperti: `/api/chat`, `/api/generate` (incluso lo **streaming**, che tiene
      lo slot fino all'ultimo token), `/api/embeddings`;
    - **non** coperti di proposito: le probe di liveness/inventario
      (`/api/tags`, `/api/ps`, `/api/show`) — non occupano uno slot del runner e
      un budget saturo non deve far sembrare morto il server;
    - valore: `VELOX_OLLAMA_MAX_INFLIGHT` → altrimenti `OLLAMA_NUM_PARALLEL`
      (lo stesso drop-in di `ollama.service`) → altrimenti 3; clamp 1–64, i
      valori assurdi (`0`, negativi, non numerici) **non** significano
      "illimitato" né "serializza tutto";
    - observability senza nuovo I/O pubblico: `Client.AdmissionStats()` espone
      `Limit/InFlight/Peak/Deferred/Granted` per endpoint, e la saturazione
      viene loggata a livello debug una volta per richiesta accodata.
12. **Traduzione vuota = fallimento, non successo silenzioso** (quarto giro).
    Inventario dei percorsi, verificato nel codice:

    | Percorso | Prima | Ora |
    |---|---|---|
    | Cue per-cue (`texttracks/cue_translate.go`) | accettava `""` con `nil` | **1 retry** poi `ErrEmptyTranslation` (con numero di cue) |
    | Chunk batched (`applyBatchTranslations`) | già rifiutava il vuoto | invariato |
    | Materializer (`materializer.go`) | scriveva una track READY **vuota** | `ErrTranslationFailed` che avvolge `ErrEmptyTranslation` |
    | Voiceover (`processor_voiceover.go`) | già falliva | invariato |
    | Usecase/postprocessor | già falliva (`ErrTranslationEmpty`) | invariato |
    | Adapter di wiring (`scriptGenerationTranslator`) | già falliva | ora **pinnato da test** (non era coperto) |

    Perché conta: nel materializer l'empty finiva in una TextTrack `READY` con
    `TextHash` reale, **sotto la stessa `translation_key`** che il gate
    lookup-before-translate usa per il riuso — cioè ogni run successivo avrebbe
    riusato il vuoto come traduzione certificata. Nei cue per-cue produceva
    sottotitoli senza parole con il run verde.

    Retry: **uno**, non un loop. Un vuoto di Ollama è tipicamente una
    generazione degenere (budget speso nel canale di reasoning) e il secondo
    campione lo risolve; un provider deterministico continua a rispondere vuoto
    e il fan-out per-cue non deve trasformarlo in una tempesta di retry sullo
    stesso slot.

13. **Metriche del budget di ammissione** (`observability/metrics_ollama.go` +
    adapter di composition in `app/wiring`): `ollama_admission_limit`,
    `ollama_admission_in_flight`, `ollama_admission_deferred_total` per
    endpoint. Definizioni nel package canonico (regola
    `prometheus_boundary`: le var metriche stanno in
    `internal/platform/observability`), mentre il client espone un port
    consumer-side (`AdmissionObserver`) e **non** importa la telemetria. La
    label è solo `endpoint` (uno o due valori per deployment): niente job id,
    niente model tag. Lettura operativa: `in_flight == limit` con
    `deferred_total` in crescita significa che i pool si stanno sovrapponendo
    ed è il server a fare da collo di bottiglia.

11. **Guard statico anti-bypass** (`client/ollama_endpoint_guard_test.go`): un
    test di architettura cammina i sorgenti Go di `internal/` e `cmd/`, li
    parsa **senza commenti** e fallisce se un package fuori da
    `internal/platform/ollama` nomina in un literal uno degli endpoint di
    generazione. Senza questo, un singolo package che si aprisse un
    `http.Client` proprio tornerebbe a saturare il server mentre i pool
    sembrano limitati. Il guard ha un secondo test che prova che **sa**
    fallire (fixture in una temp dir) e ignora gli stessi token dentro i
    commenti.

## 4. Misure (host: RTX A4000, gemma4:e2b)

12 cue di sottotitolo reali, modello residente, stesso `num_ctx=16384`:

| Percorso | Costo per cue | Note |
|---|---|---|
| Ollama per-cue (pre-fix) | **5.5 – 8.0 s** | `eval_count` 294–497 token per un cue di 5 parole |
| Ollama batched (post-fix) | **~0.95 s** | 12 cue in 10.9–12.0 s, `eval_count` 746–782, 12/12 id parsati |
| Argos locale (post-fix) | **0.10 – 0.25 s** | 12 cue in 1.2–3.0 s (`en→it` 99 ms/cue) |

Guadagno: **6–8×** dal solo batching (e 12× meno richieste, quindi 12× meno
pressione sui 3 slot), **~30–60×** dal ripristino di Argos come primario.

Budget di ammissione (fix 10), misurato contro un server di prova che traccia
la concorrenza reale lato server:

| Scenario | Limite | Picco osservato al server | Accodate |
|---|---|---|---|
| 9 `Chat` concorrenti, un client | 3 | **3** (non 9) | 6 |
| 6 `Chat` su **due** client dello stesso endpoint | 2 | **2** | 4 |
| 6 `GenerateWithOptions` (path legacy) | 2 | **2** | 4 |
| 1 embedding + 1 chat su client diversi, stesso endpoint | 1 | **1** | 1 |
| **pool cue reale** (`CueTranslator`, larghezza 4) su 12 cue, budget 2 | 2 | **2** | >0 |

Sempre verificato: dopo ogni errore 5xx e dopo un waiter cancellato, gli slot
tornano a 0 (nessuna capacità persa) e la chiamata successiva procede.

L'ultima riga è un test di integrazione che usa il fan-out cue **vero**
(`TestCueTranslationFanoutIsBoundedByOllamaAdmissionBudget`): una larghezza
volutamente più larga del budget dimostra che il tetto è del client e non del
pool.

**Chi è la fonte di verità sulla concorrenza.** Nello stesso test
`observability.ConcurrencyStats.MaxObserved` riporta **4** (la larghezza del
pool) mentre il server vede **2**. Non è un bug del tracker: conteggia il lavoro
sovrapposto, e un task bloccato sul budget è, per il pool, ancora in corsa — il
pool non vede dentro il client e non deve. Conseguenza operativa: `AvgObserved`
non dice quanto sta lavorando la GPU, mentre `Client.AdmissionStats()`
(`Peak`/`Deferred`) sì. Se in un run live `Peak == Limit` con `Deferred` in
crescita, i pool si stanno sovrapponendo ed è il server a fare da collo di
bottiglia — è esattamente il caso misurato qui.
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

# budget di ammissione + guard anti-bypass
go test ./internal/platform/ollama/client/ -run 'TestAdmission|TestOnlyOllamaClientPackageTargets|TestOllamaEndpointGuard' -v -count=1

# integrazione: il fan-out cue reale rispetta il budget condiviso
go test ./internal/capabilities/assets/texttracks/ -run TestCueTranslationFanoutIsBoundedByOllamaAdmissionBudget -v -count=1

# traduzione vuota = fallimento (cue, materializer) + adapter di wiring pinnato
go test ./internal/capabilities/assets/texttracks/ -run 'TranslationEmpty|TestCueTranslationRetries|TestCueTranslationFailsClosed|TestCueTranslationBatchedEmpty' -v -count=1
go test ./internal/app/wiring/ -run 'TestScriptGenerationTranslator' -v -count=1

# metriche del budget di ammissione
go test ./internal/platform/ollama/client/ -run TestAdmissionObserver -v -count=1
go test ./internal/app/wiring/ -run 'TestOllamaAdmissionObserver|TestObserveOllamaAdmission' -v -count=1

# gate di repo (verde: script/ollama/translation/research/stock rilanciati)
make verify-agent
```

### Attivazione live (verificata il 18 settembre, 11:31)

Il processo live è stato **riavviato con il binario ricompilato**
(`bin/pipelinegen` 11:31:06, PID 574112 avviato 11:31:12) e sta eseguendo i fix
di tutti e tre i giri. Evidenza raccolta dal servizio in esecuzione:

```
11:31:14  wiring/build_bundles_texttracks.go:230
          "ArgosTranslator wired as primary translation provider (Ollama fallback)"
11:31:48  translation/argos_server_translator.go:160  "argos: launching persistent server"
11:31:49  translation/argos_server_translator.go:231  "argos: server started" pid=580770
11:31:14  script_generation_runtime.go:412  nlp_concurrency=4 translation_concurrency=3
                                             tts_concurrency=4 serial_mode=false
```

- **zero** occorrenze di `ArgosTranslator unavailable` dopo il restart (prima
era la riga di ogni avvio);
- il sidecar in esecuzione risponde su `127.0.0.1:37873` con
  `{"status":"ok","concurrency":4,"threaded":true}` — cioè la concorrenza
  bounded è attiva anche nel processo reale;
- traduzione reale eseguita sul sidecar del servizio:
  `"Discipline builds the foundation."` → `"La disciplina costruisce la
  fondazione."` (`model: argos-en-it`, `via: direct`, nessun LLM coinvolto);
- il binario in esecuzione contiene il budget di ammissione (la stringa di log
  `ollama admission budget saturated` è presente in `bin/pipelinegen`), quindi
  i pool del job live condividono già il tetto unico.

Nessun `systemctl restart` manuale è stato necessario in questo giro: il
servizio era già stato riavviato dal supervisor con il binario aggiornato. Se in
futuro serve: `sudo systemctl restart pipelinegen.service`, e al boot il log deve
mostrare le righe qui sopra.

## 6. Follow-up residui (non bloccanti)

- `OLLAMA_NUM_PARALLEL=3` resta il tetto reale ed è ora **applicato lato
  client** (fix 10) per tutti i pool insieme. Se l'operatore cambia il drop-in
  di `ollama.service`, il client lo segue via `OLLAMA_NUM_PARALLEL` o
  `VELOX_OLLAMA_MAX_INFLIGHT`.
- **Model thrash: diagnosi definitiva (chiusa come diagnosi + leve).**
  Il problema è distinto dal conteggio delle richieste: il budget limita quanti
  request sono in volo, non quale modello è residente. Verificato nel codice:

  | Consumer | Modello effettivo in produzione | Fonte |
  |---|---|---|
  | Script generation (`model: auto`) | `gemma4:e2b` se il target è ≤ **300** parole, altrimenti `gemma4:e4b` | `generation/plan_builder.go::resolveModelPolicy` (hardcoded) |
  | Script generation (`model` pinnato) | il modello del payload | stesso funzione, ramo `!item.ModelAuto` |
  | Semantic analyzer | `cfg.External.OllamaModel` (e4b) | `lifecycle_scheduler.go` passa `Model:` esplicitamente |
  | Youtube metadata | `OllamaMetadataModel` → `OllamaModel` (e4b) | `youtube/adapters/metadata_service_helpers.go` |
  | Classifier | il modello passato dal chiamante | `classifier.Options.Model` |

  Cioè: dei cinque default `gemma4:e2b` presenti nel repo, **uno solo è vivo in
  produzione** (`plan_builder` sul ramo `auto` con script corti); gli altri sono
  fallback che il composition root non raggiunge mai, perché passa sempre
  `cfg.External.OllamaModel`. Non servono modifiche di codice lì.

  Conseguenza operativa con `OLLAMA_MAX_LOADED_MODELS=1` (drop-in
  `scripts/systemd/ollama.service.d/gpu.conf`): un payload `model: auto` con
  script corti costringe uno swap completo di modello; un payload che **pinna**
  il modello (come fa `ops/jobs/mike_tyson_1000w_5scene_10lang.generate.json`,
  `"model": "gemma4:e4b"`) ha thrash zero — ed è coerente con `ollama ps` che
  durante quel run mostrava un solo modello residente.

  Leve disponibili, in ordine di rischio:
  1. **pinnare `model` nel payload** (`"model": "gemma4:e4b"`, contratto già
     esistente, zero codice) — è la leva che elimina il thrash oggi;
  2. `external.ollama_metadata_model` per il solo percorso metadata;
  3. alzare `OLLAMA_MAX_LOADED_MODELS` nel drop-in: **non fatto qui** perché il
     file motiva esplicitamente il valore 1 (la GPU è condivisa col renderer
     media, `e2b` ≈1.9 GiB VRAM con 3 slot da 8192 token);
  4. rimuovere la policy small/large: è una decisione costo/qualità già
     pinnata da `plan_builder_contract_test.go`, quindi va decisa dall'operatore
     o dal prodotto, non cambiata in silenzio. Il modello risolto è comunque
     visibile nei log (`engine_generate.go`: `model=`), quindi il thrash si
     diagnostica da un run reale senza strumentazione nuova.
- Misurare il budget in un run live: `Client.AdmissionStats()` dà
  `Peak`/`Deferred` per endpoint (`Peak == Limit` con `Deferred > 0` significa
  che il tetto sta mordendo e che i pool si stavano sovrapponendo).
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

Terzo giro (budget di ammissione): a fine sessione il servizio è stato riavviato
con il binario aggiornato — è la stessa modifica a essere ora live, non una
sessione separata. Il job `mike-tyson-1000w-5scene-10lang` in corso alle 11:36 ha
quindi eseguito la fase di traduzione con Argos primario e con i pool sottoposti
al tetto unico.
