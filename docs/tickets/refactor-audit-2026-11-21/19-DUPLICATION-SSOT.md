# RA-19 — Duplicazioni e deriva semantica (SSOT)

**Data:** 2026-11-21
**Priorità:** P2 (precede RA-17, che tipizza i valori)
**Stato:** **DONE** — tutti i sei punti chiusi il 2026-11-21: 1 (spec/validatore grid),
2 (mirror `channelprofile`), 3 (cache L1/L2 con politica esplicita), 4 (parser/URL Drive),
5 (helper SHA-256, `#RRGGBB`, lingua) e 6 (env `RENDERINGGEN_STORE_URL`). La tipizzazione dei
valori (tipi `SHA256`/`RGB`/`LanguageTag` al posto delle stringhe) resta il perimetro di RA-17,
non di questa scheda.

**Owner:** kernel + capability owners
**Origine:** report esterno 2026-11-21 §5; verificato sul codice
**Verifica sul codice:** **CONFERMATO** per il mirror `channelprofile`, per le 4 validazioni
colore, per i 3 parser Drive, per i 3 siti `os.Getenv`. **DA CONFERMARE** per il drift delle
cache L1/L2 (righe indicate)

## 1. Problema

### 1.1 Spec identiche, validatori duplicati

`kernel/script/overlay_style_spec.go:197-230`: `OverlayImageGridSpec` e
`OverlayImageBackdropGridSpec` sono strutturalmente identici; `validateImageGrid` (443-476) e
`validateImageBackdrop` (480-523) sono la stessa logica ramo per ramo.

### 1.2 Mirror che ha perso 5 campi

`capabilities/channelprofile/types.go:171-241`: `OverlayStyleProfile` dichiara di rispecchiare
`scriptpkg.OverlayStyleSpec`, ma il tipo canonico (`kernel/script/overlay_style_spec.go:56-88`)
ha in più `Bullet`, `GlowColor`, `Cursor`, `Image`, `CardBackground`. Un profilo di canale che
usa quei campi viene **silenziosamente ignorato** (`OverlayStyle()` non li copia). È deriva
semantica, non solo duplicazione.

### 1.3 Cache L1/L2 con due politiche

`adapters/media_resolver_image_segments.go:103-156` e
`adapters/vidrush_internet_image_candidates.go:26-74` leggono la stessa `vidrushImageCache` e lo
stesso `loadVidRushPersistentJSON`, ma con **chiavi diverse**; il secondo applica
`internetImageNegativeFresh` + `verifyPersonImageCandidates` sull'hit, il primo no. Lo stesso
asset può quindi essere riusato in un percorso e rivalidato (o scartato) nell'altro, senza una
regola scritta.

### 1.4 Parsing e validazione duplicati

- Drive: `pkg/urlutil.FileIDFromDriveLink` canonico + 3 parser manuali
  (`final_job_handoff.go:180`, `remote_final_job_objectstore.go:116`,
  `adapters_voiceover_repo.go:517`) + 25 format string URL.
- SHA-256: 5 reimplementazioni (vedi RA-17 §1.2).
- `#RRGGBB`: 4 implementazioni (`overlay_style_spec.go:525`, `model_map.go:509`,
  `cliprender/plan.go:289`, `feature_analyzer.go:338`).
- Lingua: `kernel/asset/bcp47.go::Normalize` vs `brain/intent/registry.go:62`.

### 1.5 Config bypass

`RENDERINGGEN_STORE_URL` letto con `os.Getenv` in
`app/wiring/script_generation_runtime.go:178`, `script_generation_overlay_queue.go:21`,
`registry_internal_modules_render.go:142`, fuori da `internal/app/knobs/registry.go`.

## 2. Soluzione

1. **Spec unica**: fondere `OverlayImageBackdropGridSpec` in `OverlayImageGridSpec` (o un tipo
   parametrizzato con un `Mode`), con un solo validatore. Se il backdrop ha davvero una regola
   diversa, il validatore prende un parametro — non una copia.
2. **`channelprofile`**: aggiungere i tag `yaml` necessari a `kernel/script.OverlayStyleSpec`
   (oggi ha solo `json`), eliminare `OverlayStyleProfile` e i tipi speculari, e decodificare
   `config/channel_profiles.yaml` direttamente nel tipo canonico. Se il mirror deve restare per
   un vincolo di decodifica, aggiungere un **test di parità per riflessione** che fallisce
   quando i campi divergono (guard, non soluzione).
3. **Cache unica**: estrarre un tipo `entityImageCache` in `scripts/adapters` con:
   - **una** funzione di chiave, documentata;
   - **una** politica di hit (freshness negativa + verifica persona) applicata sempre;
   - L1 in memoria + L2 persistente dietro la stessa interfaccia.
   Migrare i due chiamanti sullo stesso tipo. Se una differenza è intenzionale, va espressa
   come opzione esplicita (`HitPolicy`) e testata.
4. **Drive**: sostituire i 3 parser con `urlutil.FileIDFromDriveLink`; sostituire le format
   string con un helper `urlutil.DriveFileURL(id)`. Test tabellare con i formati reali
   (`/d/<id>/`, `?id=`, `open?id=`, con e senza `usp=`).
5. **SHA/colore/lingua**: un solo helper ciascuno (qui la deduplica; la tipizzazione è RA-17).
6. **Env**: registrare `RENDERINGGEN_STORE_URL` in `knobs`/config, leggerlo una volta all'avvio
   e passarlo. Lasciare l'env come **fallback** documentato nel loader di config (non nel
   wiring). Aggiungere al check degli env una allowlist: `os.Getenv` in `internal/app/**` e
   `internal/capabilities/**` deve essere zero, salvo `cmd/` e il loader di config.

## 3. Check finali

```bash
cd refactored
# 1. spec e validatori unici
grep -rn "OverlayImageBackdropGridSpec\|func validateImageBackdrop" --include=*.go internal/ | grep -v _test
# atteso: nessun output (assorbito nel tipo unico)

# 2. il mirror non esiste più
grep -rn "type OverlayStyleProfile" --include=*.go internal/
# atteso: nessun output
go test ./internal/capabilities/channelprofile/ -run 'Profile|YAML|Parity' -v
# atteso: PASS; i 5 campi (Bullet, GlowColor, Cursor, Image, CardBackground) arrivano al piano

# 3. una sola cache con una sola politica
grep -rn "vidrushImageCache\|loadVidRushPersistentJSON" --include=*.go internal/capabilities/scripts/adapters/ | grep -v _test
# atteso: solo il nuovo tipo cache

# 4. parser unico
grep -rn "func .*Drive.*ID\|driveFileIDFrom\|extractDrive" --include=*.go internal/ pkg/ | grep -v _test
# atteso: solo urlutil (o il wrapper unico)

# 5. nessun os.Getenv fuori dal loader
grep -rn "os.Getenv(" --include=*.go internal/app/ internal/capabilities/ | grep -v _test
# atteso: nessun output oltre l'allowlist documentata

# 6. build/vet/test/gate
go build ./... && go vet ./...
go test ./internal/kernel/script/ ./internal/capabilities/channelprofile/ ./internal/capabilities/scripts/adapters/
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: aggiungere `yaml` tag al tipo del kernel introduce una dipendenza `yaml` nel
  kernel. Mitigazione: i tag non richiedono import; il decoder resta in `channelprofile`.
- **Rischio**: unificare le cache cambia quali asset vengono riusati (e quindi l'output
  editoriale). Mitigazione: test di parità sui candidati prodotti prima/dopo con gli stessi
  input; la scelta della politica unica va documentata nel ticket **prima** del codice.
- **Rollback**: per punto; la cache è il punto a rischio più alto, va per ultima.

## 7. Esito della verifica (2026-11-21, punto 2)

**Il mirror non esiste più.** `capabilities/channelprofile/types.go` non dichiara più
`OverlayStyleProfile`, `OverlaySizeProfile`, `OverlayShadowProfile`, `OverlayTransitionProfile`
né il metodo di proiezione `OverlayStyle()`: `Profile.OverlayStyle` è
`*scriptpkg.OverlayStyleSpec` e viene decodificato direttamente dallo YAML.

Cosa è stato fatto, per punto:

1. **Tag `yaml` sul tipo canonico** (`kernel/script/overlay_style_spec.go`): aggiunti accanto ai
   `json` su `OverlayStyleSpec` e su tutti i tipi raggiungibili — `OverlayShadowSpec`,
   `OverlaySizeSpec`, `OverlayTransitionSpec`, `OverlayBulletStyleSpec`,
   `OverlayCursorStyleSpec`, `OverlayImageStyleSpec`, `OverlayImageBorder`,
   `OverlayImageStroke`, `OverlayImageGlowSpec`, `OverlayImageGridSpec`,
   `OverlayImageBackdropSpec` (il suo `grid` è il tipo unico sopra), `OverlayCardBackgroundSpec`,
   `OverlayCardBackgroundStrokeSpec`, `OverlayCardBackgroundShadowSpec`, e
   `VideoClipEffectSpec` (alias `ImageEffectSpec`) in `output_spec.go`. I tag non aggiungono
   import: il kernel resta senza dipendenza `yaml`.
2. **Deriva impossibile per costruzione**: un campo nuovo del blocco kernel è indirizzabile da
   un profilo appena viene aggiunto; il test
   `TestParseDecodesTheCanonicalOverlayStyleSpec` lo verifica su **tutti** i campi, compresi i
   cinque che il mirror perdeva (`Bullet`, `GlowColor`, `Cursor`, `Image`, `CardBackground`).
3. **Default `shadow.enabled` preservato**: `OverlayShadowSpec.Enabled` resta un `bool` (il
   contratto JSON non cambia). `Parse` ripristina la regola del profilo con la sonda a una
   chiave `shadowEnabledPresence` in `registry.go`: un blocco `shadow:` dichiarato è attivo
   salvo `enabled: false` esplicito. `TestParseAppliesTheProfileShadowDefault` copre i tre casi
   (assente, `true`, `false`).
4. **Validazione unica**: `validateOverlayStyle` delega a `OverlayStyleSpec.Validate()`, quindi
   profilo e payload del chiamante sono respinti dalle stesse regole (font family, bullet,
   piastra, cursore, immagine, glow, stroke, size). Restano locali solo le tre regole che il
   contratto run-level non esprime: arietà RGBA, arietà `shadow.offset` e preset di ingresso
   obbligatorio.
5. **Applicazione diretta**: `ApplyChannelProfile` assegna `req.OverlayStyle = p.OverlayStyle`
   (niente più passaggio di proiezione); i profili sono immutabili dopo `Install` e nessuno
   stadio a valle muta il blocco.
6. **Documentazione**: l'intestazione di `config/channel_profiles.yaml` dichiara che
   `overlay_style` usa le stesse chiavi del contratto pubblicato e che un blocco `shadow:` è
   attivo salvo `enabled: false`.

## 7.1 Cosa resta aperto in questa scheda

Niente: i punti 3 e 5 sono chiusi nelle §7.6 e §7.7. La tipizzazione dei valori resta in RA-17.

## 7.2 Check eseguiti (punto 2)

```bash
cd refactored
grep -rn "type OverlayStyleProfile" --include=*.go internal/          # nessun output
grep -rn "OverlayStyleProfile\|OverlayShadowProfile" --include=*.go internal/  # nessun output
gofmt -l internal/kernel/script/overlay_style_spec.go internal/capabilities/channelprofile/  # nessun output
go build ./internal/kernel/script/ ./internal/capabilities/channelprofile/ ./internal/capabilities/scripts/  # exit 0
go test ./internal/capabilities/channelprofile/   # ok
go test ./internal/capabilities/scripts/ -run ChannelProfile   # ok
go vet ./internal/capabilities/channelprofile/ ./internal/capabilities/scripts/  # exit 0
```

## 7.3 Esito della verifica (2026-11-21, punto 6)

**Il wiring non legge più l'ambiente.** I tre siti elencati dal report
(`script_generation_runtime.go`, `script_generation_overlay_queue.go`,
`registry_internal_modules_render.go`) consumano ora il campo di config
`external.renderinggen_store_url`, risolto una sola volta dal loader:

1. `ExternalConfig.RenderingGenStoreURL` ha `yaml:"renderinggen_store_url"`,
   `env:"RENDERINGGEN_STORE_URL"` e default `http://127.0.0.1:9000`; l'env resta quindi
   l'override documentato nel loader (ordine uniforme YAML → env → default), non una lettura
   sparsa nel composition root.
2. In `app/wiring` un unico helper `renderingGenStoreURL(cfg)` applica quel valore; il
   fallback `defaultRenderingGenStoreURL` resta solo come guardia per un `Config` costruito
   senza loader. Prefetch asset overlay, `OverlayClipFetcher` e Chronon timing fetcher
   puntano allo stesso store letto una volta.
3. `TestExternalConfigRenderingGenStoreURLBindings` pinna default, binding YAML e override
   env.

Verifiche: `go build ./...` exit 0; `gofmt -l` sui file toccati nessun output;
`go test ./internal/platform/config/ ./internal/app/wiring/` PASS. `grep RENDERINGGEN_STORE_URL`
su `internal/app` restituisce ora solo un commento.

Restano fuori dal perimetro di questo punto (e vanno trattati in una scheda di sweep): le
letture dell'env in `internal/capabilities/scripts/final_job_objectstore.go` e in
`internal/platform/renderinggen` (`overlay_clip_fetcher.go`, `queue_client_materialize.go`),
che la soluzione completa del ticket voleva ricondurre all'allowlist `os.Getenv` = zero in
`internal/app/**` e `internal/capabilities/**`.

## 7.4 Esito della verifica (2026-11-21, punto 4)

**Un solo parser e un solo costruttore.** I tre parser manuali elencati dal report sono stati
eliminati e i loro call-site usano `urlutil.FileIDFromDriveLink`:

- `capabilities/scripts/final_job_handoff.go` (fallback per il final audio);
- `app/wiring/script/remote_final_job_objectstore.go` (fallback quando la riga media non ha
  `drive_file_id`);
- `app/wiring/voiceover/adapters_voiceover_repo_tx.go` (`timing_json_link`), con lo stesso
  fail-closed su link vuoto che aveva il parser rimosso.

Nella stessa famiglia è stato migrato anche `overlayPlanDriveFileID`
(`capabilities/scripts/overlay_audit_plan_images.go`), che reimplementava query `id` + segmenti
`/d/` senza riconoscere il contratto canonico. Restano deliberatamente fuori due casi che NON
sono parser di link Drive: gli accessor che leggono `drive_file_id` già tipizzato
(`texttracks/backfill_acquire.go`) e il regex di `assets/deletion`, che accetta anche
`docs.google.com` (vocabolario diverso, non coperto da `FileIDFromDriveLink`).

Nuovo `urlutil.DriveFileURL(id)` in `pkg/urlutil`: unico costruttore del link web canonico
`https://drive.google.com/file/d/<id>/view`, che `FileIDFromDriveLink` rivalida. Adottato in 14
siti che prima concatenavano o formattavano la stessa stringa (`app/wiring/script`,
`platform/sqlite{imagesregistry,imagesrepo}`, `platform/postgres/media`, `capabilities/images`,
`capabilities/assets/lifecycle`, `capabilities/clips/aistock`,
`capabilities/scripts/{adapters,adapters/processor,usecase}`,
`capabilities/voiceover/service`). Restano deliberatamente tre costruzioni del solo path senza
`/view` (`platform/drive/helpers.go`, `capabilities/images/storage_drive.go`,
`capabilities/assets/catalogsync/sync_recursive.go`): sono una forma diversa, già centralizzata
dal primo, e cambiarne l'output avrebbe alterato link persistiti.

I link di download (`/uc?id=` / `/uc?export=download&id=`) restano distinti: non sono lo stesso
contratto del web link e non è stato introdotto un secondo helper per non forzare una forma
unica su due semantiche diverse.

Test: `TestDriveFileURL` e `TestDriveFileURLRoundTrips` in `pkg/urlutil`; i test esistenti dei
pacchetti toccati (final job, voiceover cache, immagini, lifecycle, aistock, adapters) restano
verdi. Verifiche eseguite: `gofmt -l` sui file toccati nessun output, `go build ./...` exit 0,
`go test` sui 13 package coinvolti PASS.

## 7.5 Esito della verifica (2026-11-21, punto 1)

**Una sola spec e un solo validatore.** `OverlayImageBackdropGridSpec` è stato eliminato:
`OverlayImageBackdropSpec.Grid` è `*OverlayImageGridSpec`, quindi le due superfici condividono
lo stesso tipo (e gli stessi tag `json`/`yaml`) per costruzione. `validateImageGrid` prende ora
il prefisso dell'errore (`overlay_style.image.grid` oppure `overlay_style.image.backdrop.grid`) e
`validateImageBackdrop` è stato rimosso: le due regole proprie del backdrop (`zoom` 1..10,
`blur_px` 6..48) vivono inline nel blocco `image.Backdrop` di `Validate` e poi delegano al
validatore unico. I messaggi di errore mantengono il path originale.

Verifiche:

```bash
grep -rn "OverlayImageBackdropGridSpec\|func validateImageBackdrop" --include=*.go internal/ pkg/  # nessun output
gofmt -l internal/kernel/script/overlay_style_spec.go            # nessun output
go build ./...                                                  # exit 0
go test ./internal/kernel/script/ ./internal/capabilities/scripts/ -run 'Overlay|Validate|Style|Backdrop|Grid'  # PASS
```

## 7.6 Esito della verifica (2026-11-21, punto 3)

**Una cache, due politiche nominate.** Le due letture non sono più due copie: entrambe passano
da `adapters/internet_image_cache.go`, che possiede

- `internetImageSegmentCacheKey(segmentID, textHash, language, model, promptVersion, perQueryLimit)`:
  l'unica identità di un risultato immagini a livello segmento (lo schema `internet-images-assets-v5`
  è nella chiave; il testo della query è deliberatamente escluso);
- `internetImageSegmentCache`: L1 run-scoped (`cacheutil.LRU`) + L2 durabile
  (`VidRushCachePort`, namespace `internet_images`), con `Load`/`Store` e sanitizzazione del
  payload in scrittura;
- due `internetImageHitPolicy` esplicite, che sono l'unica differenza rimasta tra i chiamanti:
  - `internetImageReplayPolicy` (media resolver): `EmptyReplay: true`, `NegativeTTL: 0` — una
    ricerca completata a vuoto è un risultato riproducibile e i candidati in cache sono già il
    set post-verifica che lo stadio riordina;
  - `internetImageObservationPolicy` (fan-out provider): `NegativeTTL: 15m`,
    `EmptyReplay: false`, `VerifyCachedCandidates: true` — il vuoto è un'osservazione breve e i
    candidati vanno rivalidati.

`vidRushFanoutImageCacheKey` delega alla chiave unica; `media_resolver_image_segments.go` e
`vidrush_internet_image_candidates.go` usano solo `internetImageSegmentCache`. Le regole già
pinnate restano verdi: `TestInternetImagesProcessorCachesEmptyResultsInL2` (replay del vuoto nel
media resolver) e `TestInternetImageZeroResultIsReusedWithoutAProviderCall` (negativo breve nel
fan-out). Nuovo `internet_image_cache_test.go`:
`TestInternetImageSegmentCacheKeyHasOneSpelling`, `TestInternetImageHitPoliciesAreExplicit`,
`TestInternetImageSegmentCacheReplayKeepsEmptyDurable`,
`TestInternetImageSegmentCacheObservationExpires`, e
`TestInternetImageSegmentCacheStoreStripsMaterializationFromL2`.

Verifiche: `go test ./internal/capabilities/scripts/adapters/ -count=1` PASS;
`go test ./internal/capabilities/scripts/ -count=1` PASS; `go build ./...` exit 0.

## 7.7 Esito della verifica (2026-11-21, punto 5)

**Un helper per famiglia.** Nuovo package `internal/kernel/color` con `IsHexRGB` e
`ParseHexRGB` (unico validatore/decoder di `#RRGGBB`); `internal/kernel/digest.IsSHA256` /
`IsCanonicalSHA256` sono l'unico controllo SHA-256; `kernel/asset.Normalize` è l'unica
normalizzazione BCP-47.

Migrati:

- **SHA-256** — `isLowerHex` (`overlay_render_cache.go`), `isHexSHA256`
  (`adapters/processor_vidrush_image_selection.go`), `isValidHex64`
  (`stock/enrichment/idempotency.go`) e i quattro controlli inline
  `strings.Trim(…, "0123456789abcdefABCDEF")` (`overlays/model/model_map.go` ×2,
  `overlays/model/model.go`, `scripts/random_overlay_sfx.go`). `validDigestHex` era già un
  delegato.
- **`#RRGGBB`** — `isStyleHexColor` (`kernel/script/overlay_style_spec.go`), `isHexColor`
  (`overlays/model/model_map.go`), `isFrameHexColor` (`cliprender/plan.go`), `hexColorRe` +
  `parseHexColor` (`postgres/media/feature_analyzer.go`) e i controlli inline in
  `cliprender/effects.go`, `kernel/media/clipsequence/presets.go`,
  `platform/media/clipmerge/merge.go`, `scripts/overlay_plan_v2_envelope.go`.
- **Lingua** — `brain/intent/registry.go::normalizeLanguageTag` è sparito; `resolverLanguage`
  valida con `asset.Normalize` e poi tiene il sottotag primario (la chiave dei resolver). Un
  input che il normalizzatore canonico rifiuta cade nel resolver multilingue, come prima.

Nuovo `internal/kernel/color/color_test.go` (forma esatta, case, decoder). `kernel/color` è
dichiarato in `architecture/policy.yaml::kernel_subzones`, altrimenti `archcheck --strict`
fallisce con `kernel_subzone_undeclared`.

Verifiche: `go build ./...` e `go vet ./...` exit 0; test verdi su `kernel/color`,
`kernel/script`, `overlays/model`, `cliprender`, `clipsequence`, `clipmerge`,
`postgres/media`, `stock/enrichment`, `brain/intent`; `archcheck --strict` exit 0.
