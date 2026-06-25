# PG-005 — Eliminare import infrastructure da API Clips

**Stato:** APERTO

Workflow obbligatorio: solo `main`; nessuna branch e nessuna PR. Seguire `00_GLOBAL_RULES.md`.

## Obiettivo

Rendere gli handler Clips puro trasporto, senza import o tipi concreti di infrastructure.

## Todo

1. Sincronizzare `main` e leggere i commit recenti.
2. Inventariare import, repository, config e servizi concreti usati dagli handler Clips.
3. Riutilizzare porte application/domain esistenti; estenderle solo con metodi indispensabili.
4. Spostare DTO e contratti nel layer owner.
5. Implementare gli adapter concreti nel layer infrastructure.
6. Costruire gli adapter soltanto in `internal/app`.
7. Migrare handler, fake e test senza cast runtime.
8. Aggiungere assertion compile-time.
9. Verificare zero import `internal/infrastructure` dai file API target.
10. Eseguire gofmt, test mirati, `go test ./...`, vet, build e archcheck ratchet.
11. Controllare diff e file fuori scope.
12. Fare rebase su `origin/main`, committare e usare `git push origin main`.

## Done

- [ ] Zero reach-through infrastructure.
- [ ] Handler basati solo su porte tipizzate.
- [ ] Test e gate verdi.
