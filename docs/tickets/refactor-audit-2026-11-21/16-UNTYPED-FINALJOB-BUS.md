# RA-16 — Bus non tipizzato del final job: `map[string]any` e ri-serializzazioni

**Data:** 2026-11-21
**Priorità:** P1 (fragilità + lavoro inutile sul percorso caldo)
**Stato:** **DONE (2026-11-21)** — builder diviso in preflight, scene builder e finalizzazione; `buildFinalJobPayloads` 92→3, massimo 30; porte e wire restano tipizzati
**Owner:** scripts capability — final job
**Origine:** report esterno 2026-11-21 §1.3, §2.1, §5.1; verificato sul codice
**Verifica sul codice:** **CONFERMATO**, incluse le tre riassegnazioni ridondanti

## 1. Problema

### 1.1 Interfacce e payload su `map[string]any` / `[]any` + stringhe JSON

`internal/capabilities/scripts/final_job_payload.go:46-72` definisce
`FinalJobVideoSegmentPreparer` e `FinalJobAssetResolver` su `map[string]any`, mentre i
contratti tipizzati **esistono già** in `finaljob/contract.go:81-189`
(`RuntimeAssetV1`, `CompiledPlanV2`). Il piano viene serializzato in stringa JSON e poi
ri-parsato, mutato e ri-serializzato lungo
`alignFinalJobCompiledPlanAssetIDs` → `retainFinalJobRuntimeAssets` →
`prepareFinalJobVideoSegments` → `finaljob.BuildEnvelope`.

### 1.2 Copie ridondanti nello stesso blocco

`final_job_payload.go:565-599`: `pre["compiled_render_plan_json"]`,
`pre["compiled_render_plan_sha256"]` e `finalize["runtime_assets"]` sono assegnati due volte
(569/571/576 e 595/596/597). È innocuo a runtime ma dimostra che il blocco non ha una
struttura: chi lo modifica non sa quale assegnazione è quella vera.

### 1.3 Duplicazione delle conversioni

`finalJobInt64` / `int64Value`, `finalJobStringValue` / `stringValue`, `boolValue` /
`boolFromAny`: tre coppie di funzioni equivalenti in file diversi.

### 1.4 Formule frame→µs replicate

- `final_job_plan_source_range.go:164` (`finalJobFrameStartUS`)
- `final_job_plan_prepare.go:299` (`finalJobFrameDurationUS`)
- `app/wiring/final_job_placement.go:168` (`framesDurationUS`, FPS fisso)
Sono la stessa formula; il FPS canonico è già in `kernel/media/assembly_contract.go`
(`CanonicalFPSNum/CanonicalFPSDen`).

## 2. Soluzione

1. **Sostituire le mappe con struct tipizzate** in `scripts/finaljob`:
   ```go
   type VideoSegmentPrepRequest struct { Plan CompiledPlanV2; Assets []RuntimeAssetV1; … }
   type VideoSegmentPrepResult  struct { Plan CompiledPlanV2; Segments []PreparedSegment; … }
   ```
   `FinalJobVideoSegmentPreparer` e `FinalJobAssetResolver` diventano interfacce su questi
   tipi. Il confine HTTP/wire resta `map[string]any` **solo** nella funzione di
   encoding/decoding, una per direzione.
2. **Una sola serializzazione**: il piano compilato è serializzato una volta (quando si
   costruisce l'envelope) e non più ri-parsato all'interno della stessa chiamata. Se un passo
   ha bisogno del piano, riceve la struct, non la stringa.
3. **Eliminare le assegnazioni doppie** in `final_job_payload.go:595-597` e sostituire il
   blocco con la costruzione di una struct `FinalJobSubmitPayload` con tag JSON. Il commento
   "The Master has a strict FINALIZE DTO" resta, applicato alla struct.
4. **Una sola formula frame→µs** in `kernel/media`:
   ```go
   func FrameIndexToUS(frame int64) int64   // usa CanonicalFPSNum/Den
   func FrameDurationUS() int64
   ```
   Sostituire i tre siti; vietare nuove costanti `24`/`1.0/24` con un check.
5. **Una sola famiglia di coercion** (`finaljob.Int64`, `finaljob.String`, `finaljob.Bool`) con
   test tabellari sui casi limite (nil, stringa numerica, float, bool come stringa). Le sei
   funzioni attuali si riducono a tre e i chiamanti importano da un solo posto.
6. **Test di round-trip**: un payload tipizzato → JSON → struct deve essere identico
   (`reflect.DeepEqual`) e l'hash `compiled_render_plan_sha256` deve restare stabile sui
   golden già presenti. Questo è il check che impedisce di cambiare il wire per sbaglio.

## 3. Check finali

```bash
cd refactored
# 1. niente mappe nel contratto del preparer/resolver
grep -rn "map\[string\]any\|\[\]any" internal/capabilities/scripts/final_job_payload.go
# atteso: solo nelle funzioni di encode/decode del wire, non nelle interfacce

# 2. nessuna assegnazione doppia
awk 'NR>=560 && NR<=605' internal/capabilities/scripts/final_job_payload.go | grep -c "compiled_render_plan_json"
# atteso: 1 (o 2 se una è nel decode documentato, con commento)

# 3. una sola formula frame→µs
grep -rn "1000 \* 1000\|1_000_000\|/ 24\|float64(.*)/24" --include=*.go internal/ | grep -v _test
# atteso: solo kernel/media; nessuna occorrenza nei tre file citati

# 4. una sola famiglia di coercion
grep -rn "func finalJobInt64\|func int64Value\|func finalJobStringValue\|func stringValue\|func boolValue\|func boolFromAny" --include=*.go internal/
# atteso: al massimo le tre funzioni unificate + alias documentati

# 5. wire invariato sui golden
go test ./internal/capabilities/scripts/ -run 'FinalJobPayload|Envelope|RoundTrip|SHA' -v
# atteso: PASS; hash e JSON identici ai golden esistenti

# 6. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: il wire JSON cambia (ordine campi, `omitempty`, tipi numerici) e il Master
  rifiuta. Mitigazione: round-trip + hash golden prima/dopo; il wire si congela nel test.
- **Rischio**: la struct introduce un ciclo `finaljob ↔ scripts`. Mitigazione: `finaljob`
  resta foglia (già lo è); se serve un tipo del root, si sposta il tipo.
- **Rollback**: revert per passo (1, 3, 4, 5 indipendenti).

## 6. Esito della verifica finale (2026-11-21)

- Rimosse le tre riassegnazioni ridondanti in `final_job_payload.go`.
- Nuovo `internal/kernel/media/frame_time.go`: `FrameIndexToUS(AtFPS)`, `FrameDurationUS(AtFPS)`.
  `finalJobFrameStartUS` e `finalJobFrameDurationUS` ora delegano (una sola formula, FPS canonico
  dal kernel); il wrapper `wiring.framesDurationUS` è stato eliminato e i tre call site usano
  direttamente `kernelmedia.FrameDurationUS`.
- `go test ./internal/capabilities/scripts/ -run 'FinalJob|Frame|Placement'` e
  `go test ./internal/app/wiring/ -run 'FinalJob|Placement|Frame'` → ok.
- **Aperto**: sostituzione di `map[string]any`/`[]any` con `CompiledPlanV2`/`RuntimeAssetV1` e
  unificazione delle sei funzioni di coercion. Richiede il round-trip sul wire e va coordinato
  con RA-12.

## 7. Chiusura del perimetro tipizzato (2026-11-21)

- `FinalJobVideoSegmentPreparer`, `FinalJobAssetResolver` e `finalJobStockAssetResolver`
  ricevono/restituiscono `FinalJobRuntimeAssetV1`; le implementazioni production e i fake dei
  test sono state aggiornate. Le mappe entrano nel tipo solo tramite
  `finaljob.RuntimeAssetFromMap` e ne escono solo tramite `RuntimeAssetV1.Map`.
- `RuntimeAssetV1.UnmarshalJSON` conserva i campi non canonici in `Additional`; il round-trip
  `map → typed → map` preserva `duration_ms`, `copy_eligible`, `profile_id` e gli altri campi
  transport.
- Una sola famiglia di coercion: `finaljob.Int64`, `finaljob.String`, `finaljob.Bool`, con test
  tabellari. Gli alias di package mantengono i nomi storici senza duplicare la logica.
- Golden test: `TestEnvelopeV1RoundTripKeepsPlanBytesAndHash` e
  `TestRuntimeAssetV1RoundTripPreservesAdditionalFields` in
  `internal/capabilities/scripts/finaljob/contract_roundtrip_test.go`. Verificano
  `reflect.DeepEqual`, byte canonici del piano e `compiled_render_plan_sha256` stabile.
- **Residuo dichiarato**: `buildFinalJobPayloads` continua a costruire mappe interne ed è a
  complessità 92; non è stato riscritto in questa sessione. Il contratto delle porte è tipizzato,
  ma la rimozione completa delle mappe dal builder legacy resta aperta e non viene rivendicata.

Verifiche eseguite: `go test ./internal/capabilities/scripts/...`, `./internal/app/wiring/...`,
`./internal/platform/media/rustexec/...` PASS; `go build ./...`, `go vet ./...` exit 0;
`go run ./cmd/archcheck --strict` exit 0 (`passed=true`, 0 violazioni); `gofmt -l` pulito sui file
toccati; `git diff --check` pulito.

## 8. Chiusura del builder (2026-11-21)

**DONE.** Il monolite non esiste più: la decisione visuale e la serializzazione sono due fasi
separate, con un orchestratore che non contiene policy.

| Funzione | Prima | Dopo |
| --- | ---: | ---: |
| `buildFinalJobPayloads` | 92 | **3** |
| `finalJobPreflight` | (nel monolite) | 12 |
| `finalJobSceneBuilder.appendSegment` | (nel monolite) | 17 |
| `appendFixedSegment` / `appendClipScene` / `appendConcreteStockScene` | (nel monolite) | 17 / 8 / 7 |
| `finalJobFinalizePayload` | (nel monolite) | 30 |

Misura: `go run github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0` su `final_job_payload.go` e
`final_job_payload_builder.go`; nessuna funzione supera 30. Il builder delle scene e la
finalizzazione vivono nello stesso file `final_job_payload_builder.go`: il builder non
serializza nulla, la finalizzazione è l'unico punto che produce i due payload Master. Le mappe residue sono il wire versionato del piano compilato, non un contratto di
porta: `FinalJobVideoSegmentPreparer`/`FinalJobAssetResolver` restano tipizzati.

Test final-job: `go test ./internal/capabilities/scripts -run 'BuildFinalJobPayloads|FinalJob'`
PASS. Il package completo ha un golden overlay rosso (`entity_cards_promotable_media`,
`mixed_chain` vs `image_chain`) in file overlay già modificati da lavoro concorrente non
committato, estraneo a questo diff: non viene dichiarato verde.

Verifiche: `go build ./...` exit 0; `go vet ./...` exit 0; `go run ./cmd/archcheck --strict`
exit 0, 0 violazioni, 67 warning; `gofmt -l` pulito sui file toccati; `git diff --check` pulito.
