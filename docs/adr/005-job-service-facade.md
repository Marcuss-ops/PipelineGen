# ADR-005: Drop `*job.Service` facade → use `job.InnerService` interface directly

**Stato**: Proposed (June 2026)
**Decision-maker**: TBD

**Gate**: PR C (cleanup finale di `domain/job/service.go`) richiede
team-review esplicita sul naming finale `InnerService` → `Service`
e sulla assertion `var _ job.Service = (*appjobs.Service)(nil)`. PR A
+ PR B sono refactor meccanici e procedono **senza** review
architettonica aggiuntiva (mechanical refactor + 1-liner field
type change, coperti dai test esistenti).

## Context

`internal/domain/job/service.go` espone `*job.Service` — un struct
concreto che wrappa **6 delegate function pointers** — a ogni consumer
di PipelineGen. Introdotto storicamente per aggirare il "type *X is
pointer to interface, not interface" di Go (facendo in modo che
`s.jobsSvc.Enqueue(...)` non rifiuti il dispatch), il facade è
cresciuto a:

- 7 campi `func(...)` (`EnqueueFn`, `GetFn`, `CancelFn`, `ListFn`,
  `IsTerminalFn`, `RegisterHandlerFn`, `ListEventsFn`).
- 5 method shim pubblici che derivano `ErrNotWired` quando il delegate
  interno è nil (un sentinel che esiste solo per fare fail-loud in
  sviluppo).
- 3 setter late-binding (`SetInner`, `SetRegisterHandler`,
  `SetListEvents`) + 2 constructor (`NewUnwiredService`, `NewService`).
- Una sentinella (`ErrNotWired`).

Totale: ~120 righe di codice di indirezione per smaltire **un solo**
caso d'uso concreto: il consumer deve avere una referenza non-nil al
service di job al bootstrap, e si vuole che il fail-mode sia rumoroso.

Il pipeline ha esattamente **una** implementazione in-tree
(`*internal/application/jobs.Service`), un solo composition root, e
un solo test harness. Lo scenario "late-binding reale" non esiste: il
service è disponibile al massimo ~100µs dopo l'avvio del processo.

## Decisione

**Eliminare `*job.Service` come tipo di campo esposto ai consumer.
I consumer dichiarano i loro field come `job.InnerService`
(l'interfaccia interna già esistente in `domain/job/service.go::98`).
Il composition root istanzia `*appjobs.Service` presto (è già
in `BuildJobsBundle`) e lo passa come `job.InnerService` — Go
risolve correttamente il dispatch via interface, senza nessun
indirezione aggiuntiva.**

In pratica:

- `Service` struct + i suoi 7 campi `func(...)` + i 5 metodi shim →
  **eliminati**.
- `ErrNotWired`, `NewUnwiredService`, `NewService`, `SetInner`,
  `SetRegisterHandler`, `SetListEvents`, la assertion `var _
  InnerService = (*Service)(nil)` → **tutti eliminati**.
- `InnerService` interface rimane e diventa il **contratto primario**
  di dominio (non più l'escape hatch interno).
- Compile-time guard sostitutiva: `var _ job.InnerService =
  (*appjobs.Service)(nil)` vive in `internal/application/jobs/service.go`
  vicino al tipo concreto, garantendo che `*appjobs.Service` mantenga
  sempre la conformità al dominio.

## Conseguenze

**Positive**:

- ~120 LOC rimosse da `internal/domain/job/service.go`.
- I consumer (`scheduler`, `scriptflow`, `realtime`) dichiarano
  `jobService job.InnerService` invece di `*job.Service` — idiomatic
  Go, nessun workaround di puntatori.
- Il fail-mode rumoroso del "service non ancora pronto" si degrada
  elegantemente: panico-on-nil al primo call-site invece di
  `ErrNotWired`. In production questo è unreachable perché il service
  è istanziato PRIMA del primo consumer che lo usa.
- Elimina il pattern late-binding (`SetRegisterHandler` chiamato
  dopo la costruzione del consumer) che costringeva ogni consumer a
  un ordine di inizializzazione a due step.
- Compose root resta a **una sola fase** di wiring per modulo invece
  di due (build bundle → wire facade → wire consumer).

**Negative**:

- I consumer che attualmente gestiscono `errors.Is(err,
  services.ErrNotWired)` per diagnosticare la mancata inizializzazione
  in fase di boot devono migrare a `if svc == nil { panic }` o
  a un check esplicito. La qualità del segnale resta uguale (fail-
  loud), ma cambia la forma (panic-on-nil contro typed-error).
- Il consumer deve avere il service **non-nil** già al
  construction time. PipelineGen già soddisfa questa condizione
  ovunque tranne i test isolati, quindi l'impatto è su ~3-5 test
  file che mockavano il service via facade.

**Neutrali**:

- `job.InnerService` resta posizionato in `internal/domain/job/`
  (è un'interfaccia di dominio, va lì). Niente relocate.
- Il composition root di `internal/app/dependencies.go` non cambia
  strutturalmente: `BuildJobsBundle` continua a costruire
  `*appjobs.Service`; cambia solo il field type della struct
  risultante da `Facade *job.Service` (struct) a `Service
  job.InnerService` (interface). ~5 righe modificate.

## Alternative considerate (e respinte)

**A. Tenere il facade com'è, documentarlo.** Respinta: perpetua
il tech debt; il prossimo agente ri-farà la stessa domanda.

**B. Wrapper minimal che resta struct ma perde 80 LOC di shims.**
Respinta: stessa forma architettonurale, solo più piccolo. Non
risolve il problema reale (interface-vs-struct nel consumer field).

**C. Promuovere `*job.Service` a interface puro, drop lo struct.**
**Questa è essenzialmente la decisione adottata** — vedi "Scelta
finale" sotto.

**D. Conditional facade (mantieni solo per testing, rimuovi in
production).** Respinta: i test importerebbero comunque la facade,
quindi nessun codice risparmiato in production. Aggiunge path
condizionale di import.

**E. Consumers dichiarano `*appjobs.Service` direttamente (no
interface, no struct).** Respinta: coupling diretto all'application
package dal dominio, peggio del compromise interface-based. Viola
AGENTS.md §"🧰 Utilities to prefer" che richiede dominio indipendente
dall'application layer.

## Scelta finale per il naming

`InnerService` viene rinominata a `Service` **in PR A**, non in
PR C. Due round-trip di rename (Inner→Service→cancelato) introducono
rumore nei diff e confondono git blame. PR A fa già il rename e PR C
può quindi essere una pura eliminazione di codice. La decisione è
irreversibile: nessun "InnerService" residuo dopo PR A.

## Prerequisiti

Wave 17.1 (settembre 2026) ha normalizzato `job.Store` come singolo
contratto canonico in `internal/domain/job`. Questo ADR è
**indipendente**: la facade opera a livello dominio, Wave 17.1 era a
livello infrastruttura. Wave 17.2 può partire in qualsiasi momento
dopo Wave 17.1 — nessuna dipendenza runtime, ma leggere Wave 17.1 per
contestualizzare l'evoluzione della ownership del contratto di
persistenza.

## Migration plan (3 PR piccoli)

1. **Wave 17.2 PR A (zero risk — 1 file)**:
   `internal/infrastructure/jobs/local/broker.go::LocalBroker.jobs` è
   il consumer con il minor numero di file impattati (zero downstream
   impact: il broker è già isolato dietro interfaccia). Cambio field
   type da `domainjob.Store` (già interface, nessuna modifica) a
   conferma che il pattern è già pulito — questo PR è la **rampa di
   lancio** per verificare che Go dispatch su interface funzioni senza
   il workaround struct. Facade resta vivo per gli altri consumer.

   **Acceptance criteria verificabili**:
   - `grep -rn '\\*job\\.Service\\b' internal/infrastructure/jobs/local/broker.go` ritorna
     ZERO hits (a parte eventuali commenti di deprecation).
   - Il composition root `internal/app/dependencies.go::composeIntegration`
     ha ANCORA `jobServiceFacade *job.Service` per compatibilità con
     PR B/C, marcato con commento `// DEPRECATED: rimosso in PR C`.
   - Rinomina `InnerService` → `Service` completata (vedi §"Scelta
     finale per il naming").

2. **Wave 17.2 PR B (medium — 3-4 file)**: scheduler
   (`internal/infrastructure/database/scheduler`) +
   `internal/app/lifecycle.go` + `internal/api/script/*` script-flow
   handler. Stesso cambio di field type; il wiring resta late-binding-
   friendly perché Go dispatch su interface non richiede init order
   speciale. Scheduler è qui (non in PR A) perché è l'unico consumer
   che gira in modalità cron-style: richiede test più estesi prima
   del merge.

3. **Wave 17.2 PR C (cleanup — ~1 file modified, 1 file deleted)**:
   realtime + broker + outliers. Dopo che TUTTI i consumer sono
   passati, eliminare `domain/job/service.go` **per intero**. Il file
   contiene solo la facade e i suoi delegati; i tipi `Job`, `Status`,
   `Filter`, `Event`, `EnqueueRequest` vivono in altri file del package
   (`domain/job/job.go`, `domain/job/store.go`, ecc.) e NON sono
   toccati da questa migration.

## Decision rationale

Il facade esiste perché "pointer-to-interface è scomodo in Go" — ma
il problema sottostante si risolve **dichiarando il field del
consumer come INTERFACE type** invece che come `*ConcreteStruct`.
PipelineGen ha:

- 1 implementazione concreta (`*appjobs.Service`).
- 1 composition root (`internal/app/dependencies.go`).
- ~6 consumer (`scheduler`, `scriptflow`, `lifecycle`, `realtime`,
  `broker`, altri).

Non c'è scenario realistico dove l'astrazione late-binding guadagni
il suo costo. Il fail-mode rumoroso è preservato naturalmente con
nil-check + panic al primo call-site, che è **più semplice** di
un typed-error che richiede `errors.Is` a ogni chiamata.

## Ri-apertura

Questo ADR si considera **rigettato** se, prima del merge di
Wave 17.2 PR C, emerge uno scenario concreto in cui:

- Un secondo backend di job (es. Redis-based queue) deve coesistere
  con SQLite nella stessa build.
- Un test harness deve simulare failure-mode del service per
  ragioni diverse dal nil-check (es. partial failure, slow
  response, deadlocked connection).
- Il late-binding diventa necessario in un consumer che non è
  ancora noto al composition root time.

In tutti gli altri casi la facade rimane un workaround per un
problema che Go ha già risolto.
