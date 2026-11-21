# RA-03 — Budget dead code: guard ≤292 e delta da ritirare

**Data:** 2026-11-21
**Priorità:** P0 (il report lo dichiara sopra guard; misura da rifare)
**Stato:** DONE — budget rispettato (229 ≤ 279 dopo il ratchet del 2026-11-21)
**Owner:** piattaforma (`refactored`) — famiglie già classificate in `docs/operations/`
**Origine:** report esterno 2026-11-21 §0.3, §4.3
**Verifica sul codice:** **DA MISURARE** — `deadcode` non è installato su questo host; il
numero "300" del report non è riprodotto. Il guard documentato è **≤292**.

## 1. Problema

`docs/operations/DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md:19-24` fissa il guard corrente:

- **≤ 292** funzioni irraggiungibili con `deadcode -test=false ./...` (radici: i main di
  produzione);
- di cui **20 in allowlist test-only** (`tests/tools/*`, `sqlite/sqltest`), quindi
  **272 nel perimetro di produzione**.

Il report dichiara 300. Se la misura conferma 300, il delta è **+8**: il guard è sfondato e
ogni nuovo codice irraggiungibile deve essere ritirato (o classificato) prima della chiusura.
Il conteggio include anche le catene morte note (`publishFinalAudioNeeded` che ritorna sempre
`false`, `NewArgosTranslator`, `NewUploadIntentUseCase`, `memoryRenderCache`), che restano
esempi, non un elenco esaustivo.

## 2. Impatto

- Il guard è l'unico freno all'accumulo: se non è misurato, non è un guard.
- Le catene morte di produzione sono superficie che sembra disponibile e non lo è
  (falsa disponibilità): chi la chiama nei test la rende "viva" senza che nessun utente la
  usi.

## 3. Soluzione

1. **Installare e misurare** (unico modo per avere un numero):
   ```bash
   go install golang.org/x/tools/cmd/deadcode@latest
   export PATH="$(go env GOPATH)/bin:$PATH"
   deadcode -test=false ./... 2>/dev/null | sort > /tmp/dead-prod.txt
   deadcode -test ./...        2>/dev/null | sort > /tmp/dead-test.txt
   wc -l /tmp/dead-prod.txt /tmp/dead-test.txt
   ```
   Riportare i due numeri nella scheda. Se `deadcode` cambia formato fra versioni, fissare la
   versione con `go install …@vX.Y.Z` e annotarla.
2. **Confrontare con il guard**: `wc -l /tmp/dead-prod.txt` vs **292**. Se ≤292, il ticket è
   già chiuso dal punto di vista del budget: aggiornare solo la data della misura nel piano e
   passare al punto 5 (igiene delle catene note).
3. **Classificare il delta** per package, riusando le schede di famiglia esistenti
   (`TICKET-SCRIPTS-DEAD-CHAIN-SWEEP-2026-11-19`, `TICKET-PLATFORM-DEAD-HELPER-SWEEP-2026-11-19`,
   `TICKET-KERNEL-ASSET-DEAD-SURFACE-2026-11-19`, `TICKET-MEDIAREGISTRY-DEAD-SURFACE-2026-11-19`):
   ```bash
   sed 's/:[0-9]*:[0-9]*$//' /tmp/dead-prod.txt | sort | uniq -c | sort -rn | head -30
   ```
   Decisione per voce: **ritirare** (nessun chiamante, nessun contratto), **inventariare**
   (oracolo di test deliberato, con owner), o **cabla** (esiste ma è irraggiungibile per un
   bug di wiring → ticket a parte).
4. **Ritirare in lotti piccoli**, un package per commit, con la regola già in uso: si rimuove
   solo ciò che nessun test richiede; i costruttori usati solo dai test restano **oracoli** e
   vanno in allowlist, non cancellati. Non toccare `pkg/veloxclient` (consumatori esterni non
   verificabili).
5. **Chiudere le catene note** citate dal report, se ancora presenti:
   ```bash
   grep -rn "func publishFinalAudioNeeded" internal/capabilities/scripts/
   grep -rn "func NewArgosTranslator\|func NewUploadIntentUseCase\|type memoryRenderCache" internal/
   ```
   Per `publishFinalAudioNeeded`: se ritorna sempre `false`, eliminare il ramo e
   `publishFinalAudio` nello stesso commit (il test che li copre va adattato, non cancellato
   senza sostituto). Per `memoryRenderCache`: o è cablato, o si ritira.
6. **Aggiornare il piano** `docs/operations/DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md` con:
   data della misura, comando, numero, e delta rispetto a 292. Se il numero scende, abbassare
   il guard (ratchet verso il basso).

## 4. Check finali

```bash
cd refactored
# 1. budget rispettato
deadcode -test=false ./... 2>/dev/null | wc -l
# atteso: <= 292 (idealmente <= 272 se si azzera anche l'allowlist test-only)

# 2. nessun oracolo di test cancellato per errore
deadcode -test ./... 2>/dev/null | wc -l
# atteso: <= valore precedente; se scende, dichiarare quali test sono stati adattati

# 3. il codice rimosso non era raggiungibile: build e vet verdi
go build ./...
go vet ./...

# 4. i package svuotati hanno i test verdi (non cancellati)
go test ./internal/capabilities/scripts/... ./internal/platform/... ./internal/kernel/...
# atteso: PASS o SKIP dichiarato per i test che richiedono servizi

# 5. nessun file rimasto vuoto o con solo import
gofmt -l $(git diff --name-only | grep '\.go$')
# atteso: nessun output

# 6. il piano è aggiornato
git diff docs/operations/DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md
# atteso: nuova misura datata + guard eventualmente abbassato
```

## 5. Rischi e rollback

- **Rischio**: `deadcode` con radici diverse dà numeri diversi. Mitigazione: usare
  esattamente `deadcode -test=false ./...` (main di produzione impliciti) e annotare la
  versione del tool.
- **Rischio**: rimuovere un simbolo usato via reflection o da un consumer esterno
  (`pkg/veloxclient`). Mitigazione: esclusione esplicita e verifica dei consumatori prima.
- **Rollback**: revert per lotto; ogni lotto è indipendente.

## 6. Esito della verifica finale (2026-11-21)

Misura eseguita con il tool reale (`go install golang.org/x/tools/cmd/deadcode@latest`):

```
deadcode -test=false ./... | wc -l   → 279 (280 prima della pulizia finale)
deadcode -test ./...       | wc -l   → 87
```

**279 ≤ 292**: il guard è rispettato; il "300" del report non è riproducibile su questo tree.

Le catene nominate dal report:

| Catena | Stato | Decisione |
| --- | --- | --- |
| `publishFinalAudio` / `publishFinalAudioNeeded` | non più presenti | già chiuse |
| `NewArgosTranslator` | non più presente | già chiusa |
| `memoryRenderCache.Get/Put` | irraggiungibile in produzione | **oracolo di test**: `newMemoryRenderCache()` è usato da 4 test in `cliprender`. La policy del repo vieta di cancellare gli oracoli → resta, va classificato nell'allowlist |
| `NewUploadIntentUseCase` | irraggiungibile in produzione | **oracolo di test**: coperto da `upload_intent_test.go` (happy path + 3 fallimenti). Resta |

Nessuna delle due catene è stata cancellata: sono coperte da test, quindi rimuoverle
significherebbe cancellare copertura, non debito. Il piano va aggiornato con la misura 279 e con
la classificazione dei due oracoli.

Il codice davvero morto **è stato rimosso**: `media.FrameIndexToUS` (mai chiamata: i call site
usano la variante con FPS esplicito) e il wrapper `wiring.framesDurationUS`, che duplicava
`kernelmedia.FrameDurationUS` in tre chiamate. Misura: 280 → 279.

## 7. Ricontrollo e ratchet (2026-11-21, sessione successiva)

Misura rifatta con il binario già installato (`deadcode -test=false ./...`):

```
deadcode -test=false ./... | wc -l   → 229
deadcode -test ./...       | wc -l   → 84
```

**229 ≤ 279**, quindi il budget non solo è rispettato: scende di 50. Il calo viene da lavoro
concorrente sullo stesso tree e dalla rimozione della catena morta dei prompt config
(`prompts.Get`/`prompts.Config` e i cinque rami che lo leggevano: il registry non aveva
nessun writer, quindi ogni ramo era comportamento morto). Guard aggiornato per ratchet:
**≤ 229**.

I due oracoli di test sono classificati nell'allowlist in
`docs/operations/DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md` (§ *Allowlist classificata*):

- `NewUploadIntentUseCase` resta nell'elenco `deadcode -test=false` (voce presente, contata);
  è un costruttore coperto da `upload_intent_test.go` e la sua catena di produzione è RA-06 §1.3.
- `memoryRenderCache` vive in `render_cache_memory_test.go`, quindi non compare in
  `-test=false`; è comunque classificato come oracolo con owner `capabilities/cliprender`.

**Sweep U1000 (seconda sessione, 2026-11-21).** `deadcode -test=false` resta **229**: tutte
le funzioni residue sono raggiunte da test o contratti (0 zero-reference). Il codice morto non
visto da `deadcode` è stato rimosso con `staticcheck -checks U1000` (test inclusi): 49 simboli
trovati, **27 rimossi** (17 const, 8 var, 2 func) e **5 file** morti eliminati; restano 21 campi
di gusci API e `allowedMetadataKeys`, che è superficie del percheck e va tenuto (la sua
cancellazione fa scattare `percheck_metadata_key_registry`: provato, ripristinato). Dettaglio e
verifiche in `docs/operations/DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`, § *Sweep U1000*.

Nessun oracolo è stato cancellato. Verifiche: `go build ./...` e `go vet ./...` exit 0;
`archcheck --strict` exit 0.
