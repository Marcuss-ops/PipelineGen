# PG-008 — Rimuovere database/sql da application jobs e outbox

**Stato:** APERTO

Solo `main`; nessuna branch e nessuna PR.

## Todo

1. Sincronizzare `main` e cercare `database/sql` nei package jobs e outbox application.
2. Mappare query, transazioni e tipi concreti.
3. Definire o completare porte domain/application minime.
4. Spostare SQL e mapping negli adapter sotto `internal/infrastructure/database`.
5. Iniettare porte tipizzate nei service.
6. Eliminare escape hatch `.DB`, cast e wrapper temporanei.
7. Aggiornare composition, fake e test.
8. Verificare zero import `database/sql` nei target.
9. Eseguire gofmt, test mirati, test completi, vet, build e archcheck ratchet.
10. Rebase su `origin/main`, commit mirato e `git push origin main`.

## Done

- [ ] Jobs e outbox application senza SQL.
- [ ] Adapter database unici.
- [ ] Test e gate verdi.
