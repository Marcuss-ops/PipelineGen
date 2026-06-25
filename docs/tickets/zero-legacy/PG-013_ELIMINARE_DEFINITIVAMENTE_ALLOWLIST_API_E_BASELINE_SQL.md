# PG-013 — Rimozione baseline

**Stato:** PARZIALE

Solo `main`; nessuna branch e nessuna PR.

Dopo la chiusura dei ticket API e SQL: cercare e rimuovere allowlist, baseline hardcoded e file grandfathered; non creare sostituti equivalenti; collegare il vero strict di PG-001; aggiungere test anti-regressione; eseguire ratchet, strict, test, vet e build; rebase su `origin/main`; commit; `git push origin main`.

Done: zero allowlist, zero baseline, strict a zero.
