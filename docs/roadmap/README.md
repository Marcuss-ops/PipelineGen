# PipelineGen — Roadmap operativa verso il 100% dell'operatività

> Fonte operativa per il ciclo successivo al refactor PR1–PR4.
>
> Stato di riferimento: `main` dopo i merge di alleggerimento Node scraper, split Docker e migrazione Qdrant/storage.
>
> Questo documento sostituisce come guida futura le roadmap PR0–PR4 e `REFACTOR_COMPLETE.md`, che restano utili come storico del ciclo precedente.

## Stato sintetico

| Blocco | Stato | Azione residua |
|---|---:|---|
| Node scraper leggero | completato | mantenere `puppeteer-core` come unica dipendenza browser |
| Runtime Docker separati | completato | certificare con smoke test server/worker/admin |
| Qdrant e storage fuori da `internal/media` | completato | riallineare tracker e baseline |
| Test precedentemente saltati | completato | impedire nuovi `t.Skip` non classificati |
| YouTube capability split | parziale | chiudere facade, dipendenze piatte e alias |
| Repository truth | parziale | riallineare migration map, baseline e guardrail |
| Strict architecture mode | mancante | implementare `archcheck --strict` |
| Production certification | mancante | CI verde, E2E, backup/restore, security gate |
| Scale validation | mancante | multi-worker, failure injection, load test, SLO |

## Ordine obbligatorio

| Documento | Risultato |
|---|---|
| [PR5 — YouTube capability split finale](PR5_YOUTUBE_CAPABILITY_SPLIT.md) | Root YouTube piccolo, capability autonome, massimo 8–10 dipendenze per builder |
| [PR6 — Repository truth e strict mode](PR6_ARCHITECTURE_TRUTH_STRICT_MODE.md) | Tracker coerenti, guardrail reali, zero compatibilità non autorizzata |
| [PR7 — Certificazione production](PR7_PRODUCTION_CERTIFICATION.md) | Un commit e una release dimostrati funzionanti end-to-end |
| [PR8 — Validazione della scalabilità](PR8_SCALE_VALIDATION.md) | Capacità, concorrenza, recovery e limiti misurati |
| [Definition of Done 100%](DEFINITION_OF_DONE_100_PERCENT.md) | Checklist finale per dichiarare PipelineGen operativo e pronto a scalare |

PR5 e PR6 sono architetturali. PR7 certifica il sistema reale. PR8 certifica la crescita. Nessuna dichiarazione “100% operativo” è valida prima della chiusura della Definition of Done.

## Regole non negoziabili

1. Partire sempre da `origin/main` aggiornato.
2. Una PR risolve un solo problema e modifica soltanto i file dichiarati nello scope.
3. Cercare il codice esistente prima di creare nuovi package, registry, resolver o adapter.
4. Ogni nuova feature entra nel registry, resolver o sampler comune appropriato.
5. Nessun nuovo alias, wrapper pass-through, fallback legacy o service locator.
6. Nessun aggiornamento della baseline per nascondere una regressione.
7. Nessun test saltato senza build tag, issue e motivazione verificabile.
8. Nessun merge con CI assente, rossa o non osservabile.
9. Ogni percorso operativo deve avere timeout, cancellazione, retry limitato e idempotenza.
10. Backup non verificato con restore equivale a backup inesistente.
11. Scalabilità dichiarata soltanto dopo misure riproducibili.
12. Dopo ogni push controllare `git log -n 5 --oneline` e il diff remoto.

## Workflow Git per ogni blocco

```bash
git fetch origin
git checkout main
git pull --ff-only origin main
git checkout -b codex/<nome-blocco>
```

Durante il lavoro:

```bash
git status -sb
git diff
git fetch origin
git rebase origin/main
```

Prima della PR:

```bash
gofmt -w <file-go-toccati>
go test <package-toccati>
go vet <package-toccati>
git status -sb
git diff origin/main...HEAD
git log -n 5 --oneline
```

## Politica degli stati

Usare soltanto:

- `pending`: lavoro non iniziato;
- `in_progress`: branch attivo con task incompleti;
- `blocked`: impossibile procedere per una dipendenza nominata;
- `done`: exit gate eseguito e prova salvata;
- `verified`: exit gate rieseguito su `main` dopo il merge.

Una checkbox non va marcata sulla base del commit message. Devono essere controllati codice, import, test, CI e artefatti.

## Evidenze obbligatorie

Ogni PR deve riportare nel corpo:

- commit base di `main`;
- file modificati;
- comandi eseguiti;
- risultato dei test;
- output sintetico degli exit gate;
- rischi residui;
- procedura di rollback;
- link alla CI;
- aggiornamento della checklist nella stessa PR.

## Cosa significa “100% operativo”

PipelineGen è operativo al 100% soltanto quando:

- il codice compila, passa vet, lint, test e race test;
- l'architettura passa in modalità strict senza baseline di tolleranza;
- server, worker, admin e scraper costruiscono e si avviano;
- un flusso reale passa da input a output senza intervento manuale;
- restart, retry e duplicati non perdono o moltiplicano il lavoro;
- backup e restore sono stati provati;
- metriche, alert e runbook sono attivi;
- sicurezza e segreti sono verificati;
- il carico atteso e un margine di almeno 2× sono stati misurati;
- esiste una release versionata, riproducibile e rollbackabile.

La checklist completa è in [DEFINITION_OF_DONE_100_PERCENT.md](DEFINITION_OF_DONE_100_PERCENT.md).

## Documenti storici

Questi file non devono più essere usati come unica fonte per il lavoro futuro:

- `REFACTOR_COMPLETE.md` — piano storico PR1–PR4;
- `docs/POST_CASCADE_OPERATIONAL_READINESS.md` — audit post-cascade;
- `docs/roadmap/PR0_REPOSITORY_TRUTH.md` fino a `PR4_COMPOSITION_ROOT.md` — ciclo precedente.

Quando una loro informazione resta valida, deve essere trasferita nei documenti PR5–PR8 invece di aggiungere nuovi follow-up sparsi.
