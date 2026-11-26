# P0-03 — `MapOverlay` camera validation: 77/58 → validator nominati

**Data:** 2026-11-26
**Priorità:** P0 (validazione di un contratto che arriva al renderer; errori mappa già osservati nei replay)
**Stato:** **DONE**
**Area:** `internal/capabilities/overlays/model`

## 1. Problema misurato

`gocyclo` sul package:

```
77 model (*MapOverlay).validateCameraMove model_map.go:122
58 model (*MapOverlay).Validate            model_map.go:354
```

`validateCameraMove` contiene, in un unico corpo: presenza della camera, conteggio
LOD, geodesia from/to, zoom (finitezza, range, monotonia, match con i LOD),
tilt, bearing, provenienza/attribuzione per LOD, zoom/width/height, match con
`asset_refs`, duplicati, centro del LOD, copertura del viewport, pin, e
verifica byte-level del PNG (SHA-256 + dimensioni). `Validate` ripete parte
della validazione della base raster e dei pin.

Il rischio non è la lunghezza: è che un errore di contratto venga attribuito al
punto sbagliato e che il ramo di copertura geografica non abbia test diretti.

## 2. Soluzione

Estrarre da `validateCameraMove`:

- `validateCameraMovePresence` — caso `CameraMove == nil`, LOD/asset;
- `validateCameraMoveEndpoints` — from/to, zoom, tilt, bearing;
- `validateCameraMoveLODSet` — 2..8 LOD, match con gli asset;
- `validateMapLODHeader` — source/attribution/asset ID/zoom/raster;
- `validateMapLODOrder` — asset_refs, zoom crescente, duplicati;
- `validateMapLODCoverage` — centro, primo LOD, viewport, pin;
- `validateMapLODAsset` — match `asset_refs`, SHA-256, PNG, dimensioni.

Estrarre da `Validate`:

- `validateMapBaseFields` — provider/sorgente/attribuzione/dimensioni/zoom/centro/motion;
- `validateMapBasemapAsset` — il primo asset;
- `validateMapPins` — limite, ID, label, radius, colore, geodesia, window.

L'ordine dei controlli e i messaggi di errore restano identici: sono contratto
osservabile nei log e nei report di certificazione.

## 3. Test

- `TestValidateCameraMoveEndpoints` tabellare: valido, zoom NaN/Inf, zoom
  decrescente, tilt oltre ±80, bearing oltre ±360.
- `TestValidateCameraMoveLODSet` tabellare: 1 LOD, 9 LOD, mismatch asset.
- La suite esistente `model_map_test.go` resta il contratto per raster, pin,
  attribution e wire.

## 4. Check finali

```bash
gocyclo -over 40 internal/capabilities/overlays/model   # atteso: nessun output
go test ./internal/capabilities/overlays/model/... -count=1
go test ./internal/capabilities/overlays/... -count=1
go build ./... && go vet ./...
go run ./cmd/archcheck --strict
```

## 5. Evidenza

**2026-11-26, HEAD `cd43abdb7`.**

- `gocyclo` sul package: `(*MapOverlay).validateCameraMove` **77 → 4**;
  `(*MapOverlay).Validate` **58 → 6**; l'helper più alto è
  `validateMapBaseFields` a **22**, sotto la soglia 40.
- `go test ./internal/capabilities/overlays/model/... -count=1` →
  `ok github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model 0.855s`
- `go test ... -run 'TestValidateCameraMove' -count=1 -v`: 8 casi endpoint + 6
  casi LOD, tutti PASS.
- `go build ./...` → exit 0; `go vet ./...` → exit 0.
- Il file di validator è `model_map_validation.go`; gli helper PNG/motion già
  presenti sono stati spostati in `model_map_assets.go` per non perdere la
  verifica byte-level durante il taglio.

## 6. Rischi e rollback

- **Rischio:** cambiare l'ordine rende un input invalido un errore diverso.
  Mitigazione: test di congelamento dei messaggi sui casi esistenti; gli helper
  sono chiamati nella sequenza originale.
- **Rischio:** la verifica PNG fa I/O e non deve essere eseguita due volte.
  Mitigazione: `validateMapLODAsset` è l'unico owner della verifica per LOD;
  `Validate` mantiene la verifica del solo basemap.
- **Rollback:** revert del file; nessun tipo wire cambia.
