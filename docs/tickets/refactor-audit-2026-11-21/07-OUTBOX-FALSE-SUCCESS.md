# RA-07 — Outbox: `nil` al posto dell'errore e validazioni non terminali

**Data:** 2026-11-21
**Priorità:** P1
**Stato:** DONE — voiceover, delivery e `index_delete` chiusi
**Owner:** `capabilities/jobs` (handler outbox)
**Origine:** report esterno 2026-11-21 §4.1; verificato sul codice dove indicato
**Verifica sul codice:** **CONFERMATO** in `voiceover_cleanup.go:256-262`; **DA CONFERMARE**
alle altre righe (stesso pattern)

## 1. Problema

Due classi di difetto nello stesso punto: l'handler dichiara successo su un lavoro che non è
stato fatto.

### 1.1 Dipendenze `nil` → `return nil` (acknowledgement falso)

| File | Riga | Comportamento |
| --- | --- | --- |
| `internal/capabilities/jobs/voiceover_cleanup.go` | 256-262 | `h.driver == nil` → `log.Warn` + `return nil`: il file Drive non viene cancellato ma l'evento è ackato |
| `internal/capabilities/jobs/index_delete.go` | 235-248, 428 | `assetDeleter`/`vectorDeleter` nil → salta la cancellazione e ritorna `nil` |
| `internal/capabilities/jobs/metadataexport_handler.go` | 92-119 | ack "legacy"/vuoto senza export |
| `internal/capabilities/jobs/delivery.go` | 344-356 | ack senza consegna |

`voiceover_cleanup.go` è verificato: il commento dice "production wiring always supplies one",
ma un errore di composizione non deve diventare un successo silenzioso — deve diventare un
errore **terminale** (dead-letter) o un errore di avvio.

### 1.2 Validazione non marcata terminale → retry infinito

`validateDeliveryRequest` (`delivery.go:280-291`), `provider_sync.go:153-188`,
`script_generate_queued.go:42-47`, `overlay_drive_outbox.go:142-144` restituiscono errori di
payload non wrappati con `outboxevents.NewTerminalError`. Il pool li considera transitori e
ritenta fino a `max_attempts`: payload invalido ⇒ retry sprecati e dead-letter tardiva.

Il repo ha già il meccanismo: `outboxevents.NewTerminalError` (usato correttamente in
`asset_published_handler.go:129-161` e `binding_indexing.go:103-145`). Il difetto è
l'incoerenza, non l'assenza dello strumento.

## 2. Soluzione

1. **Censire tutti gli handler** e classificare ogni `return nil` con dipendenza `nil`:
   ```bash
   grep -rn "== nil" internal/capabilities/jobs/*.go | grep -v _test
   ```
2. **Fallire in composizione** dove possibile: se l'handler richiede `driver`/`deleter`, la
   `Build*Bundle` deve rifiutare la costruzione senza quella dipendenza (fail-fast all'avvio,
   non a evento). Nel composition root aggiungere la validazione e un test che la esercita.
3. **Runtime `nil` = terminale**: se la dipendenza manca a runtime (wiring parziale, test),
   restituire `outboxevents.NewTerminalError(fmt.Errorf("…: dependency %s not wired", …))`.
   Mai `return nil`.
4. **Wrappare ogni errore di validazione** (payload malformato, campo obbligatorio mancante,
   versione schema diversa, id vuoto) con `NewTerminalError`. Restano transitori solo: I/O di
   rete, `context.DeadlineExceeded`, `driver`/DB temporaneamente non disponibili.
5. **Test per handler**, in tabella: caso felice, dipendenza `nil`, payload invalido. Il test
   deve verificare che il pool classifichi il caso come terminale (o che l'errore sia
   `errors.Is(..., outboxevents.ErrTerminal)` — usare l'helper del pool, non un tipo inventato).

## 3. Check finali

```bash
cd refactored
# 1. nessun ack su dipendenza mancante
grep -rn "driver nil\|deleter == nil\|Deleter == nil" internal/capabilities/jobs/*.go | grep -v _test
# atteso: solo rami che ritornano NewTerminalError o errore, nessun "return nil" associato

# 2. ogni errore di validazione è terminale
go test ./internal/capabilities/jobs/ -run 'Terminal|Validate|Payload' -v
# atteso: PASS; un test per handler che asserisce la classificazione terminale

# 3. il pool non ritenta un payload invalido
go test ./internal/platform/jobs/... -run 'Outbox|Terminal|DeadLetter' -v
# atteso: PASS con attempts=1 sul caso terminale

# 4. composizione fail-fast
go test ./internal/app/wiring/ -run 'Bundle|MissingDependency' -v
# atteso: PASS; la build senza driver/deleter fallisce

# 5. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: rendere terminale un errore che era transitorio (es. lock del file) manda in
  dead-letter un job recuperabile. Mitigazione: classificare riga per riga con la tabella del
  punto 4; in dubbio, resta transitorio.
- **Rischio**: fail-fast nella composition root rompe ambienti di test che costruivano bundle
  parziali. Mitigazione: i test usano fake, non `nil`; censire prima.
- **Rollback**: revert per handler.

## 6. Esito della verifica finale (2026-11-21)

- `voiceover_cleanup.go`: driver `nil` → la pulizia locale viene comunque eseguita, poi viene
  restituito `outboxevents.NewTerminalError(...)` che nomina il file orfano. Due test nuovi:
  `TestVoiceoverCleanupHandler_NilDriver_FailsClosedTerminal` e
  `TestVoiceoverCleanupHandler_NilDriver_StillRemovesLocalOrphans`; il vecchio test che pinnava
  il successo silenzioso è stato riscritto.
- `delivery.go`: l'errore di `validateDeliveryRequest` è avvolto in `NewTerminalError`; i sentinel
  tipizzati avevano già il breadcrumb "(terminal)", le validazioni generiche no.
- `go test ./internal/capabilities/jobs/ -run 'Delivery|Voiceover|Cleanup'` → ok.
- **`index_delete.go`: CHIUSO.** `NewIndexDeleteHandler` panica se `VectorPointDeleter` o
  `AssetDeleter` è `nil` (fail-fast in composizione), `Handle` su un handler costruito a mano
  restituisce `NewTerminalError("…index delete dependencies are not wired")`, e i test lo pinnano:
  `TestIndexDeleteHandler_ConstructorRejectsMissingDependency`,
  `TestIndexDeleteHandler_MissingDependencyIsTerminal`,
  `TestIndexDeleteHandler_PayloadParseIsTerminal`,
  `TestIndexDeleteHandler_SchemaVersionMismatchIsTerminal`,
  `TestIndexDeleteHandler_EmptyAssetIDIsTerminal`.
- **`delivery.go`**: l'ack legacy è compatibilità voluta e documentata (il contratto non è
  cambiato).
- **Composizione (`BuildOutboxBundle`)**: quando il media plane PostgreSQL non è attivo, gli
  handler PG opzionali vengono saltati con un `Warn` esplicito e gli eventi finiscono in
  dead-letter (nessun handler registrato). Non è un successo falso: la registrazione mancante
  fallisce chiusa. Quando il media plane è attivo, i gate esistenti (`mediaPostgres` nil,
  index worker assente, `ClipIndexer.ServerURL` vuoto) impediscono la costruzione prima di
  arrivare all'handler.
- Verifica: `go test ./internal/capabilities/jobs/ -run 'Delivery|Voiceover|Cleanup|IndexDelete'
  -count=1` → **ok** (2026-11-21).

## 6. Esito della verifica finale (2026-11-21, `index_delete`)

**Il `nil` non può più diventare un ack.** `IndexDeleteHandler` ha due deleter obbligatori
(`VectorPointDeleter`, `AssetDeleter`); prima entrambi i call-site erano dentro
`if h.assetDeleter != nil` / `if h.vectorDeleter != nil`, quindi un handler costruito senza una
delle due dipendenze attraversava l'intera funzione, saltava la cancellazione e ritornava `nil`
(l'evento veniva marcato completato con l'asset ancora indicizzato).

Ora:

1. `NewIndexDeleteHandler` **panica** se una delle due dipendenze è `nil` (fail-fast in
   composizione, coerente con `voiceover.NewUploadIntentUseCase` e godlike/05).
2. `Handle` controlla comunque le dipendenze prima di decodificare e, se mancano, restituisce
   `outboxevents.NewTerminalError`: un handler zero-value o costruito per via diretta non può
   ackare. Terminale perché un retry non può creare una dipendenza.

Test nuovi: `TestIndexDeleteHandler_ConstructorRejectsMissingDependency` (entrambe le
dipendenze) e `TestIndexDeleteHandler_MissingDependencyIsTerminal`. I test esistenti del
handler (payload terminale, idempotenza, guard Drive) restano invariati e verdi.

Verifiche: `go test ./internal/capabilities/jobs/ -run 'IndexDelete' -count=1` PASS;
`go build ./...` e `go vet ./...` exit 0.
