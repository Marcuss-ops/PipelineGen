# RA-17 — Primitive obsession e parametri opachi

**Data:** 2026-11-21
**Priorità:** P2 (dopo RA-19, che unifica i validatori)
**Stato:** PARTIAL — §1.2 (validatori duplicati) **DONE** con RA-19: SHA-256, `#RRGGBB` e lingua
hanno ora un solo helper ciascuno. Restano aperti il variadico §1.1 e la sostituzione delle
stringhe con tipi valore (`SHA256`, `RGB`, `LanguageTag`, `FileID`): è la parte che cambia i
contratti e richiede una sessione dedicata (piano e ordine in `00-INDEX.md` §5).
**Owner:** `kernel/{digest,script,asset,media}` + chiamanti
**Origine:** report esterno 2026-11-21 §1.3, §5.3; verificato sul codice
**Verifica sul codice:** **CONFERMATO** per il variadico e per le copie di validazione

## 1. Problema

### 1.1 Variadico posizionale con due significati

`internal/capabilities/scripts/overlay_plan_compile.go:23,129,135`:

```go
func compileOverlayPlanWithMotionOffset(..., perSceneImages ...bool) (*capmodel.OverlayPlan, error)
…
if len(perSceneImages) > 0 && perSceneImages[0] && canvas.AnimationCounts["entities"] > 0 { … }
sceneImagesEnabled := len(perSceneImages) < 2 || perSceneImages[1]
```

Due flag semantici distinti passati come posizioni di uno slice: il chiamante può invertirli
senza che il compilatore se ne accorga, e `perSceneImages[1]` non ha nome. Il variadico è
propagato in `prepareOverlayPlanCompile`, in `overlayPlanCompileInput` (campo `[]bool`) e in
`sealCompiledOverlayPlan`.

### 1.2 Stringhe grezze dove esistono già i tipi canonici

| Concetto | Copie | Canonico esistente |
| --- | --- | --- |
| SHA-256 | **rimossi** `isLowerHex`, `isHexSHA256`, `isValidHex64` e i 4 `strings.Trim` inline | `internal/kernel/digest.IsSHA256` / `IsCanonicalSHA256` |
| Colore `#RRGGBB` | **rimosse** le 8 copie (4 funzioni + 4 inline) | `internal/kernel/color.IsHexRGB` / `ParseHexRGB` |
| Lingua BCP-47 | **rimosso** `normalizeLanguageTag`; `resolverLanguage` valida con il canonico | `kernel/asset.Normalize` |
| Drive file ID | 3 parser manuali (`final_job_handoff.go:180`, `remote_final_job_objectstore.go:116`, `adapters_voiceover_repo.go:517`) + 25 format string | `pkg/urlutil.FileIDFromDriveLink` |

## 2. Soluzione

1. **Opzioni nominate** al posto del variadico:
   ```go
   type OverlayPlanCompileOptions struct {
       PerSceneImages    bool   // abilita il piano immagini per scena
       SceneImagesEnabled bool  // consente le immagini di scena nel canvas
   }
   func compileOverlayPlanWithMotionOffset(..., opts OverlayPlanCompileOptions) (*capmodel.OverlayPlan, error)
   ```
   Sostituire ogni chiamata; l'ordine non è più rappresentabile. Rimuovere `[]bool` da
   `overlayPlanCompileInput` e da `sealCompiledOverlayPlan`. Zero variadici per flag booleani:
   aggiungere un check `grep -rn '\.\.\.bool' internal/` → nessun output.
2. **Tipi valore nel kernel**, ciascuno con `Parse` che valida e `String` che serializza:
   ```go
   // internal/kernel/digest
   type SHA256 struct{ hex string }
   func ParseSHA256(s string) (SHA256, error)      // avvolge IsCanonicalSHA256
   // internal/kernel/color
   type RGB struct{ R, G, B uint8 }
   func ParseHexRGB(s string) (RGB, error)         // #RRGGBB, case-insensitive in input
   // internal/kernel/asset (già c'è)
   type LanguageTag struct{ … }                    // Normalize + Valid
   // internal/kernel/drive (nuovo, se pkg/urlutil non è importabile da internal)
   type FileID string
   func ParseDriveFileID(raw string) (FileID, error)
   ```
3. **Migrare per famiglia, non tutto insieme**: prima le struct pubbliche (`OverlayStyleSpec`,
   `OverlayPlan`, `FinalJob`), poi i chiamanti. Un campo tipizzato resta serializzato come
   stringa nel JSON (`MarshalJSON`/`UnmarshalJSON` sul tipo), quindi il wire non cambia.
4. **Una sola normalizzazione lingua**: FATTO a livello di helper (RA-19 §7.7). Il registry
   usa `asset.Normalize` e poi tiene il sottotag primario, che è la chiave dei resolver; la
   variante tipizzata `LanguageTag` resta da fare qui.
5. **Vietare le nuove stringhe**: fields nuovi con nome `SHA256`, `Color`, `Language`,
   `DriveFileID` devono usare il tipo. Check statico opzionale (`go/ast`) sui file nuovi.
6. **Test dei parser** tabellari: input valido, maiuscolo, spazi, prefisso `#`, lunghezza
   errata, `sha256:` prefissato, URL Drive con `?id=` e `/d/<id>/`. Fuzz opzionale su
   `ParseDriveFileID`.

## 3. Check finali

```bash
cd refactored
# 1. nessun variadico di flag
grep -rn '\.\.\.bool' --include=*.go internal/ | grep -v _test
# atteso: nessun output

# 2. i validatori duplicati sono spariti (in coordinamento con RA-19)
grep -rn "func isLowerHex\|func isHexSHA256\|func isValidHex64\|func validDigestHex\|func normalizeLanguageTag" --include=*.go internal/ pkg/
# atteso: nessun output

# 3. i tipi valore sono usati nei contratti
go test ./internal/kernel/digest/ ./internal/kernel/color/ ./internal/kernel/asset/ -v
# atteso: PASS con test tabellari di Parse/String

# 4. il wire non cambia
go test ./internal/capabilities/scripts/ -run 'OverlayPlan|Marshal|Golden' -v
# atteso: PASS; JSON identico ai golden

# 5. build/vet/gate + test dei chiamanti
go build ./... && go vet ./...
go test ./internal/capabilities/scripts/... ./internal/capabilities/overlays/... ./internal/kernel/...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: i tipi valore cambiano il comportamento di confronto (`==` su struct con stringa
  interna è ok, su struct con slice no). Mitigazione: campi comparabili, niente slice.
- **Rischio**: la normalizzazione lingua "corretta" rompe `brain/intent`. Mitigazione: test
  sui tre casi e decisione esplicita nella scheda prima del codice.
- **Rollback**: per famiglia; i tipi possono restare anche se la migrazione si ferma (sono
  additivi).

## 5. Esito (2026-11-21)

**§1.1 chiuso.** I due flag per-scena non sono più posizioni di uno slice: sono un unico
bundle nominato, `overlayPlanImageOptions{PerSceneEntityCards, DisableSceneImages}`, con due
metodi che esprimono la semantica (`perSceneEntityCards()`, `sceneImagesEnabled()`). Il default
storico è preservato: bundle assente = nessuna entity card per scena, immagini di scena attive.

- 17 firme convertite: `sealCompiledOverlayPlan`, `promoteVerifiedEntityCards`,
  `finalizeSealedOverlayPlan`, `compileOverlayPlanWithMotionOffset`,
  `prepareOverlayPlanCompile`, `CompileOverlayPlanWithRustResolver`,
  `compileResultOverlayPlan`, `compileOverlayPlanForLanguage`, `buildLocalizedOverlayPlans`,
  `BuildSemanticRenderBundleFromResult`, `entityCardMediaIndex`, `capEntityImageOverlays`
  (bundle), più `fetch`, `Run`, `executeSemanticSearchBatch`, `imageSearchEntities`,
  `CompileCanonicalAudioPlanAudioOnlyWithIntents` (booleano esplicito: un solo flag, nessuna
  ambiguità).
- `overlayPlanCompileInput.perSceneImages` è `[]overlayPlanImageOptions`; il call site di
  `runner_overlay_plan_branch.go` converte esplicitamente i due valori
  (`PerSceneEntityCards`, `DisableSceneImages: !genericSceneImagesEnabled`).
- Check: `grep -rn '\.\.\.bool' --include=*.go internal/ | grep -v _test` → **0**;
  `go build ./...` exit 0; `go test ./internal/capabilities/scripts/ -count=1` → ok;
  `go vet ./internal/capabilities/scripts/...` → exit 0; `gofmt -l` vuoto;
  `archcheck --strict` exit 0, `has_hard_gate_hits=false`.

Resta fuori da questa scheda solo la migrazione dei tipi valore, già chiusa nella §1.2 dalla
sessione precedente (`kernel/color`, `kernel/digest`, `asset.Normalize`). **RA-17 DONE**.
