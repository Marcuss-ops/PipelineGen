# TICKET — chain_segments single source of truth (2026-11-18)

- **Status:** PARTIAL (agg. 2026-11-22). The identity decision now runs
  once: the planner promotes entity cards from
  `media.promotable_entity_assets` with the ported seeded sampler, the Go
  promotion pass is deleted, **Open 3 è chiuso** (il ceiling è campo della
  richiesta), **Open 4 è chiuso** (l'inferenza del consumatore è ritirata),
  **Open 5 è chiuso** (fixture generata dal planner) e **Open 6 è chiuso**
  (modello tipizzato unico; `DecodeChainSegments`/`ChainSegmentMaps` rimossi).
  L'accettazione visiva è stata eseguita il 2026-11-21 (`r12`/`r4`, 21/21
  gate). Resta solo l'**A/B col pianificatore pre-v2** («identici a prima»);
  i goldens e le suite sono verdi.
- **Severity:** P1 — a post-seal pass rewrites chain segment identity, which the
  ownership doc forbade, and nothing failed when it did.
- **Owner:** overlay/editorial pipeline + media plane
- **Source:** analysis 2026-11-18 on `refactored@8b41b5bf1`,
  `BrainRust/overlay-compiler`, `RenderingGen@1c799cfd6`
- **Related:** ADR-029 (authority boundary), `OVERLAY-EDITORIAL-OWNERSHIP.md`,
  `entity-overlay-composition.md`, BrainRust TICKET-14

## Problem

`params.chain_segments` is produced by BrainRust and consumed twice in Go, but
until 2026-11-18 the "single contract" existed only as prose plus two copies of
a fixture and two SHA constants.

1. **Entity promotion rewrites segment identity after the seal.**
   `promoteChainEntityCardSegments` (`scripts/overlay_plan_seal.go:120`) sets
   `seg["kind"]`/`seg["template_id"]` and **deletes** `text`, `entity_style_id`,
   `motion_id`, `motion_params`, `params` (`:154-158`), then flips the item to
   `image_chain` (`:176-177`). That is identity and motion re-selection after
   Rust ranked the plan, while the ownership doc said `Kind`/`TemplateID`/
   `Text`/`MotionID` are read-only post-seal.
2. **A run ceiling drops items after the seal.** `capEntityImageOverlays`
   (`overlay_entity_cards.go:377`) removes `entity_image` items past
   `MaxEntityImageOverlaysPerRun`, so the rendered plan is not the ranked plan.
3. **The invariant test could not see any of it.** `snapshotEditorial`
   snapshotted item-level fields only and the test ran map materialization
   alone, never promotion and never a segment.
4. **Three wire definitions, one untyped.** The Rust producer built segments as
   `serde_json::Map` with `insert_non_empty`, so a key rename could not be
   caught by the Rust compiler; the Go producer used `omitempty` throughout and
   the RenderingGen mirror used none.
5. **The alignment gate skipped itself.** `TestChainSegmentFieldsMatchRenderingGenMirror`
   read the sibling file, compared one direction only and `t.Skip`ped when the
   checkout was absent. The Rust producer was not covered at all.
6. **The fixture was not producer output.** It carried five `"text": ""` keys
   that `insert_non_empty` can never emit and was missing the always-emitted
   `chain_gap_us`, so a decode/encode round trip silently changed the document.
7. **The two sources of truth meet here.** Whether an entity has a certified
   portrait lives in PostgreSQL (`entity_image_catalog`); whether the card
   becomes an image is an editorial decision. Rust ranks blind, Go decides
   afterwards, and the decision was undocumented.

## What landed (2026-11-18)

- **Typed producer.** `ChainSegment` in
  `BrainRust/overlay-compiler/src/editorial_plan_v2/planner/coalesce.rs` replaces
  the untyped map. `is_blank` reproduces the old `insert_non_empty` semantics, so
  the wire bytes are unchanged (all 30 golden plan-contract tests stay green).
- **Three independent key-set pins** for the same 27 keys: Rust
  `segment_contract_tests::CANONICAL_KEYS`, `capmodel`'s
  `chain_segments_contract_test.go`, RenderingGen's
  `internal/overlay/chain_segments_contract_test.go`. None of them depends on a
  sibling checkout.
- **`omitempty` pinned** on the Go producer: a Go re-emit cannot keep an empty
  string the producer would have dropped, which would otherwise change the plan
  fingerprint of an identical chain.
- **Fixture corrected** to producer shape (blank `text` removed,
  `chain_gap_us` added, both repositories byte-identical at
  `7647670ee04550892cc9c8939bba6b71d354698ccdb2bdbdd8a4ba8d25dde9d0`) with a
  strict decode/encode round-trip assertion.
- **Post-seal segment gate.** `snapshotEditorial` now snapshots every segment's
  `Kind`/`TemplateID`/`Text`/`EntityID`/`MotionID`/`GroupID`/`StartUS`/
  `DurationUS`, and two tests pin the declared surface:
  `TestChainSegmentPromotionTouchesOnlyTheDeclaredSurface` (added keys, dropped
  keys, preserved timing/entity/order) and
  `TestChainSegmentPresetResolutionTouchesOnlyPresetID`.
- **Matrix corrected.** `OVERLAY-EDITORIAL-OWNERSHIP.md` now declares entity-image
  promotion as ADR-029 exception 2 and the entity-image ceiling as a declared
  drop, each with its pinning test.
- **Mirror comparison is bidirectional** and fails instead of passing when the
  `chainSegment` anchor disappears.
- **Consumer identity is one declared shim.** RenderingGen's `segmentIdentity`
  (`internal/overlay/chain_compile.go`) replaced the inline inference: the
  mapping is pinned case by case (`TestSegmentIdentityDeclaredMapping`), a
  segment carrying a map *and* image bytes or carrying no content fails the
  compile (`TestSegmentIdentityFailsClosed`,
  `TestCompileChainRejectsContentlessSegment`), and a legacy segment without
  `kind` still lowers (`TestCompileChainInfersMissingKind`).
- **The Rust boundary is pinned, and the donor path is verified.** A portrait
  attached by the planner can no longer promote the card or pick its preset
  (`identity_ownership_tests`), and an asset without URL + 64-char SHA is no
  longer copied onto a card. The 30 golden plan fixtures — the byte-level
  contract for the whole planner — are unchanged.
- **Determinism and the ceiling are gated.** `TestPromotionIsIdempotent`,
  `TestPromotionFingerprintIsDeterministic` and
  `TestEntityImageCeilingDropsOnlyEntityImages` pin the ticket's remaining
  reachable acceptance criterion: the same request yields the same fingerprint,
  a replayed promotion is a no-op, and the ceiling drops entity images only.

## Open

1. ~~**Where promotion lives — blocked on the preset sampler, not on the catalog.**~~
   **DECISO 2026-11-21: (a).** Vedi §Decisione 2026-11-21 qui sotto. Il testo
   storico resta per tracciabilità, ma la premessa «the missing piece is *not*
   the catalog» è **falsa** ed è corretta nella decisione.

   Testo originale: the planner already receives the certified portraits
   (`media.verified_entity_assets`, built by `overlayPlanMedia`) and already
   attaches donors to cards (`media.rs::attach_resolved_entity_images`), so the
   missing piece is *not* the catalog. It is the **preset**: a promoted image
   gets `PresetID = SelectEntityImagePreset(planID, sceneID, itemID)`, which is
   `DefaultDeterministicPresetSampler.Sample` seeded on
   job+scene+item with family `entity_image` over `imagePresetCandidates`
   (`preset.image`, the **Chronon-owned** `VisualPresetRegistry` vocabulary).
   Moving the decision into the planner therefore means either
   - (a) shipping that vocabulary in the request and porting the seeded sampler
     into Rust — a second implementation of the selection math in another
     language, which `deterministic_preset_sampler.go` explicitly rules out
     ("the sampler never hardcodes a preset id … so the Go side can never drift
     from Chronon's catalog"); or
   - (b) accepting a different entrance rotation for every entity image.

   Both are product decisions, so the split is now pinned instead of migrated:
   `media.rs::identity_ownership_tests` fails if the planner ever writes `kind`
   or `preset_id` while attaching a portrait, and the same test asserts the
   donor path only accepts an asset with a renderable URL and a 64-char content
   hash (the rule `append_verified_intent_images` already enforced).
2. ~~**Delete the second decision, not just gate it.** After (1), remove
   `promoteChainEntityCardSegments`' identity rewrite and keep asset attachment.~~
   **ESEGUITO 2026-11-21:** `promoteVerifiedEntityCards`,
   `promoteChainEntityCardSegments` e il ceiling Go sono stati **cancellati**, non
   solo saltati. Vedi §Finestra v2 eseguita.
3. **Move the ceiling pre-Rust** (`MaxEntityImageOverlaysPerRun` as a request
   ceiling) so no plan loses items after the seal.
4. ~~**Remove the consumer's inference entirely.**~~ **ESEGUITO 2026-11-21.**
   `segmentIdentity` (`RenderingGen/renderinggen/internal/overlay/chain_segments.go`)
   non inferisce più nulla: richiede `kind` **e** `template_id` e fallisce con
   `must declare kind and template_id (chain_segments producer contract)`.
   Resta solo il controllo di contenuto vuoto. Il mapping test è cancellato,
   `TestCompileChainInfersMissingKind` è diventato
   `TestCompileChainRejectsMissingKind`. Evidenza di sicurezza: **227 file di
   piano, 3.094 segmenti** in `data/`, `refactored/data/`, `ops/`,
   `deliverables/` — **0** senza le due chiavi.
5. ~~**Generate the fixture or drop the SHA pin.** The fixture is authored, so it
   proves lowering, never producer parity. Either a Rust test emits these bytes or
   the two SHA constants are replaced by a generated artifact.~~
   **CHIUSO 2026-11-21.** Vedi §Open 5 eseguito.
6. ~~**Retire the local decoders.** `chainSegmentMaps`, `chainSegments`,
   `overlayPlanChainSegments` and `chainSegmentStyleMaps` are already gone; the
   remaining `DecodeChainSegments` / `ChainSegmentMaps` pair must survive only
   until (1) makes the plan cross the boundary already validated.~~
   **CHIUSO 2026-11-22.** `OverlayItem` porta `ChainSegments []ChainSegment`,
   idratato da `UnmarshalJSON`, con `Segments()`/`SetChainSegments()` come unica
   superficie; `DecodeChainSegments`, `ChainSegmentMaps` e
   `UpdateChainSegmentMaps` sono rimossi. I consumer di produzione leggono il
   tipo e non decodificano più mappe. `Params["chain_segments"]` resta lo slot
   wire/fingerprint e il setter lo riscrive come mappe per non cambiare la cache
   identity. Evidenza: `go test ./internal/capabilities/overlays/...
   ./internal/capabilities/scripts/... -count=1` e `go vet` sui due alberi,
   verdi il 2026-11-22; `git diff --check` pulito.

## Acceptance criteria for closing

- ~~`promoteChainEntityCardSegments` no longer mutates `kind`, `template_id`,
  `motion_id`, `text`; `TestChainSegmentPromotionTouchesOnlyTheDeclaredSurface`
  is reduced to "no key changes at all".~~ **MET** — the function is gone;
  `TestSealBoundaryLeavesEveryItemAndChainSegmentUntouched` pins that the seal
  boundary changes no item and no segment (only `render_key` is derived).
- No item or segment disappears between the Rust return and the renderer.
- ~~A plan compiled twice from the same request yields the same fingerprint.~~
  **MET** — the planner is deterministic (`compilation_is_deterministic` over the
  fixture matrix, including the promoted golden) and no Go pass mutates the plan
  after the seal (`TestSealBoundaryLeavesEveryItemAndChainSegmentUntouched`).
- `make verify-main` green with the three key-set gates in it. **BLOCKED** by a
  local, untracked cache entry (`data/final-job-fragments/66/*.mp4` carrying a
  pre-canonical avcC); no code in this ticket is involved.

## Prossimi passi (2026-11-19)

Il contratto è pinnato; quello che manca è la decisione di prodotto sul
sampler dei preset (§Open 1) e, a valle, la rimozione della seconda decisione.
L'ordine eseguibile è questo:

1. **Scegliere tra (a) e (b) del §Open 1** e scriverlo qui con data. Senza
   questa scelta i punti 2–6 non si possono toccare: sono tutti conseguenze.
2. **Se (a):** spedire il vocabolario `preset.image` nella richiesta e portare
   il sampler seedato in Rust; gate: `identity_ownership_tests` continua a
   passare e i 30 golden restano byte-identici.
3. **Se (b):** accettare la rotazione diversa e dichiararla; gate: nessuna
   modifica ai golden, ma la scelta va scritta in `OVERLAY-EDITORIAL-OWNERSHIP.md`.
4. **Rimuovere la riscrittura** in `promoteChainEntityCardSegments` (punto 2 del
   §Open) e ridurre `TestChainSegmentPromotionTouchesOnlyTheDeclaredSurface` a
   «nessuna chiave cambia».
5. **Spostare il ceiling pre-Rust** (punto 3): `MaxEntityImageOverlaysPerRun`
   nella richiesta, così nessun item sparisce dopo il seal.
6. **Ritirare `segmentIdentity`** (punto 4) quando il producer emette sempre
   `kind` e `template_id`, e rigenerare la fixture invece di tenerla scritta a
   mano (punto 5).

I residui **funzionali** delle catene (handoff a 0, backdrop, fit cover,
verifica del movimento camera) non stanno qui: sono in
[`TICKET-OVERLAY-CHAIN-ENTITY-IMAGES-2026-11-19.md`](TICKET-OVERLAY-CHAIN-ENTITY-IMAGES-2026-11-19.md).

## Correzione del ricontrollo 2026-11-21

> Il ricontrollo precedente era stantio: descriveva il tree **prima** della
> finestra v2, che nella stessa giornata ha cancellato la promozione Go. Testo
> corretto sul codice attuale (`refactored` working tree + `RenderingGen`):

- `promoteChainEntityCardSegments` e `capEntityImageOverlays`: **non esistono
  più** (`grep` sul codice = 0 match; restano solo nei documenti). Il ceiling
  vive nel planner (`promotion.rs:361`) e la promozione in `planner/promotion.rs`.
- `segmentIdentity` **esiste ancora** (`RenderingGen/renderinggen/internal/overlay/chain_segments.go:98`,
  chiamato da `chain_compile.go:332`): è l'unico residuo di inferenza.
- `DecodeChainSegments` era ancora presente in questo ricontrollo; è stato
  rimosso il 2026-11-22 insieme a `ChainSegmentMaps` (vedi §Open 6 eseguito).

Quindi Open 1 e Open 2 sono chiusi (finestra v2), Open 3 è verificato chiuso
qui sotto; Open 4, Open 5 e Open 6 sono chiusi. Resta solo l'A/B pre-v2.

## Decisione 2026-11-21 — Open 1 = (a), Open 2 = chiusa con finestra v2

### Open 1: (a), con una correzione di premessa

Scelta **(a)**: il vocabolario `preset.image` viaggia nella richiesta e il
sampler seedato viene portato in Rust. (b) è respinta: accettare una rotazione
diversa cambierebbe l'ingresso di **ogni** entità image, cioè una regressione
visiva su tutte le catene con ritratto, senza alcun guadagno di contratto.

L'obiezione del testo originale («a second implementation of the selection math
… which `deterministic_preset_sampler.go` explicitly rules out») è imprecisa:
quel docstring vieta gli **ID hardcoded** («the sampler never hardcodes a preset
id»), non un port pinnato con fixture di parità. Il repo ha già spostato
selection logic in Rust con vettori di parità (TICKET-15/16/19), quindi il port
è il precedente, non l'eccezione. Il vincolo reale è la parità **byte-exact**
del seed: quattro campi in quest'ordine (`job_fingerprint`, `scene_id`,
`semantic_id`, `preset_family`), SHA-256, `BigEndian.Uint64(sum[0:8]) % len`,
`sum[8:16]` per l'animazione. Vettori Go misurati sul tree
(`refactored@029f9b972`) da pinnare nel test Rust:

| seed (job, scene, item) | preset |
| --- | --- |
| `job_1795166123889306562_acaae690`, `scene-01`, `scene-01:chain-image:0` | `image_slide_left` |
| `job-fp-123`, `scene_03`, `sem_scene03_person_01` | `image_focus_in` |
| `plan-chain-2`, `scene-1`, `ov-chain-2` | `image_slide_right` |
| `p`, `s`, `i` | `image_focus_in` |

**Correzione (misurata): il catalogo è parte del problema, non solo il preset.**
`overlayPlanMedia` (`overlay_plan_v2_envelope.go:222`) popola
`media.verified_entity_assets` **solo** dalle `entity.Image` delle annotazioni,
con chiave stable+canonical dell'annotazione. Il resolver della promozione Go
(`entityCardMediaIndex`) indicizza anche `SemanticRenderBundle`, quindi i
ritratti **solo-bundle** (e gli alias canonici) non sono nella richiesta. In più
`promoteChainEntityCardSegments` **salta** i segmenti che hanno già asset
(`chainSegmentHasAssets`, riga 137): «ha asset» e «è promuovibile» sono insiemi
**disgiunti per costruzione**. Da qui due errori speculari se il planner
promuovesse ingenuamente:

- promuovere ogni card con asset trasforma le card annotation-backed in
  `entity_image` (oggi restano card ricche con testo) → regressione visiva;
- ignorare i bundle-only lascia senza ritratto proprio le card che oggi Go
  promuove → perdita di volti.

Serve quindi una superficie separata — `media.promotable_entity_assets`
(asset + lookup id + canonical + occurrence, risolti dallo stesso resolver) — e
il planner promuove una card **senza** `asset_refs` che ha una entry
promotable. Questo è il contenuto della finestra v2, non un refactor interno.

### Open 2: chiusa — un solo owner per volta, per costruzione

La riscrittura Go non viene duplicata: si elimina nella stessa finestra v2 che
porta (i) `canvas.image_presets`, (ii) il sampler Rust con i vettori sopra,
(iii) `media.promotable_entity_assets` e la promozione post-coalescing in
`coalesce.rs` (seed = id del chain item già fuso, così il preset coincide con
quello che Go sceglieva). Il confine tra i due owner è ora pinnato da
`TestPromotionLeavesAnAlreadyPromotedChainUntouched`
(`overlay_chain_segment_invariant_test.go`): se il planner ha già emesso
`entity_image`/`image_popup`, o la card porta già i suoi asset, la promozione Go
lascia l'item **byte-identico**. Non esistono due decisioni concorrenti:
l'owner è chi incontra il segmento ancora `entity_card` senza asset.

Gate per dichiarare chiusa la finestra (non eseguiti oggi):

1. `TestChainSegmentPromotionTouchesOnlyTheDeclaredSurface` ridotto a «nessuna
   chiave cambia» e promozione Go cancellata (non solo saltata).
2. `cargo test -p overlay-compiler` verde con i vettori di parità e i 30 golden
   del piano **rigenerati e dichiarati** (il piano cambia solo per le catene che
   oggi Go promuove).
3. Una run di accettazione su un job reale con catena di card: volti, ordine,
   preset d'ingresso e caption identici a prima. Senza questa run la finestra
   resta aperta, anche se i test sono verdi.

### Cosa è stato fatto in questa sessione

- Decisione (a) scritta qui e in `docs/architecture/OVERLAY-EDITORIAL-OWNERSHIP.md`.
- Premessa del catalogo corretta con i riferimenti di codice.
- Aggiunto il pin del confine `TestPromotionLeavesAnAlreadyPromotedChainUntouched`
  (  `go test ./internal/capabilities/scripts -run 'TestChainSegmentPromotion|TestPromotionLeaves'`
  → `ok`), che è l'artefatto che rende sicura la convivenza dei due owner fino
  alla finestra v2. **(Il pin è stato rimosso con la promozione Go: vedi
  §Finestra v2 eseguita.)**
- Nessuna modifica al sampler, al wire o al planner: uno scaffold parziale
  sarebbe stato codice morto (nessun consumer finché manca
  `promotable_entity_assets`), quindi è stato rimosso e il tree `BrainRust` è
  tornato pulito. **(Superato dalla finestra v2 del 2026-11-21: vedi sotto.)**

## Finestra v2 eseguita (2026-11-21)

Decisione (a) implementata end-to-end in un cambio atomico Go+Rust: se una parte
fosse atterrata da sola, o la promozione sarebbe stata duplicata o i volti
bundle-only sarebbero spariti.

- **Wire.** `canvas.image_presets` porta il vocabolario `preset.image`.
  `media.promotable_entity_assets` (`PromotableEntityAsset` /
  `PromotableEntityAssetV2`) è la superficie separata da
  `verified_entity_assets`: `overlayPlanMedia` emette una entry per occurrence che
  il resolver Go risolve in byte renderizzabili (URL + SHA-256, niente placeholder
  semantici), con l'identità che il planner stampa sull'item.
- **Sampler.** `editorial_plan_v2/planner/preset_sampler.rs` riproduce il sampler
  Go byte-exact, incluso il quirk per cui `BigEndian.Uint64` legge le prime otto
  cifre **ASCII** dell'hex digest, non i byte del digest. I quattro vettori
  misurati sul sampler Go sono pinnati nel test Rust.
- **Promozione.** `planner/promotion.rs` gira dopo coalescing, clipping e naming,
  promuove segmenti e card isolate, semina con l'id del chain item fuso (come
  faceva il chiamante Go), lascia intatto ciò che ha già asset o è già
  `entity_image`, rispetta `images=disabled` e applica il ceiling
  (`canvas.max_image_overlays`, fallback 20) solo se ha promosso qualcosa.
- **Go.** `promoteVerifiedEntityCards`, `promoteChainEntityCardSegments`,
  `capEntityImageOverlays` e i loro helper/test sono **cancellati**.
  `finalizeSealedOverlayPlan` non riscrive più identità; `compileOverlayPlanV2`
  riattacca la `LocalPath` delle portrait promosse per content address, perché la
  wire non la trasporta.
- **Identità.** `PlanItemV2` porta ora `entity_ref` (`EntityRefV2`: entity_id,
  type, name, canonical_entity_id opzionale), emesso per ogni card costruita dal
  timeline e conservato dalla promozione in place. La catena fusa non ha una
  sola identità e resta senza ref, come i suoi segmenti. Go lo proietta su
  `OverlayItem.EntityRef` con un DTO dedicato in `rustexec`, quindi la wire
  `overlay-plan.v1` non si allarga e il digest di render non cambia; i consumer
  (audit, editing timeline, freeze, entity motions, stream wire) tornano a
  leggere la provenienza canonica.
- **Golden.** Nuova fixture `entity_cards_promotable_media`: due card adiacenti
  che fondono in `image_chain` (entrambi i segmenti promossi) più una card
  isolata promossa in place, che copre `entity_ref` sul piano. Rigenerati con
  `examples/compile_fixture` solo i piani che contengono card:
  `minimal_phrase_only` e `entity_cards_verified_media` cambiano per il solo
  blocco `entity_ref`; gli altri sei restano byte-identici.
- **Verifica.** `cargo test -p overlay-compiler` (lib + `overlay_plan_contract`,
  32 test) verde; `go test ./internal/capabilities/scripts/...` e
  `./internal/capabilities/overlays/...` verdi, incluso il gate
  `TestOverlayPlanV2AllFixturesMatchFrozenGoldens` con il binario
  `semantic-engine` ricostruito (la fixture promotable è entrata nel gate);
  nuovo test di proiezione in `rustexec`; `go build ./...` verde.

Resta aperto:

1. **Run di accettazione visiva** su un job reale con catena di card: volti,
   ordine, preset d'ingresso e caption identici a prima (gate 3 della decisione).
   **Eseguita il 2026-11-21** su `entity-chain-pan-20261120-r12`
   (`job_1795279272120413702_bbf9e926`) e `entity-chain-flyunder-20261120-r4`
   (`job_1795279801017510593_47c4e504`): 6 entità PERSON, 6 asset distinti,
   catena da 6 segmenti in ordine, `motion_id` certificati per segmento,
   caption su ogni segmento, clip 1920×1080/24 a 408 frame, 21/21 gate. Manca
   solo l'A/B con il pianificatore pre-v2 per dire «identici a prima».
2. ~~Open 3: ceiling come campo della richiesta.~~ **CHIUSO 2026-11-21
   (verificato):** il valore è già un campo della richiesta —
   `max_image_overlays` (`contracts/generation/request.go:49`,
   `model.go:106`) → `canvas.MaxImageOverlays`
   (`runner_overlay_plan_branch.go:174`) → `canvas.max_image_overlays` sul wire
   (`overlay_plan_v2_envelope.go:86`) → `request.canvas.max_image_overlays`
   nel planner (`promotion.rs:361`, fallback in `media.rs:261`). La frase
   «viaggia dal media resolver» non descrive il tree attuale: il resolver
   produce solo il **default** run-level (`MaxImageOverlaysPerRun = 20`).
3. Open 4 è **chiuso** (vedi §Open e il test `TestCompileChainRejectsMissingKind`).
   Open 5 è **chiuso** (2026-11-21) e Open 6 è **chiuso** (2026-11-22, modello
   tipizzato unico; vedi §Open 6 eseguito).

Chiuso con questa finestra: **provenienza canonica delle card promosse**. Il
`PlanItemV2` espone `entity_ref`, la promozione lo conserva in place e Go lo
proietta su `OverlayItem.EntityRef`; l'audit non ricade più su `entity_id`. I
segmenti di catena restano senza ref — una catena fusa non ha una sola identità,
come non l'avevano i segmenti prima del cutover.

## Open 6 eseguito — modello tipizzato unico (2026-11-22)

- **Modello.** `OverlayItem.ChainSegments []ChainSegment` (`json:"-"`) è la
  proiezione tipizzata di `Params["chain_segments"]`; `UnmarshalJSON` la idrata
  al boundary e `Segments()` la decodifica una volta sola per i piani costruiti
  in memoria. `SetChainSegments()` aggiorna insieme campo e slot wire.
- **Ritiro.** `DecodeChainSegments`, `ChainSegmentMaps` e
  `UpdateChainSegmentMaps` non esistono più. `ValidateChainItem` usa il campo
  tipizzato; il child split usa `RebaseChainSegments`, l'unico helper di rebase
  al confine del modello. Nessun consumer di produzione contiene un decoder
  locale.
- **Consumer migrati.** `visual_budget_phrase`, `final_job_phrase_clip`,
  `model_render_fonts`, `render_queue_items`, `overlay_plan_finalize`,
  `overlay_plan_v2_map_items`, `overlay_audit_plan_images`. Gli helper di stile
  (`applyTextStyleToChainSegment`, `applyImageStyleToChainSegment`,
  `chainSegmentHasCaption`, `segmentIsImage`) ricevono `ChainSegment` o un suo
  puntatore.
- **Compatibilità del fingerprint.** Il setter riscrive `Params` come
  `[]map[string]any` (chiavi ordinate), non come struct: un piano rielaborato
  mantiene la stessa cache identity. `FingerprintValue` continua a
  canonicalizzare `Params` con il round-trip JSON esistente.
- **Verifica.** `go test ./internal/capabilities/overlays/...
  ./internal/capabilities/scripts/... -count=1` → PASS (inclusi model, contract
  e conformance); `go vet` sugli stessi package → PASS; `go build ./...` → PASS;
  `git diff --check` → 0. Il gate `make verify-main` non è stato rilanciato in
  questa passata.

## Open 5 eseguito — fixture generata dal planner (2026-11-21)

Il vecchio fixture era **irraggiungibile per costruzione**: incatenava cinque
segmenti `image`/`IMAGE_OVERLAY` senza caption, e `coalesce_chained_overlays`
non fonde mai le foto semplici (restano standalone). Non era quindi solo
«authored invece che generato»: descriveva un piano che il producer non può più
emettere.

- **Fixture producer.**
  `BrainRust/overlay-compiler/tests/fixtures/overlay_plan/requests/chain_segments_conformance.request.json`
  (cinque PERSON con ritratto verificato, caption verbatim, cinque motion
  certificate: `image_focus_reveal`, `image_scale_reveal`,
  `image_slide_left_reveal`, `image_slide_right_reveal`, `image_card_push`) è
  entrato nella matrice di `overlay_plan_contract.rs` insieme al piano generato
  `plans/chain_segments_conformance.plan.json`; il test confronta i byte del
  piano con l'output reale del planner (32/32 verdi).
- **Fixture condivisa.** Il piano è abbassato in `overlay-plan.v1` e scritto in
  `testdata/contracts/chain-segments-v1.json` e `RenderingGen/contracts/`
  (byte identici, SHA-256
  `a96712594aadf832c47674cfc557a4b20cea27898a70fa967dcaeedf87351093`). Il piano
  emesso ha **5 segmenti `entity_image`**, 5 asset distinti e 5 motion distinte
  (`image_scale_reveal`, `image_slide_left_reveal`, `image_slide_right_reveal`,
  `image_card_push`, `image_focus_reveal`).
- **Generatore.** `UPDATE_CHAIN_SEGMENTS_CONFORMANCE=1 go test
  ./internal/capabilities/scripts -run TestRegenerateChainSegmentsConformanceFixture`
  ricompila la richiesta col binario `semantic-engine` e riscrive entrambe le
  copie; una seconda esecuzione produce lo stesso SHA (idempotente).
- **Gate di deriva.** `TestChainSegmentsConformanceFixtureMatchesProducer`
  ricompila la richiesta a ogni run, verifica che le due copie siano
  byte-identiche e confronta `params.chain_segments` col piano reale. Le due
  metà Go/RenderingGen passano: `TestChainSegmentsSharedConformanceFixture` e
  `TestSharedChainSegmentsFixtureCompilesFiveImages`.

Open 6 è chiuso il 2026-11-22 (vedi §Open 6 eseguito). Resta solo l'A/B con il
pianificatore pre-v2 per l'accettazione visiva.
