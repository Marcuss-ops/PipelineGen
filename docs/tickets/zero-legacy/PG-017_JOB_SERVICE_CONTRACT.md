# PG-017 — Contratto canonico job service

Stato: completato.

Regola operativa: usare esclusivamente `main`; nessuna branch e nessuna PR. Verificare soltanto che la facade concreta, i delegate, il late binding e `ErrNotWired` non ricompaiano. Una regressione reale va ribasata su `origin/main` e pubblicata con `git push origin main`.
