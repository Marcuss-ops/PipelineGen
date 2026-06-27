# Indice ticket RW-PROD

> Indice sintetico dei 17 ticket definiti nel runbook
> [`04-remote-worker-production-readiness-tickets.md`](../04-remote-worker-production-readiness-tickets.md).

Ogni ticket è P0: un worker non entra in `PRODUCTION_READY` se anche solo uno è aperto.

## Tabella ticket

| # | ID | Titolo | Dipendenze | Stato al commit corrente |
|---|----|--------|------------|--------------------------|
| 1 | RW-PROD-001 | Identità worker e mTLS production fail-closed | — | `OPEN` |
| 2 | RW-PROD-002 | Validazione completa configurazione production | RW-PROD-001 | `OPEN` |
| 3 | RW-PROD-003 | Bootstrap runtime ed executor reale | RW-PROD-002 | `OPEN` |
| 4 | RW-PROD-004 | Liveness e readiness worker separate | RW-PROD-003 | `OPEN` |
| 5 | RW-PROD-005 | Stato canonico worker dal master | RW-PROD-004 | `OPEN` |
| 6 | RW-PROD-006 | Sizing risorse e admission control | RW-PROD-005 | `OPEN` |
| 7 | RW-PROD-007 | Canary mTLS per ogni worker remoto | RW-PROD-001, RW-PROD-003, RW-PROD-005 | `OPEN` |
| 8 | RW-PROD-008 | Integrità artifact e finalizzazione | RW-PROD-007 | `OPEN` |
| 9 | RW-PROD-009 | Riconnessione dopo restart master | RW-PROD-005 | `OPEN` |
| 10 | RW-PROD-010 | Crash worker, lease expiry e retry | RW-PROD-008 | `OPEN` |
| 11 | RW-PROD-011 | Network partition e duplicate suppression | RW-PROD-009, RW-PROD-010 | `OPEN` |
| 12 | RW-PROD-012 | Drain, SIGTERM e cancellazione processi | RW-PROD-010 | `OPEN` |
| 13 | RW-PROD-013 | Metriche, log e alert operativi | RW-PROD-005, RW-PROD-006 | `OPEN` |
| 14 | RW-PROD-014 | Monitoraggio e rotazione PKI | RW-PROD-001 | `OPEN` |
| 15 | RW-PROD-015 | Soak test e matrice di certificazione hardware | RW-PROD-006, 007, 009, 010, 011, 012 | `OPEN` |
| 16 | RW-PROD-016 | Comando `velox-worker-agent doctor` | RW-PROD-001 → RW-PROD-015 | `OPEN` |
| 17 | RW-PROD-017 | Rollout, promotion e rollback worker | RW-PROD-015, RW-PROD-016 | `OPEN` |

## Regole di transizione

- `OPEN → IN_PROGRESS` solo quando il ticket precedente nell'ordine di esecuzione (vedi sotto) è `DONE`.
  Eccezione: ticket indipendenti (`RW-PROD-013` dopo `RW-PROD-005`+`RW-PROD-006`, `RW-PROD-014` dopo `RW-PROD-001`) possono partire in parallelo se esplicitamente approvati.
- `IN_PROGRESS → BLOCKED` solo se la dipendenza tecnica o organizzativa è fuori controllo del team; ogni `BLOCKED` richiede owner, motivazione, scadenza e ticket di uscita.
- `IN_PROGRESS → READY_FOR_REVIEW` solo con test verdi, criteri di accettazione verificati ed evidenze archiviate (vedi sezione "Evidenze richieste" del singolo ticket).
- `READY_FOR_REVIEW → DONE` solo dopo merge + verifica post-deploy.

## Ordine di implementazione obbligatorio

1. **RW-PROD-001** Identità e mTLS.
2. **RW-PROD-002** Validazione completa config.
3. **RW-PROD-003** Bootstrap runtime/executor.
4. **RW-PROD-004** Liveness/readiness.
5. **RW-PROD-005** Stato canonico master.
6. **RW-PROD-006** Resource sizing.
7. **RW-PROD-007** Canary per worker.
8. **RW-PROD-008** Artifact integrity.
9. **RW-PROD-009** Restart master.
10. **RW-PROD-010** Crash worker e retry.
11. **RW-PROD-011** Network partition.
12. **RW-PROD-012** Drain e shutdown.
13. **RW-PROD-013** Metriche e alert.
14. **RW-PROD-014** PKI rotation.
15. **RW-PROD-015** Soak e hardware certification.
16. **RW-PROD-016** Worker doctor.
17. **RW-PROD-017** Rollout e rollback.

Non iniziare il ticket N+1 finché N non è `DONE`. Ogni shortcut ha bisogno di approvazione esplicita del reviewer di runtime + security.

## Definizione di "ticket implementabile"

Per essere considerato "implementabile" e quindi candidato come **executable action**, un ticket deve esporre almeno:

- Comando o endpoint di verifica riproducibile (`doctor`, canary, integration test).
- Criteri di accettazione verificabili da test automatici (non solo ispezione manuale).
- Evidenze archiviate in un path canonico (report JSON, log firmato, query DB, dump metriche).
- Owner in `[ops/infra, runtime, security]`.

## Link utili

- Runbook completo: `../04-remote-worker-production-readiness-tickets.md`
- Checklist operativa per singolo worker: `../worker-certification-checklist.md`
- Definizione di Done globale: sezione 3 del runbook.
- Regola finale di ammissione: sezione 6 del runbook + sezione 2 della checklist.

---

## Indice ticket Wave 19 PR4 + PR5 (Direction Hardening + db/sql shrinkage)

> Indice sintetico dei 2 ticket definiti nel runbook
> [`wave-19-pr4-pr5-tickets.md`](../wave-19-pr4-pr5-tickets.md).

Derivati da `architecture/current.yaml::Wave 19::pr4_pr5_followups.items[*]`
(`verified_zero: false` al commit corrente). Non sono P0 come la serie
RW-PROD: W19-PR4-001 è P0 (fail-fail-closed gate maintenance), W19-PR5-001
è P1 (audit monotono). La chiusura dei due ticket NON cambia lo status
top-level di Wave 19 (già `done` + `verified_zero: true`); aggiorna solo
i due `verified_zero` figli `pr4_pr5_followups.items[PR4|P5]`.

| # | ID | Titolo | Dipendenze | Stato al commit corrente |
|---|----|--------|------------|--------------------------|
| 1 | W19-PR4-001 | Hard gate promotion: allowlist subtractSet per application→infrastructure + cross_capability_import | nessuna (PR2-1, PR2-3, PR5 hardening `1b2da8f1` già upstream) | `OPEN` |
| 2 | W19-PR5-001 | database/sql inheritance shrinkage (Path B baseline-growth audit) | W19-PR4-001 `DONE` | `OPEN` |

## Regole di transizione (Wave 19)

- `OPEN → IN_PROGRESS` per W19-PR4-001 può partire subito (nessuna dipendenza).
- `OPEN → IN_PROGRESS` per W19-PR5-001 solo dopo che W19-PR4-001 è `DONE`.
- `READY_FOR_REVIEW → DONE` solo dopo merge + verifica post-deploy (counter
  blocks dalla focused + ratchet mode archcheck + cross-link YAML aggiornato).

## Note di correlazione

- I due allowlist file `docs/migrations/{application-infrastructure,cross-capability}-imports-allowlist.txt` sono già `DONE` su `main` come parte del PR-track PR4. La promozione del gate li consuma.
- Il ticket `FollowWave19PR3TypedPortLift` (Path A typed-port lift) è indipendente da W19-PR5-001; può procedere in parallelo dopo W19-PR4-001.
- I due ticket NON chiudono Wave 19 (Wave 19 è già `done`). Chiudono solo i due `pr4_pr5_followups.items[*]` figli (`verified_zero: false → true`).
