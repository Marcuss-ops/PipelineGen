# PG-023

Stato: aperto.

Usare solo `main`; non creare branch o PR.

Rendere espliciti retry e payload job: distinguere valore assente da zero, mantenere un solo default, rifiutare valori negativi, scegliere un solo formato per il payload vuoto, aggiornare API, producer, persistence e test. Eseguire i gate, fare rebase su `origin/main` e pubblicare con `git push origin main`.
