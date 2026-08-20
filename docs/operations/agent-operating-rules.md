# Regole operative dell'agente (session-scoped)

Documento riutilizzabile da passare a ogni sessione agentica su PipelineGen.
Obiettivo unico: **completare il lavoro richiesto nel minor tempo possibile**,
senza attività collaterali, con report minimo e verificabile.

---

## 1. Preflight aggregato (massimo 60 secondi, UN solo comando)

Un unico comando deve controllare in sequenza:

```text
1. servizio active (systemctl is-active pipelinegen.service)
2. endpoint /ready (HTTP 200)
3. token valido (scripts/with-velox-auth o curl autenticato)
4. Drive (cartelle/asset attesi)
5. auth YouTube
6. asset necessari presenti (query unica SQLite)
7. coda outbox (query unica, non una query per asset)
```

Niente esplorazione della repository se il comando è già conosciuto.

## 2. Limiti operativi rigidi

```text
Massimo 3 comandi esplorativi iniziali.
Massimo 2 tentativi per blocker (stessa causa).
Massimo 1 modifica di codice per causa verificata.
Nessuna pulizia o rinomina non richiesta.
Nessuna nuova ricerca se esiste già un registry.
Un solo poller per tutti i job.
Query SQL aggregate, non una query per asset.
```

## 3. Esecuzione diretta

Chiamare direttamente l'endpoint corretto per il processo richiesto:

```text
stock      → /api/stock-pipeline/*
script     → /api/script/generate
voiceover  → endpoint voiceover
clips      → /api/clips/process
```

Non cambiare obiettivo durante il test.

## 4. Poll automatico (UN solo poller)

Non usare cicli manuali di `sleep` + query SQL. Per interrogare lo stato di un
job usare l'endpoint canonico esposto dal servizio (es. `/api/jobs/{id}`) e
fermarsi al primo cambio di stato osservato. Se in futuro servirà una CLI
dedicata, vivrà in `scripts/tools/` con un contratto chiaro di stampa dei
soli cambi di stato:

```text
PENDING → RUNNING
progress 10% → 40%
RUNNING → SUCCEEDED
```

Nessuna nuova analisi a ogni controllo.

## 5. Non aspettare tutti gli asset

Per un canary non serve attendere l'elaborazione completa di asset non usati
dal test (es. 75 asset in indicizzazione). Sono sufficienti i minimi necessari:

```text
3 asset ACTIVE + INDEXED per soggetto (fight / interview / training)
5 soggetti × 3 asset = 15 asset
```

Il test completo della coda può essere eseguito separatamente.

## 6. Tentativi e cause

```text
Per ogni causa di errore: massimo 2 tentativi.
Dopo il secondo tentativo: identifica la causa precisa
e passa alla soluzione più diretta.
```

## 7. Modifiche al codice

Modificare il codice SOLO quando:

```text
1. il difetto è riproducibile;
2. non è risolvibile tramite configurazione (fixture, payload, flag).
```

Se la causa è configurabile, correggere la configurazione, non il codice.

## 8. Run separati e indipendenti

Non mescolare in un unico run attività diverse. Usare run separati:

```text
RUN 1 — prepare-stock
RUN 2 — verify-index
RUN 3 — generate-scripts
RUN 4 — voiceovers
```

Se RUN 3 fallisce, non si riparte da YouTube o dalla pulizia Drive:
si diagnostica e si corregge la causa di RUN 3 soltanto.

## 9. Regola del PASS

```text
Continua fino a PASS oppure fino a un blocker tecnico verificato,
con comando, errore e componente responsabile.
```

- Non utilizzare fallback tra soggetti (es. clip di Tyson al posto di Pacquiao).
- Non produrre falsi PASS.

## 10. Report finale (standardizzato, SOLO questi elementi)

Ogni run termina con UN report nel formato fisso qui sotto. Niente altro:
niente analisi libere, niente cronologia, niente riassunti di esplorazione.

```text
RUN N — <nome run>
Durata totale:      <mm:ss>
Risultato:          PASS oppure blocker
Fase più lenta:     <fase> (<durata>)
Job falliti:        per ciascuno:
  - job_id | comando | errore | componente responsabile
Prossimo intervento: <azione concreta e verificabile, con comando>
```

Regole obbligatorie del report:

```text
Nessun fallback tra soggetti (es. clip di Tyson al posto di Pacquiao).
Niente falsi PASS: SUCCEEDED_WITH_WARNINGS in strict mode NON è un PASS;
un gate eluso o un job con warning non può essere dichiarato PASS.
Ogni esito diverso da PASS si riporta come blocker, sempre con
comando + errore + componente responsabile.
Blocker = motivo tecnico verificato con comando, errore e componente.
```

---

## Riferimenti

- Regole di engineering e Git: `AGENTS.md`
- Mappa delle fonti canoniche: `CANONICAL.md`
- Procedure operative: `docs/operations/`
