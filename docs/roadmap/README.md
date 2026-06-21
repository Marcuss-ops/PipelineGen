# PipelineGen — Roadmap operativa residua PR0–PR5

Questa directory contiene **solo il lavoro ancora aperto**. Le attività già completate sono registrate in `architecture/migration.yaml` e nella cronologia Git e non vengono ripetute nelle checklist.

## Stato attuale

| Documento | Stato reale | Obiettivo residuo | Bloccato da |
|---|---|---|---|
| [PR0 — Repository truth](PR0_REPOSITORY_TRUTH.md) | Parziale | Rendere documentazione, baseline e tracker coerenti con il codice corrente | — |
| [PR1 — YouTube infrastructure](PR1_YOUTUBE_INFRASTRUCTURE.md) | Parziale | Completare il cutover verso porte consumer-side e rimuovere adapter/fallback concreti dall'application | PR0 |
| [PR2 — Artlist infrastructure](PR2_ARTLIST_INFRASTRUCTURE.md) | Parziale | Creare gli adapter concreti Artlist in infrastructure e alleggerire il service applicativo | PR0 |
| [PR3 — API compaction](PR3_API_COMPACTION.md) | Aperta | Eliminare i sette package API legacy mantenendo invariato il contratto HTTP | PR1, PR2 |
| [PR4 — Composition root](PR4_COMPOSITION_ROOT.md) | Avanzata | Eliminare alias, helper condivisi, late binding e lifecycle non tipizzato rimasti | PR3 per la chiusura finale |
| [Single source of truth](SINGLE_SOURCE_OF_TRUTH_GUARDRAILS.md) | Aperta | Portare a zero duplicazioni, alias permanenti, import fuori layer e owner multipli | PR1–PR4 |
| [PR5 — Full Working E2E](PR5_FULL_WORKING_E2E.md) | Non iniziata | Certificare workflow reali, recovery, backup, carico e deployment | PR0–PR4 + SOT strict |

## Ordine operativo

1. Chiudere PR0 e rendere affidabile la fotografia del repository.
2. Completare PR1 e PR2 senza aggiungere nuovi wrapper o percorsi paralleli.
3. Eseguire PR3 mantenendo route e payload invariati.
4. Chiudere i residui PR4 e il debito `internal/media` coinvolto dalle capability migrate.
5. Implementare i guardrail single-source-of-truth in modalità strict.
6. Eseguire PR5 e dichiarare operative soltanto le capability realmente certificate.

PR1 e PR2 possono procedere in parallelo solo se non modificano gli stessi file di wiring. PR5 non deve essere usata per completare refactor rimasti aperti.

## Regole non negoziabili

1. Un concetto ha un solo owner, un solo import path canonico e un solo punto di registrazione.
2. Le interfacce sono piccole e definite dal consumer.
3. `domain` non contiene SQL, SDK, filesystem o processi concreti.
4. `application` non importa Gin, SQLite concreto, Google Drive SDK, FFmpeg o `os/exec`.
5. `api` contiene solo trasporto e non costruisce service o repository.
6. Gli adapter concreti vengono costruiti soltanto in `internal/app`.
7. Nessun alias permanente, wrapper pass-through, fallback legacy o setter di wiring evitabile.
8. Nessuna goroutine viene avviata in un costruttore.
9. Ogni nuovo provider o strategia entra nel registry, resolver o sampler canonico.
10. Le route HTTP e i payload restano invariati salvo modifica esplicita con contract test.
11. Ogni task termina con test mirati e un exit gate verificabile.
12. Non aggiungere nuove feature durante questo ciclo di consolidamento.

## Definizione comune di completamento

Un blocco è completato soltanto quando:

- tutte le checklist residue del documento sono chiuse;
- i test mirati, `go vet` e la build sono verdi;
- `go run ./scripts/archcheck` non introduce nuove violazioni;
- lo strict gate previsto dal documento SOT è verde quando applicabile;
- non restano TODO temporanei, test saltati o fallback creati dalla stessa modifica;
- la documentazione descrive il codice realmente presente;
- il diff non combina refactor, feature e cleanup estranei.

## Debito strutturale che resta fuori dai task già completati

- adapter e dipendenze concrete ancora presenti in `application/youtube` e `application/artlist`;
- sette package API legacy: `drive`, `realtime`, `searchqueries`, `sources`, `fullimages`, `workers`, `script`;
- alias job e composition ancora attivi;
- SQL ancora presente in `internal/domain/asset`;
- package residui sotto `internal/media` da assegnare ai proprietari finali;
- `archcheck` ancora in modalità ratchet, senza modalità strict;
- assenza di suite E2E completa, backup/restore verificato e soak test.
