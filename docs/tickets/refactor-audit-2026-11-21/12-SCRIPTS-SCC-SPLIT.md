# RA-12 — `capabilities/scripts`: SCC di 117 file e god model `GenerateRequest/Result`

**Data:** 2026-11-21
**Priorità:** P1 (strutturale; abilita RA-02 e RA-16)
**Stato:** CLOSED (SUPERSEDED, 2026-11-24) — i criteri quantitativi sono superati: root
`internal/capabilities/scripts` = **63 file di produzione** (target ≤141; cap di wave ≤65),
quindi la SCC file-level è **≤63** (target <117). Il cap file e la SCC sono stati chiusi da
GOP-01 (`3e0a9e3ae`). Il residuo non coperto — migrazione dei lettori dei campi piatti — è
ora in **BON-01 §Riapertura 2026-11-24**
(`bonifica-complessita-2026-11-23/01-SCRIPTS-RUNNER-FINALJOB-SCC.md`). Nessun lavoro di
codice resta in questa scheda.
**Owner:** scripts capability
**Origine:** report esterno 2026-11-21 §1.1, §5.4
**Verifica sul codice:** **CONFERMATO** il punto di partenza (146 file prod, 40.508 LOC secondo
`architecture/package_hotspots.json`, nota "CONTRACTION BLOCKER MEASURED 2026-11-11: 117-file
strongly-connected closure"); il conteggio SCC va rifatto con lo script.

## 1. Problema

Il package `internal/capabilities/scripts` è una facciata di compatibilità con 146 file
produttivi. La nota di governance dice esplicitamente che:

- a 40.508 LOC e cap 600 righe/file, il minimo assoluto è 68 file;
- l'analisi delle dipendenze mostra una **closure strongly-connected di 117 file** per
  runner e final-job/render-queue;
- solo 5 file sono estraibili senza invertire contratti.

La causa non è la dimensione dei file: sono i **god model** `GenerateRequest` (50+ campi) e
`GenerateResult` (40+ campi) che tengono tutto connesso. Gli split sotto 600 LOC hanno
prodotto "file gemelli" nella stessa directory (128→141 file) senza ridurre la SCC: è
evidenza che il taglio per dimensione non funziona.

## 2. Soluzione — contract-first, per fasi

L'obiettivo non è "meno file" ma "un file per fase, con contratti che non si vedono". Ordine:

1. **Inventario dei campi** (misura, non opinione):
   ```bash
   cd refactored
   awk '/^type GenerateRequest struct/,/^}/' internal/capabilities/scripts/*.go
   awk '/^type GenerateResult struct/,/^}/' internal/capabilities/scripts/*.go
   ```
   Per ogni campo: chi lo **scrive**, chi lo **legge**, in quale fase (testo, audio,
   overlay, final job, pubblicazione). Il risultato è una tabella nella scheda.
2. **Definire i contratti di fase** in sottopackage dedicati (nomi già proposti dal report):
   - `internal/capabilities/scripts/contracts/generation` — input editoriale, canale, lingua,
     budget;
   - `internal/capabilities/scripts/contracts/audio` — timeline, intent SFX/BGM, audio finale;
   - `internal/capabilities/scripts/contracts/finaljob` — piano compilato, asset runtime,
     manifest (in parte esiste già in `scripts/finaljob/contract.go:81-189`).
   Ogni contratto contiene **solo** i campi della sua fase, con tipi già tipizzati.
3. **Migrazione senza big bang**:
   a. aggiungere i sottostrutturati come **campi** (`GenerateRequest.Audio audio.Request`) e
      mantenere i campi piatti come **alias deprecated** con accessor che leggono/Scrivono il
      sottostrutturato (una sola fonte di verità);
   b. migrare i lettori fase per fase;
   c. rimuovere i campi piatti quando nessun lettore li usa (misura con
      `grep -c` per campo e una build che fallisce se ricompaiono).
4. **Tagliare solo ciò che è foglia**: dopo (3), rieseguire lo script SCC del §2 di RA-11. Si
   sposta un file nel sottopackage **solo** se non riporta la SCC dentro il root. Il criterio
   di chiusura è il conteggio SCC e il conteggio file (≤141, RA-02), non il LOC per file.
5. **Vietare i nuovi gemelli**: aggiungere alla policy una regola "nessun nuovo file nel root
   `scripts` senza una voce di contratto" — in pratica il cap di RA-02 fa già da freno, ma
   serve una nota nel `note` dell'hotspot che dica che il taglio ammesso è solo per contratto.
6. **Test di proprietà**: un test che, per ciascun contratto di fase, verifichi che il
   sottostrutturato è popolato e che il campo piatto corrispondente non è più letto
   (grep-based test o `go/ast`). Impedisce il ritorno del doppio binario.

## 3. Check finali

```bash
cd refactored
# 1. dimensione e conteggio
find internal/capabilities/scripts -maxdepth 1 -name '*.go' ! -name '*_test.go' | wc -l
# atteso: <= 141
go run ./cmd/archcheck --strict   # nessuna violazione pkg_size su scripts

# 2. SCC ridotta: rieseguire lo script di RA-11 e incollare il numero
#    atteso: SCC < 117 (e nessun aumento di cicli altrove)

# 3. i contratti di fase esistono e sono usati
go list ./internal/capabilities/scripts/contracts/...
go test ./internal/capabilities/scripts/... -run 'Contract|GenerateRequest|GenerateResult' -v
# atteso: PASS

# 4. nessun campo piatto residuo per la fase migrata
grep -rn "\.<CampoMigrato>" internal/capabilities/scripts/ | grep -v _test
# atteso: nessun output (o solo l'accessor documentato)

# 5. comportamento invariato: suite del package verde
go build ./... && go vet ./...
go test ./internal/capabilities/scripts/...
# atteso: PASS (i test live possono richiedere env: dichiarare SKIP)

# 6. wiring non è peggiorato
go list -f '{{.ImportPath}} {{len .Imports}}' ./internal/app/wiring
```

## 4. Rischi e rollback

- **Rischio**: gli alias deprecated diventano permanenti e la doppia scrittura diverge.
  Mitigazione: accessor unico (una sola fonte), test di proprietà, scadenza annotata.
- **Rischio**: un campo è letto via reflection/JSON tag e sfugge al grep. Mitigazione:
  censimento anche per tag JSON (`grep -rn '"json:"<tag>'`) e test di round-trip.
- **Rischio**: conflitto con lavoro in corso su `scripts` (area ad alto churn). Mitigazione:
  rebase frequente, un contratto per PR.
- **Rollback**: i passi (3a) e (3c) sono reversibili separatamente; i sottopackage possono
  restare anche se la migrazione si ferma.

## 5. Verifica 2026-11-21

**Non chiuso.** La prima fase contract-first è stata consegnata, ma il taglio dei file non è
sicuro e non viene dichiarato.

- **Contratti già estratti** — `kernel/script/finaljob` possiede il wire versionato
  (`EnvelopeV1`, `CompiledPlanV2`, `RuntimeAssetV1`, `BuildEnvelope`);
  `kernel/media/finaljob` possiede il contratto del segmento e i tipi del receipt. La
  capability li espone con alias, quindi l'API pubblica non cambia. Questo abilita RA-15.
- **Inventario completo** — [12-FIELD-INVENTORY.md](12-FIELD-INVENTORY.md): **78** campi di
  `GenerateRequest` e **67** di `GenerateResult`, ognuno con tipo, tag JSON, fase, contratto di
  destinazione e riferimenti statici nel package. Tutti classificati; nessun campo rimosso o
  rinominato.
- **Contratti generation/audio** — aggiunti `contracts/generation/request.go`,
  `result.go` e `contracts/audio/config.go`, `output.go`, con test di round-trip JSON:
  `go test ./internal/capabilities/scripts/contracts/...` PASS. I contratti sono puri (non
  importano `scriptgeneration`), coprono 56 campi di request nella fase generation, 10 nella
  fase audio, 15 di result generation e 9 di result audio. Non sono ancora la fonte di verità:
  la migrazione dei lettori è lo step successivo.
- **Misura SCC (file-level, stesso metodo del ticket)** — `files=142 cyclic_sccs=1 sql=118`.
  La closure resta una sola: contiene runner, model, overlay e l'intero cluster `final_job_*`.
  Non esiste un file foglia da spostare senza invertire un contratto: i cinque file estraibili
  citati dal report non bastano a scendere sotto 117 e non toccano il god model.
- **Cosa manca** — migrare i lettori ai contratti con accessor unico e test di proprietà,
  definire i contratti delle fasi source/overlay/render/submission/publication, e solo dopo
  tagliare i file quando la SCC scende. È una migrazione multi-PR ad alto churn; il working
  tree contiene già oltre 100 file non committati di lavoro concorrente su overlay/scripts,
  quindi avviarla ora mischierebbe due autori nello stesso diff (RA-23).
- **Chiusura 2026-11-24**: RA-12 è CLOSED (SUPERSEDED); il residuo è BON-01 §Riapertura.

## Chiusura 2026-11-24 — SUPERSEDED da GOP-01

La scheda è chiusa perché i due criteri che la tenevano aperta sono stati superati da GOP-01,
non perché la migrazione dei campi piatti sia stata completata.

| Criterio | Misura 2026-11-24 | Esito |
| --- | --- | --- |
| File di produzione nella root `scripts` ≤141 | **63** (`find ... -maxdepth 1 -name '*.go' ! -name '*_test.go'`) | ✅ |
| SCC file-level <117 | **≤63** per costruzione (una SCC non può contenere più file del package); la misura GOP-01 precedente all'estrazione `overlayplan` era 81 | ✅ |
| `regen_hotspots.py --check` | `OK: hotspots fresh` | ✅ |
| Contratti di fase esistenti | `contracts/generation`, `contracts/audio`, `kernel/script/finaljob`, `kernel/media/finaljob`; **12** file di produzione importano i contratti generation/audio | ✅ presenza |
| Contratti come fonte di verità | I campi piatti di `GenerateRequest` (**78**) e `GenerateResult` (**69**) restano letti dai consumer; **18** file fuori `scripts` usano i tipi, **107** importano `contract` | ❌ trasferito a BON-01 |
| `go build`, `go vet`, `go test ./internal/capabilities/scripts/...`, `archcheck --strict` | tutti RC 0 il 2026-11-24; `archcheck` 0 violazioni; hotspot freschi | ✅ |
| Wiring non peggiorato | `go list -f '{{len .Imports}}' ./internal/app/wiring` = **246** (baseline 218 del 2026-11-21; crescita non attribuibile a RA-12) | ⚠️ baseline da riallineare in BON-01 |

Il residuo è ora **BON-01**, riaperto il 2026-11-24 per il solo debito runner/campi piatti:
la migrazione dei lettori, i contratti mancanti (source, submission, publication, final job,
scene, overlay, render, semantic) e il residuo runner dichiarato da GOP-01. La chiusura di
RA-12 non dichiara verde quel lavoro.
