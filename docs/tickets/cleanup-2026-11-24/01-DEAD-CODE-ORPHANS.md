# CLEANUP-01 — Funzioni morte rimaste dopo gli split

**Data:** 2026-11-24
**Priorità:** P1 (igiene, rischio zero)
**Stato:** DONE
**Owner:** `capabilities/scripts`, `capabilities/jobs`, `stockpipeline/publish`
**Origine:** radiografia 2026-11-24, §2A
**Base:** `refactored` @ `2a3ea0b2c` (working tree pulito)

## 1. Problema

Gli split del 2026-11-23 hanno lasciato in produzione funzioni senza alcun
chiamante, né in produzione né nei test. Non sono debito architetturale: sono
residui che allungano la superficie del package e rendono il conteggio deadcode
meno leggibile.

Verifica eseguita con `rg -w '<nome>'` su tutto `refactored`: per ogni simbolo
sotto esiste **solo la definizione**, nessun riferimento.

| File | Simbolo | Nota |
| --- | --- | --- |
| `internal/capabilities/assets/providers/stock/stockpipeline/publish/step_publish.go:215` | `slugifyTitle` | wrapper morto di `SlugifyTitle` |
| `internal/capabilities/jobs/extracted_compat.go:221` | `estimatePayloadSeconds` | wrapper morto di `handlers.EstimatePayloadSeconds` |
| `internal/capabilities/scripts/public_aliases_a.go:250` | `checkedFloatSeconds` | wrapper morto di `contract.CheckedFloatSeconds` |
| `internal/capabilities/scripts/public_aliases_a.go:258` | `segmentExtractionExplicitlyEnabled` | wrapper morto di `contract.SegmentExtractionExplicitlyEnabled` |
| `internal/capabilities/scripts/translated_nlp.go:548` | `limitTranslatedNLPStrings` | wrapper morto di `semantics.LimitStrings` |
| `internal/capabilities/scripts/runner_scene_text_coordination.go:133` | `fixedPlaybackDurationUS` | duplicato morto: la copia viva è `scripts/workunits/fixed_playback.go:8`, usata da `workunits/scene_bindings.go:123` |
| `internal/capabilities/scripts/entity_image_motions.go:15` | `entityImageCaption` | nessun chiamante |
| `internal/capabilities/scripts/finaljob/overlay_publication.go:211` | `finalJobOverlayIsImageStyle` | nessun chiamante |
| `internal/capabilities/scripts/overlayproject/numeric_grounding.go:187` | `numericEntityGroundedForLanguage` | nessun chiamante |
| `internal/capabilities/scripts/overlayproject/numeric_grounding.go:216` | `numericValues` | orfano: usato solo dalla funzione precedente |

### Correzioni rispetto al report

- Il report colloca `checkedFloatSeconds` e
  `segmentExtractionExplicitlyEnabled` in `contract_aliases.go`: sono in
  `public_aliases_a.go`. Il numero di riga del report è quindi inaffidabile;
  vale il grep.
- `numericValues` non era elencato nel §2A. Lo diventa dopo la rimozione di
  `numericEntityGroundedForLanguage`: va rimosso nello stesso commit, altrimenti
  resta un secondo orfano.
- La copia viva di `fixedPlaybackDurationUS` è in `scripts/workunits`, non nel
  root `scripts`: si elimina solo quella del root.

## 2. Soluzione

Rimuovere le dieci funzioni e gli import che restano senza uso:

1. cancellare i corpi elencati in §1;
2. rimuovere gli import diventati inutilizzati (`scriptpkg` in
   `public_aliases_a.go` e `runner_scene_text_coordination.go`, `semantics` in
   `translated_nlp.go`, `capmodel` in `entity_image_motions.go` e
   `overlayproject/numeric_grounding.go`);
3. non toccare `scripts/workunits/fixed_playback.go` né i suoi chiamanti.

Nessun cambio di comportamento: non esiste un percorso di produzione o di test
che raggiunga questi simboli.

## 3. Check finali

```bash
cd refactored
rg -w 'slugifyTitle|estimatePayloadSeconds|checkedFloatSeconds|segmentExtractionExplicitlyEnabled|limitTranslatedNLPStrings|fixedPlaybackDurationUS|entityImageCaption|finalJobOverlayIsImageStyle|numericEntityGroundedForLanguage|numericValues' \
  --glob '*.go' --glob '!docs/**'
# atteso: solo la copia viva in scripts/workunits/fixed_playback.go
go build ./...
go test ./internal/capabilities/scripts/... ./internal/capabilities/jobs/... \
  ./internal/capabilities/assets/providers/stock/stockpipeline/... -count=1
gofmt -l <file toccati>   # nessun output
go run ./cmd/archcheck --strict
```

## 4. Esito (2026-11-24)

- 10 funzioni rimosse, incluse le due alias in `public_aliases_a.go` e
  `numericValues`, che resta orfano dopo la rimozione del suo unico chiamante.
- La copia viva di `fixedPlaybackDurationUS` in `scripts/workunits` è intatta.
- `go build ./internal/capabilities/scripts/` → rc 0.
- `gofmt -l` sui file toccati → nessun output; `git diff --check` → rc 0.
- `archcheck --strict` → rc 0, `has_hard_gate_hits: false`.
- Riverifica finale: rimosse anche le tre copie residue
  (`checkedFloatSeconds`, `segmentExtractionExplicitlyEnabled`,
  `entityImageCaption`); `rg -w` non trova più definizioni morte.
- `go build ./...` rc 0; `go test ./internal/capabilities/scripts -count=1`
  PASS (20,4 s); `jobs`, `stockpipeline` e `clips` PASS.
- `go run ./cmd/archcheck --strict` rc 0, `has_hard_gate_hits: false`.
- Nessun commit: il working tree condiviso contiene 20+ rename `RM` di un'altra
  sessione; un commit selettivo includerebbe o spezzerebbe quel lavoro.

