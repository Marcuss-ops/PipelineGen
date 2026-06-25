# PG-014 — Configurazione worker

Stato: completato.

Regola operativa: usare esclusivamente `main`; nessuna branch e nessuna PR. Verificare soltanto che `VELOX_MASTER_URL` resti l’unico contratto e che il vecchio fallback non ricompaia. Una regressione reale va ribasata su `origin/main` e pubblicata con `git push origin main`.
