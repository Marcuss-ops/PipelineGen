# PR8 — Database Decision: SQLite vs PostgreSQL v1.0.0-rc.1

## Criteri per mantenere SQLite

SQLite è la scelta corretta se TUTTE le condizioni sono vere:

- [x] Un solo host con volume locale affidabile
- [x] Writer concorrenti controllati (≤4 worker)
- [ ] Busy rate sotto soglia (da misurare)
- [ ] p95 claim latency < 500ms (SLO)
- [ ] Backup/restore soddisfa RTO < 1h
- [ ] Throughput 2× target raggiunto
- [x] Nessuna necessità di più server writer

## Criteri per migrare a PostgreSQL

Aprire migrazione se ALMENO UNA condizione è vera:

- [ ] Più host devono scrivere sullo stesso database
- [ ] SQLite busy supera la soglia (da definire, suggerito: >1%)
- [ ] p95 claim latency viola SLO (500ms)
- [ ] WAL/checkpoint degrada il sistema (>5s checkpoint)
- [ ] Job leasing richiede locking più robusto (SELECT FOR UPDATE)
- [ ] Backup blocca il carico oltre RTO
- [ ] Throughput non raggiunge 2× target con risorse disponibili

## Stato attuale

### Architettura corrente

```
Server ──▶ media.db.sqlite (WAL mode, busy_timeout=5000)
Worker ──▶ stesso file via volume Docker condiviso
```

### Configurazione SQLite dal codebase

```go
// storage.OpenSQLiteDB centralizzato
dbPath + "?_journal_mode=WAL&_busy_timeout=5000"
```

### Single-writer design

Il codebase usa pattern single-writer:
- Server: HTTP handler (read + enqueue job)
- Worker: job executor (write results)
- SQLite WAL: letture concorrenti, scritture serializzate
- Lease/fencing: previene double-write tra worker

### Vantaggi SQLite attuali

1. **Zero infrastruttura**: nessun server DB separato da gestire
2. **Backup semplice**: `sqlite3 .backup` o `cp` con WAL checkpoint
3. **Performance**: 667 file Go, latency sub-ms per query semplici
4. **Portabilità**: singolo file, deploy ovunque
5. **Costo**: $0 operativi per il database

### Rischi SQLite identificati

1. **Contention a >4 worker**: WAL single-writer limita scaling orizzontale
2. **Backup bloccante**: checkpoint WAL può bloccare scritture per secondi
3. **No replica**: nessun read replica per scalare letture
4. **File locking**: NFS/volumi di rete possono causare corruzione
5. **Tooling**: meno tool di monitoring rispetto a PostgreSQL

## Raccomandazione

**MANTENERE SQLITE** per il target 1× e 2× con ≤4 worker. Il design single-writer con WAL mode, lease fencing e busy_timeout è sufficiente per il workload stimato.

**PIANIFICARE PostgreSQL** come migrazione futura se:
- Il team cresce e servono >1 server writer
- I load test mostrano contention >1% a 4 worker
- Il backup WAL diventa bloccante oltre RTO

### Piano di migrazione (quando necessario)

1. Introdurre `internal/infrastructure/database/postgres/` adapter
2. Aggiungere `DB_DRIVER=postgres` feature flag in config
3. Implementare `asset.Repository` per PostgreSQL
4. Migrare schema SQLite → PostgreSQL (pgloader o manuale)
5. Dual-write per periodo di transizione
6. Cutover con flag

## Prossimi passi

- [ ] Misurare SQLite busy rate con load test 1-4-8 worker
- [ ] Misurare p95 claim latency
- [ ] Testare backup/restore con carico
- [ ] Documentare RTO/RPO misurati
