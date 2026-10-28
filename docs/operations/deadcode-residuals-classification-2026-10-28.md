# Dead code residui — classificazione per consumatori e contratti (2026-10-28)

Risponde a «Serve: classificare i residui per consumatori e contratti, non
eliminare in massa il risultato di `deadcode`» (`docs/stato/refactored.md` §5,
workspace root). **Questa è solo una classificazione: nessuna funzione è stata
rimossa.**

## Misure (riproducibili)

```bash
go install golang.org/x/tools/cmd/deadcode@latest
cd refactored
deadcode ./cmd/server ./cmd/worker ./cmd/admin      # 482 righe (produzione)
deadcode -test ./...                                # 141 righe (produzione+test)
```

Le due misure si compongono così (482 voci prod uniche per coppia file/funzione):

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

## Morti ovunque — funzioni ordinarie (11): pool di futura rimozione

| File | Funzione |
|---|---|
| `internal/capabilities/overlays/preset_image_selection.go` | `EntitySequenceCameraMotion` |
| `internal/capabilities/translation/types.go` | `DefaultPromoLanguages` |
| `internal/capabilities/voiceover/service/filename.go` | `SanitizeFilename` |
| `internal/capabilities/voiceover/service/service.go` | `NewService` |
| `internal/capabilities/voiceover/service/types_helpers.go` | `buildRequestID` |
| `internal/capabilities/voiceover/service/types_helpers.go` | `normalizeBatchRequestWithConfig` |
| `internal/kernel/media/assembly_contract.go` | `StreamSignatureFromContract` |
| `internal/kernel/media/assembly_contract.go` | `StreamSignatureFromProbe` |
| `internal/platform/ptrutil/ptrutil.go` | `Bool` |
| `internal/platform/ptrutil/ptrutil.go` | `Ptr` |
| `internal/platform/sqlite/assets/imagesregistry/media_asset_mutations.go` | `UpdateMediaAssetContentHashesTx` |

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

- Nessuna rimozione: la classificazione è il deliverable richiesto; il pool di
  rimozione andrà rieseguito con build+vet+test dopo ogni futuro taglio.
- `pkg/veloxclient` (19 voci) non è stato toccato.
- Nessun cambiamento ai file di test o ai fixture.
