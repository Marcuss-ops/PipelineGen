# Dead code residui — classificazione per consumatori e contratti (2026-10-28)

Risponde a «Serve: classificare i residui per consumatori e contratti, non
eliminare in massa il risultato di `deadcode`» (`docs/stato/refactored.md` §5,
workspace root).

**Aggiornamento 2026-11-02:** il pool di funzioni ordinarie è stato eseguito.
Sono state rimosse 9 delle 11 voci; le 2 rimaste hanno una ragione verificabile
(§«Pool eseguito»). La misura è stata rigenerata dopo il taglio, non ereditata
dal 28-10.

## Misure (riproducibili)

```bash
go install golang.org/x/tools/cmd/deadcode@latest
cd refactored
deadcode ./cmd/server ./cmd/worker ./cmd/admin      # 482 righe (produzione)
deadcode -test ./...                                # 141 righe (produzione+test)
```

Le due misure si compongono così (482 voci prod uniche per coppia file/funzione).
La tabella è la fotografia del 28-10 e resta come storia: dopo il taglio del
02-11 la misura è **483 righe di produzione / 134 con i test**, e il pool di
funzioni ordinarie è sceso da 11 a 2 voci (vedi §«Pool eseguito»).

| Classe | Conteggio | Policy |
|---|---:|---|
| Oracoli di test (raggiungibili solo dai test) | **457** | **TRATTENUTI** — la policy è production-safe, non coverage-safe: costruttori/metodi usati solo dai test restano come oracoli |
| Morti ovunque — metodi su tipi nominati | **13** | Verificare consumatori esterni e vincoli di interfaccia (porte/deserializzatori) PRIMA di qualsiasi futura rimozione |
| Morti ovunque — funzioni ordinarie | **11** | Pool di futura rimozione (nessuna rimozione in questa sessione) |
| Morti ovunque — superficie `pkg/` | **1** | **NON RIMUOVERE** — superficie importabile da consumatori esterni non verificabili (stessa regola di `pkg/veloxclient`) |
| (Helper morti dentro file di test) | ~116 | Fuori perimetro: vivono solo nei file `_test.go` |

Nota sulle misure: `deadcode -test ./...` analizza anche i file di test, quindi
contiene anche helper di test irraggiungibili (non elencati qui). L'intersezione
con la misura di produzione è l'insieme "morti ovunque" qui classificato.

## Morti ovunque — metodi su tipi nominati (13): verificare prima di toccare

Regola (docs/stato/refactored.md §5): per metodi che implementano porte o
deserializzatori verificare i consumatori esterni e i vincoli di interfaccia
prima di toccarli. Da ispezionare caso per caso:

| File | Metodo | Nota di contratto |
|---|---|---|
| `internal/capabilities/translation/ports.go` | `unimplemented.Translate` | stub di porta (port interface) — serve il vincolo di interfaccia |
| `internal/capabilities/voiceover/service/parent_state.go` | `FanoutResultSnapshotAccessor.Snapshot` | accessor di porta parent-state |
| `internal/capabilities/voiceover/service/process.go` | `Service.ensureOutputDir` | metodo privato |
| `internal/capabilities/voiceover/service/process.go` | `Service.processLanguage` | metodo privato |
| `internal/capabilities/voiceover/service/process.go` | `Service.readSwapContext` | metodo privato |
| `internal/capabilities/voiceover/service/types.go` | `BatchItem.UnmarshalJSON` | **deserializzatore** — tenuto per vincolo `json.Unmarshaler` |
| `internal/kernel/media/assembly_contract.go` | `FrameRate.FPSFloat` | metodo di contratto kernel |
| `internal/kernel/media/assembly_contract.go` | `FrameRate.Valid` | validatore di contratto |
| `internal/kernel/media/assembly_contract.go` | `Rational.Valid` | validatore di contratto |
| `internal/kernel/media/assembly_contract.go` | `StreamSignature.Fingerprint` | identità di contratto |
| `internal/kernel/models/registry.go` | `Model.HasVectorSpace` | metodo di modello/registry |
| `internal/kernel/models/registry.go` | `Model.Identity` | metodo di modello/registry |
| `internal/kernel/models/registry.go` | `Model.Validate` | validatore di modello/registry |

## Pool eseguito — 9 rimosse, 2 trattenute (2026-11-02)

Rimosse (nessun consumatore di produzione, di test o di interfaccia):

| File | Funzione |
|---|---|
| `internal/capabilities/overlays/preset_image_selection.go` | `EntitySequenceCameraMotion` (+ la var `entitySequenceCameraMotion`, rimasta senza lettori) |
| `internal/capabilities/translation/types.go` | `DefaultPromoLanguages` (+ il tipo `LanguageTarget`, usato solo da lei) |
| `internal/capabilities/voiceover/service/types_helpers.go` | `buildRequestID` |
| `internal/capabilities/voiceover/service/types_helpers.go` | `normalizeBatchRequestWithConfig` |
| `internal/kernel/media/assembly_contract.go` | `StreamSignatureFromContract` |
| `internal/kernel/media/assembly_contract.go` | `StreamSignatureFromProbe` |
| `internal/platform/ptrutil/ptrutil.go` | `Ptr` |
| `internal/platform/ptrutil/ptrutil.go` | `Bool` (+ il commento orfano di `Str`) |
| `internal/platform/sqlite/assets/imagesregistry/media_asset_mutations.go` | `UpdateMediaAssetContentHashesTx` |

Trattenute, con la ragione che il tool non può vedere:

| File | Funzione | Perché resta |
|---|---|---|
| `internal/capabilities/voiceover/service/filename.go` | `SanitizeFilename` | È irraggiungibile dai main, ma `process.go:193` (catena `processLanguage`, a sua volta irraggiungibile) la chiama: rimuoverla richiede prima la revisione della classe «metodi su tipi nominati» qui sopra, non un taglio isolato. |
| `internal/capabilities/voiceover/service/service.go` | `NewService` | Compare nella misura di produzione ma **non** in `deadcode -test ./...`: è un oracolo di test, e la policy lo trattiene. |

`BoolDefault` (stessa area di `Ptr`/`Bool`) resta per lo stesso motivo: è usato
solo da un test (`youtube/usecase/service_test.go`) ed è quindi un oracolo, non
dead code.

## Morti ovunque — superficie `pkg/` (1): non rimuovere

- `pkg/textutil/textutil.go: TokenizeWithStopWords` — funzione esportata di un
  package `pkg/`: consumatori esterni non verificabili da questo checkout.
  Stessa regola di `pkg/veloxclient` (le cui 19 voci sono nella classe oracoli/
  superficie e non si toccano).

## Oracoli di test (457)

Irraggiungibili dai tre main di produzione ma chiamati dai test. Per policy
restano: il taglio è production-safe, non coverage-safe. La lista completa è
rigenerabile col comando `deadcode ./cmd/server ./cmd/worker ./cmd/admin` e
sottraendo l'intersezione con `deadcode -test ./...` (righe 1–45 del generato
`/tmp/deadcode-classification.txt` della sessione).

## Cosa NON è stato fatto (deliberatamente)

- I 13 metodi su tipi nominati non sono stati toccati: implementano porte,
  validatori o deserializzatori e richiedono la verifica dei consumatori
  esterni prima di un taglio.
- `SanitizeFilename` resta finché resta la catena irraggiungibile che la chiama.
- `pkg/veloxclient` (19 voci) e la superficie `pkg/` non sono stati toccati.
- Nessun cambiamento ai fixture.
- Verifica del taglio: `go build ./...`, `go vet` sui package toccati e le suite
  mirate sono verdi; la misura `deadcode` è stata rigenerata dopo la rimozione.
