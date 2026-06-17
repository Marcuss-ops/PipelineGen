# Agent Script Writer — Nuovo Flusso Autonomo (Gemma 4)

> ⚠️ **PARZIALMENTE ARCHIVIATO — Giugno 2026**  
> Il **flow agent Python in-loop** (`HandleSourceScriptGenerateJob`, job type `script.generate_from_source`, endpoint legacy `/api/script/generate-from-source`) **è stato rimosso** e la pipeline unificata è ora `HandleClipScriptGenerateJob` (job type `script.generate_from_clips`).  
> L'endpoint HTTP `POST /api/script/generate-with-images` **esiste ancora** (registrato in `handler_flow.go:153` accanto a `/generate-from-clips`); **non** è un alias backward-compat di `/generate-from-clips` — è un endpoint dedicato con handler `GenerateWithImages` (`handler_generate_with_images.go`) e request type `GenerateWithImagesRequest`, che forza un preset di payload (`extract_entities=false`, `generate_scene_images=true`, `generate_metadata=false`). Entrambi gli endpoint condividono però lo stesso job type e la stessa pipeline.  
> Lo script Python `scripts/agent_script_writer.py` è ancora disponibile per uso CLI/manuale e viene invocato dall'endpoint sincrono `/api/script-docs/generate` (sezione §4.2) quando `auto_research: true` e `source_text` è vuoto.  
> **Per il flow asincrono corrente, leggi `docs/SCRIPT_PIPELINE.md`.**

---

## 1. Panoramica

L'**Agent Script Writer** è uno strumento Python che genera autonomamente script YouTube a partire da un **topic** (argomento), senza richiedere un `source_text` pre-scritto dall'utente.

L'agent opera in un ciclo **ReAct** (Reasoning + Acting):
1. **Pensa** (THOUGHT) — valuta cosa sa e cosa gli manca
2. **Agisce** (ACTION) — cerca sul web (`SEARCH`), legge pagine (`READ`) o segnala che è pronto (`WRITE`)
3. **Osserva** — riceve i risultati della ricerca e ripete il ciclo
4. **Scrive** — quando ha abbastanza informazioni, genera lo script finale

---

## 2. Cosa è cambiato rispetto a prima

| Aspetto | Prima (pre-Giugno 2026) | Dopo (Gemma 4 + Agent) |
|---|---|---|
| **Input richiesto** | `source_text` obbligatorio — l'utente doveva fornire un testo sorgente già scritto | Solo `topic` — l'agent ricerca e scrive autonomamente |
| **Ricerca** | Nessuna. Il testo sorgente era statico | Ricerca web autonoma via SearXNG + cache SQLite |
| **Modello LLM** | `gemma3` o misti | `gemma4:e4b` (default) — molto più capace nel reasoning |
| **Prompt** | Italiano, spesso confuso con JSON | Inglese, formato ReAct (`THOUGHT:` / `ACTION:`), molto più robusto |
| **Formato intermedio** | JSON fragile tra LLM e Go | Nessun JSON intermedio — plain text + parsing regex robusto |
| **Batch mode** | Non esisteva | Genera N sotto-script su subtopic diversi e li unifica |
| **Cache ricerca** | Non esisteva | Cache SQLite persistente (`data/agent_cache.db`, TTL 48h) |
| **Espansione lunghezza** | Nessuna garanzia | `_ensure_min_words()` espande automaticamente se lo script è troppo corto |
| **Timeout agent** | ~2 min (veniva killato dal job worker) | 20 minuti dedicati (context indipendente) |

---

## 3. Architettura ReAct al dettaglio

### 3.1 System Prompt

Ogni chiamata all'LLM inizia con un system prompt che impone il formato ReAct:

```
THOUGHT: [Cosa so, cosa mi manca, piano di ricerca]
ACTION: [One command: SEARCH("query") or READ("https://url") or WRITE()]
```

Il LLM **decide autonomamente** se serve ricerca:
- **Topic generico/conoscenza** (es. "How does photosynthesis work") → `WRITE()` immediato
- **Topic attuale** (es. "Le auto elettriche cinesi nel 2026") → `SEARCH()` per dati recenti

### 3.2 Ciclo esatto

```
Utente fornisce topic → LLM decide (THOUGHT + ACTION)
                          │
                          ▼
              ┌─────────────────────┐
              │  ACTION: SEARCH()   │──→ SearXNG → cache SQLite → risultati al LLM
              │  ACTION: READ()     │──→ Trafilatura → cache SQLite → testo al LLM
              │  ACTION: WRITE()    │──→ Generazione finale script
              └─────────────────────┘
                          │
                          ▼
              Max 10 step (default) → se non è pronto, best-effort
```

### 3.3 Ricerca web (SearXNG)

- **URL default**: `http://localhost:8080` (SearXNG locale)
- **Cache**: SQLite `data/agent_cache.db` con TTL 48h. Se la query è già in cache, risponde in ~14ms.
- **Risultati**: massimo 5 risultati per query (titolo, URL, snippet 400 char)

### 3.4 Lettura pagine

- Usa `trafilatura` per estrarre testo pulito da HTML
- Massimo 8.000 caratteri per pagina
- Anche le pagine lette vengono salvate in cache SQLite

### 3.5 Generazione finale

Quando il LLM segnala `WRITE()`, l'agent fa **una chiamata LLM dedicata** (`generate_final_script`):
- Passa **tutta la ricerca raccolta** (fino a 25.000 caratteri) nel contesto
- Prompt di sistema: "Write in {lang}. No headers, no JSON, just the full script text."
- Temperatura: 0.35 (fattuale ma creativo)
- `num_predict`: 16.384 token (default)

Se lo script risultante è sotto il `min_words` richiesto, `_ensure_min_words()` chiede all'LLM di espanderlo (~+500 parole).

---

## 4. Integrazione con i due endpoint API

### 4.1 Endpoint asincrono (script generation, alias di generate-from-clips)

**Endpoint**: `POST /api/script/generate-from-clips` (canonico).  
L'endpoint `POST /api/script/generate-with-images` **esiste ancora** ma è dedicato (handler `GenerateWithImages` con request type separato `GenerateWithImagesRequest`); forza `generate_scene_images=true, extract_entities=false, generate_metadata=false`. Non è un alias di `/generate-from-clips` (vedi `AGENTS.md` tabella endpoint). Da evitare per questo caso d'uso.

**Handler**: `internal/api/handlers/script/handlers/handler_flow.go` → job handler `job_handler.go` (entry point `HandleClipScriptGenerateJob`)

**Flusso** (pipeline unificata, vedi `internal/api/handlers/script/handlers/job_handler_clip_source.go`):
```
1. Client POST con {topic, language, num_clips, ...} (GenerateFromClipsRequest)
2. Go enqueua un job nel sistema jobs (`script.generate_from_clips`)
3. Job worker esegue `HandleClipScriptGenerateJob()` (testo-only / clip-aware / auto-search)
4. ├─ Path 1: clip_ids presenti → `ClipSourceBuilder.BuildClipContext()` → engine.WriteScript
   ├─ Path 2: num_clips > 0, no clip_ids → `mediaCurator.Curate()` → auto-search + WriteScript
   └─ Path 3: nessun clip → text-only → engine.WriteScript con plan
5. Se richiesto: entity extraction + insights (parallel) + YouTube metadata
6. Google Doc (sempre creato)
7. Job completato → result includes script, word_count, doc_url, etc.
```

**Parametri rilevanti** (schema `GenerateFromClipsRequest`):

```json
{
  "topic": "Le auto elettriche cinesi nel 2026",
  "language": "it",
  "duration": 180,
  "min_words": 400,
  "num_clips": 0,
  "extract_entities": true,
  "generate_metadata": true
}
```

> **Nota**: i campi legacy `agent_max_steps`, `agent_min_words`, `agent_batch`,
> `agent_model` **non sono più supportati** in questo endpoint. L'agent
> script writer Python è ancora disponibile tramite il flusso
> `/api/script-docs/generate` (sincrono, vedi §4.2) o invocato manualmente
> via CLI per popolare `source_text` da passare poi a `/generate-from-clips`.

### 4.2 Endpoint sincrono (solo testo / script-docs)

**Endpoint**: `POST /api/script-docs/generate`

**Handler**: `internal/api/handlers/script/handlers/handler_generate.go`

**Flusso**:
```
1. Client POST con {topic, language, auto_research: true}
2. Go crea un context HTTP con timeout 15 min
3. ├─ Se source_text è vuoto e auto_research=true → esegue agent
   │   ├─ Context indipendente di 20 minuti (background, non HTTP-bound)
   │   ├─ Agent produce source_text
   │   └─ req.SourceText viene popolato
   ├─ `BuildScriptDocument()` — genera il documento strutturato (timeline, scene)
   ├─ Harvest background (opzionale)
   ├─ Upload Google Doc
   ├─ Voiceover (opzionale)
   └─ Risposta JSON al client
```

**Parametri rilevanti**:

```json
{
  "topic": "Le auto elettriche cinesi nel 2026",
  "language": "it",
  "auto_research": true,
  "agent_max_steps": 5,
  "agent_min_words": 1000,
  "agent_batch": 0,
  "agent_model": ""
}
```

> ⚠️ **Nota**: questo endpoint è **sincrono**. Il client deve usare un timeout lungo (minimo 2-4 minuti) oppure il server configura `read_timeout: 600` e `write_timeout: 600` in `config.yaml`.

---

## 5. CLI dello script Python

Puoi eseguire l'agent anche manualmente da riga di comando:

```bash
# Default (inglese, 1500 parole, max 10 step)
python3 scripts/agent_script_writer.py "Latest AI regulation in Europe 2026"

# Italiano, modello esplicito, 2000 parole minime
python3 scripts/agent_script_writer.py --model gemma4:e4b --min-words 2000 --lang it "Space exploration 2026"

# Batch mode: 3 subtopic che vengono unificati in uno script coeso
python3 scripts/agent_script_writer.py --batch 3 "The future of electric vehicles"

# Silenzioso (solo output finale su stdout, metadati su stderr)
python3 scripts/agent_script_writer.py --quiet --output /tmp/script.txt "My topic"

# Gestione cache
python3 scripts/agent_script_writer.py --clear-cache
python3 scripts/agent_script_writer.py --no-cache "Fresh research topic"
```

---

## 6. Tabella parametri completi

| Parametro | Tipo | Default | Descrizione |
|---|---|---|---|
| `topic` | string | — | Argomento dello script (obbligatorio) |
| `--model` | string | `gemma4:e4b` | Modello Ollama da usare |
| `--lang` | string | `en` | Lingua di output (`it`, `en`, `es`, …) |
| `--max-steps` | int | `10` | Step massimi del ciclo ReAct |
| `--min-words` | int | `1500` | Lunghezza minima dello script in parole |
| `--batch` | int | `0` | Se > 0, genera N sub-script e li unifica |
| `--ollama-url` | string | `http://localhost:11434` | URL del server Ollama |
| `--searxng-url` | string | `http://localhost:8080` | URL del server SearXNG |
| `--quiet` | flag | — | Sopprime i log di progresso |
| `--output` | string | — | Salva lo script su file invece di stdout |
| `--no-cache` | flag | — | Disabilita la cache SQLite |
| `--clear-cache` | flag | — | Svuota la cache ed esce |
| `--cache-ttl` | int | `48` | Ore di TTL per la cache |
| `--cache-db` | string | `data/agent_cache.db` | Path del database SQLite cache |

---

## 7. Come interfacciarsi correttamente (per i nuovi agenti)

### 7.1 Se stai modificando il server Go

1. **Non usare `c.Request.Context()` per l'agent** — il context HTTP ha un timeout breve. Usa invece:
   ```go
   agentCtx, agentCancel := context.WithTimeout(context.Background(), 20*time.Minute)
   defer agentCancel()
   cmd := exec.CommandContext(agentCtx, "python3", args...)
   ```

2. **Sempre `--quiet`** quando chiami l'agent dal server, per evitare log eccessivi.

3. **Sempre `defer os.Remove(tmpFile)`** per pulire il file temporaneo.

4. **Sanitizza `Topic` e `AgentModel`** — controlla che non inizino con `-` per prevenire injection di argomenti.

5. **Default `AgentMaxSteps`**: nel job handler = 10, nel generate sincrono = 5. Se cambi uno, valuta se cambiare anche l'altro.

### 7.2 Se stai modificando lo script Python

1. **Non rompere il formato ReAct** — il parser (`parse_action`) cerca `THOUGHT:` e `ACTION:` in varie forme. Se cambi il system prompt, assicurati che il parser sia aggiornato.

2. **Non cambiare il modello di default** senza testarlo — `gemma4:e4b` è il default perché è stato validato end-to-end. Altri modelli potrebbero non rispettare il formato ReAct.

3. **La cache è automatica** — non c'è bisogno di logica extra. `tool_web_search` e `tool_read_page` gestiscono cache hit/miss automaticamente.

4. **Batch mode è pesante** — genera N script separati + 1 chiamata di unificazione. Usala con `max_steps` ridotti per ogni sub-script.

### 7.3 Se stai aggiungendo un nuovo endpoint che usa l'agent

Segui questo pattern standard (già usato in entrambi gli handler):

```go
// 1. Verifica che l'agent debba partire
if strings.TrimSpace(req.SourceText) == "" && req.AutoResearch {
    // 2. Imposta default
    if req.AgentMaxSteps <= 0 { req.AgentMaxSteps = 5 }
    if req.AgentMinWords <= 0 { req.AgentMinWords = 1500 }

    // 3. Costruisci args
    tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("agent_%d.txt", time.Now().Unix()))
    defer os.Remove(tmpFile)
    args := []string{
        filepath.Join(scriptDir, "agent_script_writer.py"),
        "--quiet",
        "--max-steps", strconv.Itoa(req.AgentMaxSteps),
        "--min-words", strconv.Itoa(req.AgentMinWords),
        "--lang", req.Language,
        "--output", tmpFile,
        req.Topic,
    }

    // 4. Esegui con context indipendente
    agentCtx, agentCancel := context.WithTimeout(context.Background(), 20*time.Minute)
    defer agentCancel()
    cmd := exec.CommandContext(agentCtx, "python3", args...)
    out, err := cmd.CombinedOutput()
    // ...gestione errori...

    // 5. Leggi output
    data, _ := os.ReadFile(tmpFile)
    req.SourceText = strings.TrimSpace(string(data))
}
```

---

## 8. Tempi tipici di esecuzione

| Fase | Tempo tipico | Collo di bottiglia |
|---|---|---|
| Ricerca web (SearXNG) | 2-10s per query | Rete / SearXNG |
| Cache hit | ~14ms | Nessuno |
| Chiamata LLM (reasoning) | 10-30s per step | Ollama locale (GPU) |
| Generazione finale script | 20-60s | Ollama locale (GPU) |
| Espansione `_ensure_min_words` | +30-60s | Ollama locale (GPU) |
| **Totale agent** | **~1-3 minuti** | GPU Ollama |
| Pipeline Go (immagini, voiceover, doc) | **~5-10 minuti** | Generazione immagini (Ollama FLUX) |

---

## 9. Troubleshooting comune

| Sintomo | Causa probabile | Soluzione |
|---|---|---|
| `signal: terminated` nel log | Timeout troppo breve del context | Usa `context.Background()` con 20m, non `c.Request.Context()` |
| `Empty reply from server` | Client chiude la connessione prima | Aumenta `--max-time` del curl o timeout server in `config.yaml` |
| Script troppo corto | `_ensure_min_words` non attivo o modello non obbedisce | Verifica che `min_words` sia passato correttamente |
| Ricerca troppo lenta | SearXNG freddo / nessuna cache | Verifica che SearXNG sia attivo su `:8080` |
| `trafilatura not installed` | Dipendenza mancante | `pip install trafilatura` |
| Script generico / senza fatti | LLM non ha fatto ricerca | Verifica che il topic sia attuale e che il system prompt non sia stato alterato |

---

## 10. File correlati

| File | Ruolo |
|---|---|
| `scripts/agent_script_writer.py` | Agente Python autonomo (ReAct) |
| `internal/api/handlers/script/handlers/handler_flow.go` | Handler principale: registra `/generate-from-clips`, `/generate-with-images` (dedicato, separato), `/generate-batch`, `/generate-from-catalog`, `/curate`. `/generate-with-images` NON è alias di `/generate-from-clips` — chiama `h.GenerateWithImages` in `handler_generate_with_images.go`. |
| `internal/api/handlers/script/handlers/job_handler.go` | Registrazione job handler per `script.generate_from_clips` |
| `internal/api/handlers/script/handlers/job_handler_clip_source.go` | `HandleClipScriptGenerateJob` — pipeline unificata |
| `internal/api/handlers/script/handlers/handler_generate.go` | Handler sincrono (`/api/script-docs/generate`) |
| `internal/api/handlers/script/script_docs_types.go` | Tipi `ScriptDocsRequest` con campi agent |
| `internal/ml/ollama/generate.go` | Generazione testo Ollama nel server Go |
| `config.yaml` | `read_timeout`, `write_timeout`, `script_docs_enabled` |
| `data/agent_cache.db` | Cache SQLite persistente per ricerche e pagine |

---

*Documento aggiornato a Giugno 2026. Per modifiche all'agent, aggiornare questo file e verificare end-to-end prima del deploy.*
