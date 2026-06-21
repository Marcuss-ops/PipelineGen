# PipelineGen — Roadmap operativa PR0–PR4

Questa directory è la fonte operativa per il prossimo ciclo di consolidamento di PipelineGen.

La roadmap descrive azioni verificabili, file reali, test ed exit gate. Non contiene istruzioni di branch, comandi di push o procedure Git: ogni documento definisce soltanto il lavoro tecnico da eseguire.

## Obiettivo del ciclo

Portare il repository dallo stato attuale, già privo dei principali namespace legacy, a una struttura coerente e scalabile:

- documentazione e tracker coerenti con il codice reale;
- separazione netta tra use case e adapter concreti per YouTube e Artlist;
- API organizzata per capability;
- composition root modulare senza service locator globale;
- nessuna nuova feature finché PR0–PR4 non sono concluse.

## Ordine obbligatorio

| Documento | Obiettivo | Stato iniziale | Bloccato da |
|---|---|---|---|
| [PR0 — Repository truth](PR0_REPOSITORY_TRUTH.md) | Allineare roadmap, baseline e documentazione al codice reale | Da fare | — |
| [PR1 — YouTube infrastructure](PR1_YOUTUBE_INFRASTRUCTURE.md) | Estrarre download, FFmpeg, filesystem e metadata da `application/youtube` | Da fare | PR0 |
| [PR2 — Artlist infrastructure](PR2_ARTLIST_INFRASTRUCTURE.md) | Estrarre scraper, processi, download e filesystem da `application/artlist` | Da fare | PR0 |
| [PR3 — API compaction](PR3_API_COMPACTION.md) | Consolidare i package API per capability senza cambiare le route | Da fare | PR1, PR2 |
| [PR4 — Composition root](PR4_COMPOSITION_ROOT.md) | Eliminare `services`/`CoreDeps` globali e costruire moduli capability-owned | Da fare | PR3 |

PR1 e PR2 possono essere sviluppate in parallelo soltanto se non modificano gli stessi file di wiring. PR3 inizia dopo la chiusura di entrambe. PR4 è l'ultimo blocco perché dipende dai package definitivi prodotti dalle PR precedenti.

## Regole non negoziabili

1. Un solo proprietario per modello, use case, adapter e route.
2. Nessun nuovo alias di compatibilità, wrapper pass-through o fallback legacy.
3. Nessun nuovo file sotto namespace destinati alla rimozione.
4. Nessun semplice spostamento di directory dichiarato come “layering completato” se il package continua a importare SQL, SDK, filesystem o processi dal livello sbagliato.
5. Le route HTTP e i payload pubblici restano invariati salvo modifica esplicitamente documentata e testata.
6. Ogni sotto-attività numerata deve chiudersi con test mirati e un criterio di accettazione verificabile.
7. Non combinare refactor architetturale, feature e cleanup estraneo nella stessa unità operativa.
8. Prima si rende verde il blocco corrente, poi si passa al successivo.

## Definizione comune di completamento

Una PR è completata soltanto quando:

- tutte le checklist del relativo documento sono marcate `[x]`;
- gli exit gate del documento restituiscono il risultato atteso;
- `go test` dei package toccati è verde;
- `go vet` dei package toccati è verde;
- `go build ./...` è verde per cambiamenti strutturali;
- `go run ./scripts/archcheck` non introduce nuove violazioni;
- la documentazione non dichiara completato lavoro ancora presente nel codice;
- non rimangono TODO temporanei, test saltati o file di follow-up creati dalla stessa PR.

## Stato reale di partenza

Già completato prima di questo ciclo:

- `internal/assets` eliminato e modelli spostati in `internal/domain/asset`;
- `internal/domain/media` eliminato;
- repository asset SQLite spostati in `internal/infrastructure/database/sqlite/assets`;
- `internal/core`, `internal/artifacts`, `internal/upload` e `internal/sources` eliminati o trasferiti;
- test batch script ripristinati in `internal/application/scripts`;
- `internal/application/scriptflow` eliminato;
- provider registry tipizzato attivo;
- `monitor`, `ingest` e `mediaasset` spostati fuori dalle vecchie directory;
- API `books`/`lessons` consolidata in `api/content`;
- API `scraper`/`mediaingest` consolidata in `api/assets`.

Debito ancora attivo:

- `architecture/migration.yaml` e `baseline.json` non rappresentano completamente lo stato reale;
- `application/youtube` e `application/artlist` contengono ancora adapter concreti;
- API ancora frammentata tra `drive`, `realtime`, `searchqueries`, `sources`, `fullimages`, `workers`, `script`;
- `internal/app/dependencies.go` contiene ancora il contenitore globale `services`;
- il job system conserva alias temporanei verso SQLite;
- numerosi package sotto `internal/media` devono ancora essere assegnati al proprietario finale.

## Aggiornamento della checklist

Le checkbox devono essere aggiornate nella stessa modifica che completa il codice corrispondente. Non marcare un blocco come concluso basandosi soltanto su un commit message o su uno spostamento fisico: verificare sempre import, test ed exit gate.
