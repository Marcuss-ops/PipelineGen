# Zero Legacy Tickets

Gli agenti lavorano esclusivamente su `main`. Non creano branch, non aprono PR e non usano force-push. `00_GLOBAL_RULES.md` prevale su ogni testo storico.

Procedura: verificare `origin/main`; assegnare un solo writer; eseguire il ticket su `main`; fare rebase; usare `git push origin main`; verificare il commit remoto prima del ticket successivo.
