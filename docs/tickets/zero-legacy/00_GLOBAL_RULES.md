# Regole operative obbligatorie — solo `main`

Queste regole prevalgono su qualsiasi istruzione storica nei ticket PG-001–PG-047.

## Workflow Git

- Lavorare esclusivamente su `main` sincronizzato con `origin/main`.
- Non creare branch di ticket, feature branch, stacked branch o branch di prova.
- Non aprire PR per i ticket Zero Legacy.
- Non usare force-push su `main`.
- Un solo agente writer alla volta. Agenti di analisi e test non committano e non pushano.
- Il ticket successivo parte soltanto dopo la verifica del commit precedente su `origin/main`.

## Prima di iniziare

```bash
git fetch origin
git checkout main
git status -sb
```

Se esistono commit locali non pubblicati:

```bash
git rebase origin/main
```

Altrimenti:

```bash
git pull --ff-only origin main
```

La working tree deve essere pulita. Se contiene modifiche di un altro writer, fermarsi senza reset, stash o sovrascritture.

## Durante il ticket

- Un ticket e uno scope alla volta.
- Cercare il codice esistente prima di aggiungere tipi o package.
- Non duplicare registry, resolver, sampler, writer, mapping o routing.
- Nessun alias di compatibilità, wrapper pass-through o fallback silenzioso.
- SQL solo sotto `internal/infrastructure/database/**`.
- Adapter concreti costruiti solo in `internal/app`.
- Non aggiornare baseline o allowlist per nascondere violazioni.
- Non aggiungere feature fuori scope.
- Non committare output, database o artefatti generati.

## Prima del commit

```bash
git fetch origin
git rebase origin/main
git status -sb
git diff
git diff --check
```

Eseguire i test mirati. Quando richiesto:

```bash
go test ./...
go vet ./...
go build ./...
go run ./scripts/archcheck --ratchet
```

Il vero `--strict` si usa soltanto dopo PG-001 e PG-046.

## Commit e push

```bash
git add <solo-file-del-ticket>
git commit -m "<tipo>(<scope>): <descrizione>"
git fetch origin
git rebase origin/main
git push origin main
git log -n 5 --oneline
git status -sb
```

Verificare sempre che il commit sia presente su `origin/main`.

## Conflitti

- Ticket che toccano lo stesso file non lavorano in parallelo.
- Il ticket successivo parte dall’ultimo `origin/main`.
- Risolvere i conflitti manualmente; non usare ciecamente `--ours` o `--theirs`.
- Non creare una branch per evitare il conflitto.

## Stop conditions

Fermarsi se il ticket è già completato, il path non esiste più, serve una compatibility layer, emerge un secondo writer, servono file fuori scope o un altro writer ha pubblicato modifiche sovrapposte. Non creare branch o PR per aggirare il blocco.
