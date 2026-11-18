# PERSON entity max — mapping from the Rust plan to the Go tests that exist

**Status:** canonical mapping (2026-11-18). Every test named below was checked
in the working tree on this date; the commands at the end run them.
**Owner:** `internal/capabilities/scripts` (planning), `internal/capabilities/images/entitycatalog`
(policy/state) and `internal/capabilities/scripts/adapters` (selection/binding).

## Why there is no Rust suite to run

- The branch `arena/71ea5f72-brainrust` does not exist locally or on the
  BrainRust remote.
- `imagecatalog/tests/person_entity_max.rs` was never committed: no BrainRust
  commit in any branch touches that path.
- `imagecatalog` was retired on 2026-11-18 by
  `BrainRust/docs/tickets/TICKET-28-imagecatalog-retirement.md`; its crate-local
  seed (`catalog_seed_20261110.json`) and annotations were deleted with it.
- The pasted text says "10 test" but lists 12 names.

The Go path is the single image authority (13b decision). This document is the
reachable replacement, split by the layer that actually owns each rule.

## Matrix

| Pasted Rust test | Go status | Go evidence |
|---|---|---|
| `canonical_parity_go_person` | **COVERED** | `TestPersonEntityMaxCanonicalParityGo` — `images/entitycatalog/person_entity_max_test.go`; every id is cross-checked against `capabilities/entities.CanonicalEntityID`. |
| `canonical_possessive_and_accents` | **COVERED** | Same table: straight/curly possessive, `José`, `O’Connor`, `Michael B. Jordan → michael-b--jordan`, CJK `李小龙 → ""`. |
| `seven_day_boundary` | **COVERED** | `TestPersonEntityMaxSevenDayBoundary` — same file; `2026-11-03T00:00:00Z` fresh, `2026-11-02T23:59:59Z` stale at `2026-11-10T00:00:00Z`. |
| `single_person_selects` | **COVERED** | Planning: `TestPersonEntityMaxSinglePersonPlansOneQuery` (`scripts/semantic_production_plan_person_max_test.go`). Selection: `TestInternetImagesProcessorCatalogHitSkipsProvider` (`scripts/adapters/entity_image_catalog_integration_test.go`) asserts a warm catalog serves the scene with zero provider calls. |
| `two_people_two_queries` | **COVERED** | Planning: `TestPersonEntityMaxTwoPeoplePlanTwoQueries` (two grounded queries, one per person). Segment: `TestPersonEntityMaxTwoPeopleOneSceneKeepBothPools` (`scripts/adapters/person_entity_max_selection_test.go`) — both canonical pools are loaded and neither person is dropped. |
| `reuse_prefers_unused` | **COVERED** | `TestPersonEntityMaxReusePrefersUnusedVariant` (`scripts/adapters/person_entity_max_selection_test.go`): two scenes of one person with two verified variants bind two *different* assets (run-scoped ledger in `projectEntityImageBindings`, falls back to reuse only when the pool is exhausted). Planner-level reuse: `TestSemanticProductionPlanReusesOneRetrievalPerAsset`, `TestPersonEntityMaxDistinctAssetsAreNotReused`. |
| `unknown_person_is_catalog_miss` | **COVERED** | `TestInternetImagesProcessorCatalogMissPopulatesAndReusesCanonicalEntity` — the miss is an explicit refresh/populate path, not a fabricated asset; the second run reuses the populated pool. |
| `not_person_is_not_catalog_eligible` | **COVERED** | `TestPersonCatalogIdentityExplicitlyExcludesNonPersonEntities` (`scripts/adapters/entity_image_catalog_contract_test.go`). |
| `replay_identical` | **COVERED** | Planner: `TestPersonEntityMaxPlanReplayIsByteIdentical`. Segment selection: `TestPersonEntityMaxReplayIsIdentical` (same warm catalog and input produce the same asset sequence). End-to-end idempotency: `TestVidRushReplayIsIdempotentAcrossSemanticDiscoveryAndPersistence`. |
| `live_seed_chiara_poggi_selects_reviewed_portrait` | **NOT PORTABLE** | The `49960` row existed only in the deleted crate seed; no reviewed seed or media id remains in Go. Not restored on purpose. |
| `live_seed_triptych_wins_with_two_people` | **NOT PORTABLE** | Same for `34357`/`23462`. The two-person behavior is covered by the planning and segment tests above. |
| `mismatch_annotation_rejects_even_accepted` | **COVERED** | Catalog level: `TestPersonEntityMaxAcceptedRowWithConflictingSourceNameIsNotSelected` — an `accepted` row whose filename names another person is not selected, while the row stays `accepted` for audit (catalog reads are pure). Unit level: `TestPersonImageSourceNameMismatchRejectsConflictingNamedImage`. Discovery level: `TestEntityImageCatalogRejectsWrongPersonWithGoodTechnicalQuality`. |

## Policy contract

`internal/capabilities/images/entitycatalog/testdata/catalog_policy_contract_rust.json`
is the surviving shared fixture (11 cases, moved there by TICKET-28).

- `TestPersonEntityMaxPolicyContractKeepsElevenCases` pins the count and the
  `pending`/empty stored status → `unknown` → ineligible edge.
- `TestGoCatalogPolicyMatchesSharedContractFixture` and
  `TestCatalogPolicyContractMatchesSharedFixtures` exercise the accept/reject
  decisions themselves.

## Cases from the plan that are deliberately absent

- **Live seed (`49960`, `34357`, `23462`).** Fixtures retired with the crate.
- **Network stress (20 scenes, 10 people).** The deterministic suite does not
  call providers or a VLM; the opt-in live batteries are described in
  `docs/operations/entity-image-pipeline-flow.md`, not here.

## Live gate (found → verified → materialized → reused)

The deterministic matrix above never calls a provider. The end-to-end probe is:

```sh
PERSON_CATALOG_LIVE=1 PERSON_CATALOG_QUERY="John Cena" OLLAMA_MODEL=gemma4:e2b \
  go test ./internal/capabilities/scripts/adapters \
  -run '^TestLiveImageRetrievalJobPersistsAndReusesAssets$' -v -count=1 -timeout 6m
```

It drives the production media stage (retrieval registry), the Ollama identity
verdict, the SQLite entity catalog and the materialization processor with an
isolated local finalizer. It needs a running Ollama with the vision model and
reachable providers (`SEARXNG_URL`, DuckDuckGo fallback). On success it logs
cold (`searches=1 assets_saved=N materialized_catalog_rows=N`) and warm
(`searches=0 new_saves=0 reused_ready=N`) counters.

The input must carry `Insights.Entities` / `ImageQueries`: the stage derives
entity queries from insights, so an annotations-only input is sent to
`internetImageDiscoveryBypass` and the gate proves nothing. That is why the
fixture in `image_retrieval_job_live_test.go` was fixed on 2026-11-18; the
insights-are-authoritative rule itself is pinned by
`TestEntityImage_SceneQueriesIncludeEntitiesMissingFromPartialAnnotations`.

## Commands

```sh
cd refactored
go test -count=1 ./internal/capabilities/images/entitycatalog
go test -count=1 -run 'PersonEntityMax' ./internal/capabilities/scripts
go test -count=1 -run 'PersonEntityMax' ./internal/capabilities/scripts/adapters
go test -count=1 ./internal/capabilities/scripts/adapters
```

## Invariants this mapping must not weaken

- Provenance stays fail-closed: an `accepted` row without a current
  `verifier_version` and an explicit visual-quality reason remains ineligible
  (`TestIsUsableCandidateQualityDefersUnknownDimensionsButKeepsProvenanceFailClosed`).
- Unknown dimensions are deferred, not rejected: the probe happens after the
  fetch, so `0×0` rows may stay eligible with their documented penalty.
- A catalog lookup never downloads, verifies or binds; selection and binding
  require acquired, verified bytes (`entity-image-pipeline-flow.md`, failure
  rules 1–4).

## Stato dei criteri del ticket person-image (2026-11-18)

Riferimento: `docs/operations/TICKET-PERSON-IMAGE-IDENTITY-2026-11-18.md`.
Solo i criteri verificabili con una prova nel repository sono elencati come
chiusi; il resto è esplicitamente aperto.

| Fase | Criterio | Stato | Prova |
|---|---|---|---|
| P0 | Report per entità con fonte e candidati | **chiuso** | `TestPersonImageVisualDump`: 13 entità, 48 candidati, 42 verificati tecnicamente (dump del 2026-11-18) |
| P0 | Distinzione `NO_QUERY` / `PROVIDER_EMPTY` / `UNVERIFIED` / `IDENTITY_MISMATCH` / `NOT_MATERIALIZED` | **chiuso** | `entityImageFailureReason` + `entity_image_failure_reason_test.go` |
| P0 | Verdict e motivo per entità sulle 13 | **chiuso con revisione visiva** | dump del 2026-11-18 riletto immagine per immagine: 20/42 candidati mostrano la persona (tabella sotto); verdict VLM del pipeline misurato live solo su `John Cena` (`accepted`, 0.99) |
| P1 | Wikipedia/pageimage valutata prima di DuckDuckGo per PERSON | **chiuso** | `TestPersonRouteQueriesWikipediaAndKeepsDuckDuckGoFallback` |
| P1 | Fallback DuckDuckGo attivo senza pageimage / su errore | **chiuso** | `TestPersonRouteKeepsDuckDuckGoWhenWikipediaHasNoPageImage`, `...WhenWikipediaSearchFails`, `...WhenWikipediaProviderIsMissing` |
| P1 | Nessun bind senza verdict | **chiuso** | `TestIsUsableCandidateQualityDefersUnknownDimensionsButKeepsProvenanceFailClosed` + `projectEntityImageBindings` |
| P2 | Face embedding con riferimento | **bloccato** | richiede una decisione su modello/licenza/soglia (domanda 3 del ticket); nessuna dipendenza introdotta |
| P3 | Config SearXNG esplicita e riproducibile | **chiuso** | `searxng/settings.yml` montato nel container; `docs/operations/searxng-image-policy.md` |
| P3 | Prime 6 posizioni PERSON senza Pinterest/stock/artwork | **chiuso** | 47/78 → 0/78 host negati, 0 entità vuote, falsi positivi da omonimia risolti dopo l'upgrade a SearXNG 2026.11.18 (`TestSearXNGPersonPolicyLive` + revisione visiva) |
| P3 | Nessun nuovo metamotore nel fan-out | **chiuso** | i provider restano Wikipedia/Wikimedia/SearXNG/DuckDuckGo/Drive |

### Perché P2 è bloccato e non simulato

Il criterio richiede un'immagine di riferimento per identità, un face embedding
locale con licenza e una soglia calibrata su un set etichettato. Non esiste nel
repository un modello né un set di calibrazione, e la scelta (ArcFace/FaceNet,
licenza, memoria) è una decisione di prodotto. Introdurre una soglia arbitraria
violerebbe la regola del ticket ("nessuna immagine non verificata può essere
bindata") e la `confidence` del VLM non è una probabilità calibrata.

### Revisione visiva del dump SearXNG (2026-11-18)

Le 42 immagini scaricate dal dump sono state aperte e confrontate a vista con
l'entità. Non è un verdetto del pipeline: è il controllo che il bacino dei
candidati contenga o meno la persona giusta.

| Entità | File | Corretti | Cosa c'era di sbagliato |
|---|---:|---:|---|
| Mike Tyson | 5 | 1 | poster della serie *MIKE* (l'attore), Mike Wazowski ×2, *Mike the Knight* |
| Chiara Poggi | 1 | 0 | scatola di *play money* Melissa & Doug |
| Alberto Stasi | 1 | 0 | poster di *The Miseducation of Cameron Post* |
| Michael Burry | 6 | 6 | — |
| George Washington | 6 | 6 | — (ritratti storici, due duplicati) |
| Conan O'Brien | 6 | 0 | poster di *Detective Conan* |
| Michael B. Jordan | 5 | 0 | Michael Jackson (poster biopic e foto) |
| Elon Musk | 6 | 5 | 1 composito crypto/Nasdaq generato |
| Steve Jobs | 5 | 2 | skin di Minecraft "Steve" ×3 |
| José María Aznar | 1 | 0 | bottiglie di liquore cinese |
| Bruce Lee, Hayao Miyazaki, Gianluca Ferretti | 0 | — | nessun candidato |
| **Totale** | **42** | **20 (47,6%)** | |

Conclusione operativa: la curation degli engine e la deny-list eliminano lo
stock (0 host negati), ma non salvano da un upstream degradato. Il 2026-11-18
Bing rispondeva a questo IP con SERP non correlate (gatti e incisori laser per
`Mike Tyson 1991`, misurato direttamente su `bing.com/images/async`), quindi i
falsi positivi non sono un problema di query né di ranking. La prova incrociata
è `PERSON_IMAGE_VISUAL_DUMP_SOURCE=ddg`: DuckDuckGo diretto restituisce 6/6 URL
per tutte le 13 entità e il campione verificato (Tyson 1991, Conan O'Brien,
Chiara Poggi) è corretto.

### Dopo il fix del 2026-11-18 (SearXNG 2026.11.18)

La causa era l'upstream Bing degradato, non la query. Aggiornando l'immagine
SearXNG e disabilitando `bing images` restano `duckduckgo images`,
`google cse images` e `brave.images`: tutte le 13 entità tornano con 6
candidati (78 totali, 0 vuote, 0 host negati) e il campione visivo è corretto
per Tyson 1991, Conan, Michael B. Jordan, Jobs a Stanford, Poggi, Stasi,
Aznar, Miyazaki, Burry ed Elon Musk. Dettagli e misure in
`docs/operations/searxng-image-policy.md`.

Due regole che ne derivano:

- **La produzione PERSON non dipende da SearXNG**: usa Wikipedia/Wikimedia e il
  provider DuckDuckGo diretto. SearXNG resta fallback e su questa istanza è
  inaffidabile finché l'engine Bing non viene sostituito o aggiornato.
- **Il gate VLM resta obbligatorio**: senza verdetto, il 52% dei candidati di
  questa run (22/42) sarebbe un bind sbagliato, e nessuna policy di engine
  può distinguere un Tyson da Mike Wazowski.
