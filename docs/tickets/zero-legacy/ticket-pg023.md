# PG-023 — Contratto retry e payload job esplicito

**Branch:** `codex/pg-023-job-contract`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Cerca MaxRetries e fallback payload. D. Leggi DTO e persistenza. E. Distingui valore assente da zero. F. Definisci un solo default. G. Rifiuta valori negativi. H. Scegli un solo payload vuoto canonico. I. Aggiorna decoding API. J. Aggiorna producer interni. K. Aggiorna persistence mapping. L. Elimina conversioni silenziose. M. Testa assente/zero/positivo/negativo. N. Testa payload vuoto. O. Verifica record esistenti senza dual-write. P. Gofmt. Q. Test job mirati. R. Test completi. S. Vet/build. T. Archcheck. U. Ripeti ricerca. V. Diff. W. Aggiorna schema docs. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Assente e zero distinguibili.
- [ ] Negativi rifiutati.
- [ ] Un solo wire format payload.
- [ ] Nessun fallback silenzioso.
