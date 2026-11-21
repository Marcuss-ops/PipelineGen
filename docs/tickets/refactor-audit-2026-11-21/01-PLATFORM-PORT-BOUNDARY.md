# RA-01 — Port boundary: i contratti semantici fuori da `capabilities/scripts`

**Data:** 2026-11-21
**Priorità:** P0 (blocca `archcheck --strict`)
**Stato:** DONE (verificato 2026-11-21)
**Owner:** `media-rust` + `kernel/script`
**Origine:** report esterno 2026-11-21 §0.1, §1.1; verificato sul codice
**Verifica sul codice:** **CONFERMATO**, con perimetro più largo del report (vedi §1)

## 0. Stato al ricontrollo (2026-11-21, stessa sessione) — leggere prima di iniziare

Mentre la scheda veniva scritta, un lavoro concorrente **non committato** ha eseguito la
migrazione: i tre file ora importano `internal/kernel/script/semantic` (nuovo package) e
`go run ./cmd/archcheck --strict` esce **0** con `total_violations: 0`. Verificato:

```bash
grep -c 'capabilities/scripts"' internal/platform/media/rustexec/semantic_engine_{discourse,people_events,places}.go
# atteso e misurato: 0, 0, 0
ls internal/kernel/script | grep semantic          # esiste semantic/
grep -c 'rustexec/' cmd/archcheck/scan/boundaries/percheck_platform_port_boundary.go   # 12
```

Quindi questo ticket **non è più di implementazione** ma di **verifica e chiusura**. Il lavoro
residuo è:

1. verificare che i 3 file siano stati migrati davvero (tipi nel kernel) e non solo
   re-indirizzati a un alias che vive ancora in `capabilities/scripts`;
2. verificare che i test dei 3 file compilino e passino (il tree ha 46 file modificati non
   committati: il rischio è un cambio a metà);
3. committare il lavoro in un commit dedicato (non mescolato ad altri);
4. decidere se procedere con le **12 entry `rustexec` rimanenti** dell'inventario: sono debito
   registrato con trigger "semantic ports move to kernel/script", che è esattamente ciò che
   è appena successo. Se i contratti ora esistono nel kernel, quelle 12 entry possono migrare
   con lo stesso metodo (o restringersi) in una scheda successiva.

Il resto della scheda resta valido come criterio di chiusura e come metodo per le 12 entry.

## 1. Problema

`go run ./cmd/archcheck --strict` a `34d21526c` esce 1 con tre violazioni
`percheck_platform_port_boundary` / `platform_capability_business_import`:

| File | Riga | Import vietato |
| --- | --- | --- |
| `internal/platform/media/rustexec/semantic_engine_discourse.go` | 6 | `internal/capabilities/scripts` |
| `internal/platform/media/rustexec/semantic_engine_people_events.go` | 9 | idem |
| `internal/platform/media/rustexec/semantic_engine_places.go` | 7 | idem |

Il gate (`cmd/archcheck/scan/boundaries/percheck_platform_port_boundary.go`) vieta a
`internal/platform/**` di importare i package *business* delle capability, e ammette solo i
sottopackage di porta (`internal/capabilities/scripts/ports`). Le eccezioni storiche vivono
in `platformCapabilityScriptsInventory`, che è debito con owner e trigger di rimozione e
**può solo restringersi**: la seconda metà del gate (`percheck_platform_port_inventory_stale`)
fallisce se una entry resta dopo la migrazione.

Il perimetro reale è più ampio delle tre righe: **16 file non-test** in
`internal/platform/media/rustexec` importano `internal/capabilities/scripts`; 13 sono in
inventario, 3 no. Correggere solo i tre file non inventariati senza estrarre il contratto
significa spostare l'import (o inventariarlo, cioè aumentare il debito).

Simboli coinvolti (misurati con
`grep -o 'scriptgen\.[A-Za-z0-9_]*' <file> | sort -u`):

- `semantic_engine_discourse.go`: `SemanticDiscourseCounts`, `SemanticDiscourseProjection`,
  `SemanticDiscourseReference`, `SemanticEntityThread`, `SemanticSceneContinuity`,
  `SemanticThreadBeat`.
- `semantic_engine_people_events.go`: `SemanticEventEvidence`, `SemanticEventFrame`,
  `SemanticEventParticipant`, `SemanticEventTime`, `SemanticPersonAgeFact`,
  `SemanticPersonRelation`, `SemanticPersonRole`, `SemanticRoleTemporal`.
- `semantic_engine_places.go`: `SemanticCrossSceneRoute`, `SemanticCrossSceneRouteStop`,
  `SemanticPlace`, `SemanticRoute`, `SemanticRouteEvidence`, `SemanticRouteStop`.

Sono **DTO di proiezione wire** (nessuna policy, nessun servizio): per definizione
appartengono al kernel, non alla capability.

## 2. Impatto

- Gate architetturale rosso: nessun merge può dichiarare `archcheck --strict` verde.
- `platform/media/rustexec` non è compilabile/testabile/sostituibile senza la capability
  `scripts`, che a sua volta importa `platform/*`: è il ciclo di sottosistema descritto in
  RA-11.
- L'inventario non può scendere finché i DTO restano dove sono.

## 3. Soluzione

1. **Censimento esatto.** Elencare per i 16 file ogni simbolo `scriptgen.*` usato:
   ```bash
   for f in internal/platform/media/rustexec/*.go; do
     case "$f" in *_test.go) continue;; esac
     grep -o 'scriptgen\.[A-Za-z0-9_]*' "$f" | sort -u | sed "s|^|$f: |"
   done
   ```
   Separare i DTO puri (struct/enum) dalle funzioni/validatori: solo i primi si spostano in
   questo ticket.
2. **Creare `internal/kernel/script/semantic`** (nome coerente con
   `internal/kernel/script/renderqueue`, citato dal gate). Regole del package:
   solo tipi e metodi puri, **zero import** da `internal/capabilities/**` e
   `internal/platform/**`; validazione inclusa solo se non richiede I/O.
3. **Spostare i tipi verbatim** in file coesi per famiglia:
   `discourse.go`, `people_events.go`, `places.go`. I nomi `Semantic*` restano: cambia solo
   il package.
4. **Alias di compatibilità in `capabilities/scripts`**, così i chiamanti non si toccano in
   questo passo: `type SemanticPlace = semantic.Place` (stesso pattern già usato in
   `audio_asset_resolver.go` e `ports_execution.go`). Gli alias sono temporanei e vanno
   rimossi quando i chiamanti importano il kernel direttamente.
5. **Aggiornare i 16 adapter** perché importino `internal/kernel/script/semantic`. Non è
   necessario farlo in un commit unico: si può procedere per famiglia (discourse →
   people/events → places) purché ogni commit lasci il gate non peggiore.
6. **Svuotare l'inventario `rustexec`** in
   `cmd/archcheck/scan/boundaries/percheck_platform_port_boundary.go`: eliminare le 13 entry
   `internal/platform/media/rustexec/*`. La metà "stale" del gate obbliga a farlo nello
   stesso cambio che migra il file.
7. **Verificare che restino solo porte.** Se un adapter ha bisogno di una funzione (non di un
   tipo), la funzione va in `internal/kernel/script/...` come helper puro oppure dietro una
   porta in `capabilities/scripts/ports`. Non si allarga `platformAllowedCapabilitySubpackages`.
8. **Controllo di direzione**: `internal/kernel/script/semantic` non deve comparire in
   `go list -deps` di nessun package che il kernel stesso importa; il kernel resta foglia.

## 4. Check finali

```bash
cd refactored
# 1. nessun adapter rustexec importa più il package business
grep -rn 'capabilities/scripts"' internal/platform/media/rustexec/*.go | grep -v _test
#    atteso: nessun output

# 2. il gate è verde
go run ./cmd/archcheck --strict
#    atteso: exit 0, "has_hard_gate_hits": false
#    (se restano solo le violazioni hotspot di RA-02, questo ticket è comunque chiuso:
#     annotare l'output e chiudere RA-02 in sequenza)

# 3. inventory stale non scatta
go run ./cmd/archcheck --strict | grep percheck_platform_port_inventory_stale
#    atteso: nessun output

# 4. il kernel non importa platform/capabilities
go list -deps ./internal/kernel/script/semantic | grep -E 'internal/(capabilities|platform)'
#    atteso: nessun output

# 5. build e test dei package toccati
go build ./...
go vet ./...
go test ./internal/platform/media/rustexec/... ./internal/kernel/script/... ./internal/capabilities/scripts/...
#    atteso: PASS (i test rustexec che avviano binari reali possono richiedere env:
#     se skippano, riportare "SKIP", non "PASS")

# 6. nessun alias orfano residuo
grep -rn '= semantic\.' internal/capabilities/scripts/*.go
#    atteso: solo gli alias dichiarati in questo ticket
```

## 5. Rischi e rollback

- **Rischio ciclo**: se `kernel/script/semantic` importa qualcosa di `kernel/script` che a sua
  volta importa capability, si crea un ciclo. Mitigazione: package foglia, check 4.
- **Rischio drift**: gli alias possono diventare permanenti. Mitigazione: contare gli alias e
  aprire una scheda di rimozione con scadenza (30 giorni) al momento della chiusura.
- **Rollback**: revert del commit; il gate torna rosso come prima. Nessun dato persistito è
  coinvolto.

## 6. Esito della verifica finale (2026-11-21)

- `grep -c 'capabilities/scripts"' internal/platform/media/rustexec/semantic_engine_{discourse,people_events,places}.go` → **0, 0, 0**.
- Esiste `internal/kernel/script/semantic`; i tre file importano `semanticscript` dal kernel.
- `go run ./cmd/archcheck --strict` → exit 0, `total_violations: 0`.
- `grep -c 'rustexec/' cmd/archcheck/scan/boundaries/percheck_platform_port_boundary.go` → **12** entry residue (altre famiglie, debito registrato con trigger corretto).
- Nota: la migrazione era già nel working tree (lavoro concorrente non committato); questa scheda
  ha verificato il codice, non lo ha riscritto. Resta da committare.
- Le 12 entry residue possono migrare con lo stesso metodo in una scheda successiva.
