# PR8 — GO/NO-GO Decision v1.0.0-rc.1

## Stato: CONDITIONAL GO ⚠️

La validazione completa richiede staging con servizi live (Qdrant, Drive, Ollama, YouTube).
Questo documento elenca i criteri e lo stato attuale.

## GO — Consentito se

| Criterio | Stato | Note |
|----------|-------|------|
| target 1× workload passa | ⏸️ | Richiede staging |
| target 2× workload passa | ⏸️ | Richiede staging |
| SLO rispettati | ⏸️ | Da misurare |
| zero job persi | ⏸️ | Da testare |
| zero duplicati terminali | ⏸️ | Da testare |
| recovery test passano | ⏸️ | Richiede kill/restart test |
| soak test 24-72 ore | ⏸️ | Richiede staging |
| database decision documentata | ✅ | Vedi DATABASE_DECISION.md |
| capacity plan approvato | ✅ | Vedi CAPACITY_PLAN.md |

## CONDITIONAL GO — Consentito con limiti

| Rischio | Mitigazione | Owner |
|---------|------------|-------|
| SQLite contention a >4 worker | Limitare a 4 worker | Ops |
| No load test reali | Deploy graduale con monitoraggio | Ops |
| Provider rate-limit non testati | Retry + backoff già implementati | Code |
| Backup mai ripristinato in staging | Backup giornaliero + test restore mensile | Ops |

## NO-GO — Bloccante

| Criterio | Stato |
|----------|-------|
| perdita dati | Da testare |
| duplicati terminali | Da testare |
| restore fallisce | Da testare |
| SLO non rispettati al target 1× | Da misurare |
| database corrotto | `PRAGMA integrity_check` da eseguire |
| failure injection richiede fix manuale | Da testare |

## Prerequisiti PR8 (dal documento)

| Prerequisito | Stato |
|-------------|-------|
| PR7 `verified` | ⚠️ Parziale — report creato ma E2E non eseguiti |
| Tag release candidate | ⏸️ Non creato |
| Backup e restore provati | ⏸️ Richiede staging |
| Metriche e alert attivi | ⚠️ `/metrics` esposto, alert non configurati |
| E2E critici verdi | ⏸️ Richiede staging |
| Ambiente load test isolato | ⏸️ Da allestire |

## Gate PR8 (exit criteria)

| Gate | Stato |
|------|-------|
| workload reale definito | ✅ Vedi WORKLOAD.md |
| SLO definiti | ✅ Vedi ENVIRONMENT.md |
| dataset riproducibile | ⏸️ |
| load generator versionato | ⏸️ |
| baseline 1 worker | ⏸️ |
| matrice multi-worker | ⏸️ |
| contention SQLite misurata | ⏸️ |
| decisione database | ✅ SQLite per 1-2× target |
| provider saturation testata | ⏸️ |
| failure injection | ⏸️ |
| soak 72 ore | ⏸️ |
| capacity model | ✅ Vedi CAPACITY_PLAN.md |
| autoscaling policy | ✅ Vedi CAPACITY_PLAN.md |
| security under load | ⏸️ |
| report GO/NO-GO firmato | ⚠️ Questo documento (da firmare) |

## Firma

```
[ ] APPROVATO — 100% operativo
[ ] APPROVATO — pronto a scalare al workload dichiarato
[ ] APPROVATO — release v________________

Commit certificato: ________________________________
Tag certificato: ____________________________________
Data: ______________________________________________
Responsabile tecnico: _______________________________
Risultato GO/NO-GO: CONDITIONAL GO __________________
Limiti dichiarati: max 4 worker, SQLite single-host, CPU-only
```
