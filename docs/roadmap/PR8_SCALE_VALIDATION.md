# PR8 — Validazione della scalabilità

## Obiettivo

Dimostrare con dati riproducibili che PipelineGen regge il carico previsto, recupera dai guasti e può crescere senza perdita, duplicazione o corruzione del lavoro.

Questa fase non usa parole come “scalabile” senza numeri. Produce:

- workload dichiarato;
- test di carico;
- test multi-worker;
- failure injection;
- limiti misurati;
- capacity plan;
- soglie di alert;
- criteri chiari per mantenere SQLite o migrare a PostgreSQL;
- report finale con decisione go/no-go.

## Prerequisiti

PR8 inizia soltanto quando:

- [ ] PR7 è `verified`;
- [ ] esiste un tag release candidate;
- [ ] backup e restore sono provati;
- [ ] metriche e alert sono attivi;
- [ ] E2E critici sono verdi;
- [ ] ambiente di load test è isolato;
- [ ] dati di test non coinvolgono account o cartelle production.

## Branch e artefatti

Branch:

```text
codex/scale-validation
```

Artefatti:

```text
docs/scale/<version>/WORKLOAD.md
docs/scale/<version>/ENVIRONMENT.md
docs/scale/<version>/RESULTS.md
docs/scale/<version>/FAILURE_INJECTION.md
docs/scale/<version>/DATABASE_DECISION.md
docs/scale/<version>/CAPACITY_PLAN.md
docs/scale/<version>/GO_NO_GO.md
```

Script e configurazioni riproducibili:

```text
scripts/loadtest/**
config/loadtest.example.yaml
```

Non committare output massivi, database, media o secret. Allegare log e report completi come artifact CI o release.

## Fase 1 — Definire il workload

Prima di testare, compilare una tabella reale:

| Metrica | Target iniziale | Target 2× | Note |
|---|---:|---:|---|
| canali gestiti | 200 | 400 | rete globale prevista |
| video/giorno | da definire | 2× | usare dati reali |
| job/ora picco | da definire | 2× | separare per tipo |
| job concorrenti | da definire | 2× | CPU-only |
| richieste API/s | da definire | 2× | read/write separate |
| clip generate/giorno | da definire | 2× | con FFmpeg |
| upload Drive/giorno | da definire | 2× | considerare quota |
| query Qdrant/s | da definire | 2× | p50/p95/p99 |
| dimensione DB/mese | da definire | 2× | includere WAL |
| storage/mese | da definire | 2× | locale + Drive |

Regole:

- niente numeri inventati;
- usare almeno 7 giorni di metriche staging o produzione controllata;
- distinguere carico medio, picco e burst;
- includere i job più costosi;
- includere limiti esterni di Drive, YouTube, Artlist, Ollama e Qdrant.

## Fase 2 — Definire SLO

SLO minimi suggeriti:

```text
API availability                >= 99.5%
job completion success          >= 99.0% esclusi errori input
job duplicate completion        = 0
job lost                        = 0
p95 enqueue latency             < 500 ms
p95 status read latency         < 300 ms
p95 lightweight job start       < 30 s
worker recovery after restart   < 2 lease windows
backup restore RTO              definito e rispettato
backup restore RPO              definito e rispettato
```

Per job media pesanti, definire SLO separati per durata video, CPU e provider esterni.

TODO:

- [ ] SLO approvati;
- [ ] ogni SLO collegato a una metrica;
- [ ] finestre di misura definite;
- [ ] error budget definito;
- [ ] esclusioni documentate.

## Fase 3 — Ambiente di test

Documentare:

- CPU fisiche e virtuali;
- RAM;
- disco e IOPS;
- filesystem;
- rete;
- Docker version;
- numero server;
- numero worker;
- SQLite/Qdrant version;
- dataset size;
- image digest;
- commit SHA;
- configurazione concorrenza;
- limiti provider.

Poiché il deployment target è CPU-only, non usare GPU nei risultati ufficiali.

## Fase 4 — Dataset realistico

Il dataset deve rappresentare:

- asset YouTube;
- asset Artlist;
- transcript corti e lunghi;
- metadata completi e incompleti;
- duplicati;
- job falliti e retry;
- asset già presenti;
- query semanticamente simili;
- file piccoli e grandi.

Checklist:

- [ ] dati anonimizzati;
- [ ] generator riproducibile;
- [ ] checksum dataset;
- [ ] distribuzione documentata;
- [ ] nessuna dipendenza da production.

## Fase 5 — Load generator

Il load generator deve supportare:

- profili costanti;
- ramp-up;
- spike;
- burst;
- soak test;
- mix di job;
- idempotency key controllate;
- cancellation;
- retry client;
- output JSON/CSV.

Ogni richiesta deve registrare:

```text
timestamp
scenario
request_id
idempotency_key
status_code
latency
job_id
result
error_class
```

Non includere token o cookie.

## Fase 6 — Baseline single server + single worker

Eseguire:

1. idle baseline 15 minuti;
2. warm-up 15 minuti;
3. carico medio 30 minuti;
4. picco 30 minuti;
5. 2× target 30 minuti;
6. cooldown 15 minuti.

Registrare:

- CPU;
- RAM;
- load average;
- disk usage e IOPS;
- SQLite busy/locked;
- WAL size;
- queue depth;
- job throughput;
- p50/p95/p99;
- failure rate;
- retry rate;
- provider errors.

Exit:

- zero job persi;
- zero completamenti duplicati;
- memoria torna vicina alla baseline dopo cooldown;
- backlog rientra entro finestra definita;
- nessun file temporaneo orfano significativo.

## Fase 7 — Multi-worker

Matrice minima:

```text
1 server + 1 worker
1 server + 2 worker
1 server + 4 worker
1 server + 8 worker
```

Per ogni configurazione misurare:

- throughput;
- speedup;
- contention;
- lease lost;
- duplicate claim;
- SQLite busy;
- provider throttling;
- CPU saturation;
- disk saturation.

Regole:

- ogni job ha un solo owner alla volta;
- fencing/lease impedisce write tardive;
- due worker non completano lo stesso job;
- retry non crea asset duplicati;
- shutdown graceful restituisce o completa il lavoro.

Test espliciti:

- [ ] due worker tentano lo stesso job;
- [ ] worker muore dopo claim;
- [ ] worker muore dopo partial write;
- [ ] lease scade durante FFmpeg;
- [ ] ack finale fallisce;
- [ ] worker lento e worker veloce;
- [ ] clock skew moderato.

## Fase 8 — SQLite contention test

Misurare:

```text
write transaction duration
busy errors
busy retry duration
WAL growth
checkpoint duration
queue polling queries
job claim latency
outbox write latency
```

Scenari:

- molte letture e poche scritture;
- più writer;
- job claim concorrenti;
- update progress frequenti;
- outbox backlog;
- backup durante carico;
- checkpoint durante carico.

Verifiche:

```bash
sqlite3 <DB> 'PRAGMA journal_mode;'
sqlite3 <DB> 'PRAGMA busy_timeout;'
sqlite3 <DB> 'PRAGMA wal_checkpoint(PASSIVE);'
sqlite3 <DB> 'PRAGMA integrity_check;'
```

## Fase 9 — Decisione SQLite/PostgreSQL

Mantenere SQLite se tutte le condizioni sono vere:

- un solo host con volume locale affidabile;
- writer concorrenti controllati;
- busy rate sotto soglia;
- p95 claim latency nello SLO;
- backup/restore soddisfa RTO/RPO;
- throughput 2× target;
- nessuna necessità di più server writer.

Aprire migrazione PostgreSQL se almeno una condizione è vera:

- più host devono scrivere sullo stesso database;
- SQLite busy supera la soglia concordata;
- p95 claim latency viola SLO;
- WAL/checkpoint degrada il sistema;
- job leasing richiede locking più robusto;
- backup blocca il carico oltre RTO;
- il throughput non raggiunge 2× target pur con risorse disponibili.

La decisione deve includere numeri, non preferenze.

## Fase 10 — Provider saturation

Test separati per:

### YouTube

- rate limit;
- cookie scaduti;
- video rimosso;
- slow download;
- throttling yt-dlp.

### Artlist

- browser non disponibile;
- sessione scaduta;
- scraper lento;
- pagina cambiata;
- zero risultati.

### Drive

- 429;
- quota giornaliera;
- timeout upload;
- connessione interrotta;
- retry idempotente.

### Qdrant

- latenza alta;
- timeout;
- restart;
- collection mancante;
- partial upsert.

### Ollama/LLM

- risposta lenta;
- risposta malformata;
- memoria insufficiente;
- modello non disponibile.

Exit:

- provider lento non blocca indefinitamente worker;
- retry è limitato e jittered;
- circuit breaker o backoff evita tempesta;
- errore è classificato;
- backlog e alert sono visibili.

## Fase 11 — Failure injection

Scenari obbligatori:

1. kill worker;
2. kill server;
3. restart Qdrant;
4. rendere Drive irraggiungibile;
5. riempire disco fino alla soglia di sicurezza;
6. rendere directory read-only;
7. interrompere rete;
8. rallentare database;
9. corrompere una cache non canonica;
10. inviare payload invalido ad alto volume.

Per ogni scenario registrare:

- tempo rilevazione;
- alert emesso;
- job interessati;
- dati persi;
- duplicati;
- tempo recovery;
- intervento manuale;
- post-condizione.

Criterio:

```text
data loss = 0
duplicate terminal completion = 0
recovery entro SLO
```

## Fase 12 — Soak test

Durata minima:

```text
24 ore per RC
72 ore prima di dichiarazione stabile
```

Durante soak:

- mix realistico di job;
- restart programmato di almeno un worker;
- backup programmato;
- checkpoint database;
- provider failure simulato;
- monitoraggio file temporanei;
- monitoraggio memory leak;
- monitoraggio backlog.

Exit:

- nessuna crescita memoria non spiegata;
- nessuna crescita temp incontrollata;
- nessun job zombie;
- nessun backlog permanente;
- nessuna corruzione;
- SLO rispettati.

## Fase 13 — Capacity model

Produrre formule semplici:

```text
worker_needed = ceil(peak_job_seconds_per_second / effective_worker_capacity)
storage_monthly = avg_output_size * outputs_per_month * retention_factor
db_growth_monthly = avg_rows_per_job * jobs_per_month * avg_row_size
recovery_time = backlog_jobs / sustained_recovery_throughput
```

Documentare:

- capacità per worker CPU-only;
- punto di saturazione CPU;
- punto di saturazione disco;
- punto di saturazione provider;
- numero massimo consigliato worker per host;
- headroom minimo;
- costo stimato per 1× e 2× target.

## Fase 14 — Autoscaling policy

Anche senza orchestratore complesso, definire trigger:

Scale out quando:

- queue depth sopra soglia per N minuti;
- oldest job age sopra soglia;
- CPU media worker sopra soglia;
- p95 start latency viola SLO.

Non scalare quando:

- provider è rate-limited;
- SQLite è già il collo di bottiglia;
- disco è saturo;
- error rate indica bug, non capacità.

Scale in soltanto con:

- drain worker;
- zero job claimed o trasferimento sicuro;
- cooldown;
- minimo worker garantito.

## Fase 15 — Security under load

Verificare:

- rate limiting efficace;
- body size limits;
- auth non bypassabile sotto concorrenza;
- HMAC replay protection;
- log non esplodono;
- metric label cardinality controllata;
- invalid payload non consuma worker costosi;
- endpoint admin isolati.

## Fase 16 — Go/No-Go

### GO

Consentito soltanto se:

- target e 2× target passano;
- SLO rispettati;
- zero job persi;
- zero duplicati terminali;
- recovery test passano;
- soak test passa;
- database decision documentata;
- capacity plan approvato;
- alert e runbook aggiornati.

### CONDITIONAL GO

Consentito soltanto con:

- limite di traffico esplicito;
- rischio non data-loss;
- owner e data di correzione;
- alert specifico;
- rollback immediato.

### NO-GO

Obbligatorio se:

- perdita dati;
- duplicati;
- restore fallisce;
- SLO non rispettati al target;
- database corrotto;
- failure injection richiede fix manuale non documentato;
- metriche insufficienti.

## Exit gate finale

PR8 è `done` quando:

- [ ] workload reale definito;
- [ ] SLO definiti;
- [ ] dataset riproducibile;
- [ ] load generator versionato;
- [ ] baseline 1 worker completata;
- [ ] matrice multi-worker completata;
- [ ] contention SQLite misurata;
- [ ] decisione database approvata;
- [ ] provider saturation testata;
- [ ] failure injection completata;
- [ ] soak 72 ore completato;
- [ ] capacity model prodotto;
- [ ] autoscaling policy documentata;
- [ ] security under load verificata;
- [ ] report GO/NO-GO firmato;
- [ ] test rieseguiti sul tag candidato.

## Tag finale

Dopo GO e chiusura della Definition of Done:

```text
architecture-clean-v1
v1.0.0
```

Il tag deve puntare allo stesso commit certificato da PR7 e validato da PR8. Se il codice cambia, certificazione e scale validation devono essere rieseguite.
