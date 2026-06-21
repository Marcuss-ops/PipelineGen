# Follow-ups

File di follow-up ereditati che non sono stati chiusi da PR0.

| File | Stato | Note |
|---|---|---|
| [`2026-06-internal-app-pre-existing-build-errors.md`](2026-06-internal-app-pre-existing-build-errors.md) | **OPEN** | Pre-PR-12. Documenta 9 errori di build in `internal/app/` su commit `812e980`. Wave 15 PR4d-final ha già eliminato `type services struct` e `type CoreDeps`, ma i 9 errori storici sono stati assorbiti da cleanup successivi (non riaperti qui). File mantenuto come riferimento storico; nessuna azione richiesta da PR0. |
| [`2026-06-migration-053-test-failure.md`](2026-06-migration-053-test-failure.md) | **OPEN** | Migration 053 ha un `BEGIN IMMEDIATE;` incorporato che annida transazioni. Owner TBD; fuori scope per PR0–PR4 (richiede audit del migration runner sugli altri file `*.sql`). |

PR0 non tocca questi file; sono tracciati qui solo per inventariare il debito pregresso. Vedi `architecture/migration.yaml` per lo stato Wave-per-Wave.
