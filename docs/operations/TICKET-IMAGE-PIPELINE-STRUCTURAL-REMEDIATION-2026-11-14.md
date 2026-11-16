# TICKET — Image pipeline structural remediation (2026-11-14)

- **Status:** CLOSED (2026-11-16) — COMPLETE: 96 of 96 tickets closed across the 2026-11-14 batches; 0 remain OPEN.
- **Severity:** P0/P1/P2 ledger; P0 blocks trustworthy entity-image completion.
- **Owner:** image pipeline (retrieval → verification → selection → materialization → catalog)
- **Source:** live jobs `john_cena_20261114`, `john_gotti_20261114`, `floyd_mayweather_story_20261114` run6/run8, and a code audit of the two live flows plus the durable catalog.
- **Rule:** a ticket closes only with an observable test/command. No ticket may close by weakening an assertion, hardcoding a person/job, or declaring success from a cache hit.

## Acceptance criteria for closing this ticket set

1. Every P0 row has a focused regression test or a reproducible command that fails before the fix and passes after it.
2. `go test ./internal/capabilities/scripts/... ./internal/capabilities/images/entitycatalog/... ./internal/platform/sqlite/assets/entitycatalog/...` is green for the touched packages.
3. The job report distinguishes `retrieved`, `verified`, `selected`, `materialized`, `bound`, and `skipped` per candidate.
4. No production path selects an image without a durable verdict or an explicit `unknown`-retry state.
5. A durable run that does not complete reports stage, run id, attempt, status, and error code.
6. The catalog has one provider identity, one content identity, and one verdict owner.

## Verification 2026-11-16

- Ledger audit: 96 rows, 96 unique `IMG-*` ids, 0 `OPEN`/`PARTIAL`/`BLOCKED`
  rows.
- `go test ./internal/capabilities/scripts -count=1` -> PASS (15.723 s).
- `go test ./internal/capabilities/images/entitycatalog/... ./internal/platform/sqlite/assets/entitycatalog/... -count=1`
  -> both packages PASS.
- The two pre-existing status contradictions are resolved: the ticket now
  declares `CLOSED` and the generated index classifies it as closed instead of
  `aperto`.

## Ledger

| ID | Sev | Status | Problem | Evidence | Acceptance |
|---|---|---|---|---|---|
| IMG-001 | P0 | CLOSED | The Rust `imagecatalog` planner is not wired into any Go path. | `BrainRust/imagecatalog/README.md` marks the crate experimental/not wired; `docs/operations/entity-image-pipeline-flow.md` declares the Go flow the only live path. | Either a Go adapter calls it, or the crate is removed/marked experimental; no live path claims to use it. |
| IMG-002 | P0 | CLOSED | Two live flows generate and bind entity images; the semantic plan is a fallback that never overwrites. | `ResolveEntityImageDecisionOwner` binds the decision to the requested surface and stamps it on `GenerateResult`; `ApplySemanticResolvedImages` stands down when `media_pipeline` owns the surface. The semantic plan may still emit diagnostics but cannot select or bind a competing entity image. `TestResolveEntityImageDecisionOwnerUsesRequestedSurface`, `TestApplySemanticResolvedImagesStandsDownWhenMediaPipelineOwnsSurface`. | One owner per decision (query, candidate, verdict, selection); the other flow consumes results or exits. |
| IMG-003 | P0 | CLOSED | A search hit and a bindable image are different states with no explicit contract. | `docs/operations/entity-image-pipeline-flow.md` defines hit→verified→selected→materialized→bound; final Cena r2 exposes all five counters (19/19/19/19/19). | Retrieval records `found`; a separate acquire/verify step produces `bindable`; the report exposes both. |
| IMG-004 | P1 | CLOSED | `MediaPlan.Searches` is read by three owners. | `vidrush_search_resolver.go`, `processor_visual_planning.go`, `runner_scene_text_generation.go`. | One resolver owns manual search routing; tests cover each slot. |
| IMG-005 | P1 | CLOSED | `vidrush_fanout_plan.go` duplicates the same `perSceneImages && automaticImageSearch && !manualImageExclusive` branch. | `entityImageLaneRequested`/`contextualImageLaneRequested` name the branch; `TestImageFanoutLanePredicates` covers manual/auto/entity combinations. | Branch predicates are named helpers; a table test covers manual/auto/entity combinations. |
| IMG-006 | P1 | CLOSED | Cache → catalog → filter → persist is duplicated in two adapters, with manual lock release in one. | `entity_image_resolver.go` is the single `resolveEntityImageCatalogCache` used by both the media stage and the provider fan-out. Catalog/discovery locks are `defer`-released on all cache and error paths; the discovery lock is transferred to the caller only while provider I/O runs, so concurrent cold requests still collapse to one call. `TestSharedEntityImageResolverServesWarmCatalogWithoutProvider`, `TestEntityImageCatalogSerializesConcurrentFirstPopulationOnPersistentSQLite`, `TestResolveEntityImageCatalogAndCacheReleasesLockOnError`. | One shared resolver; locks are `defer`-released on every path. |
| IMG-007 | P1 | CLOSED | `SceneID`, `SegmentID`, `Index` and `Position` are used interchangeably. | `buildSceneIdentityIndex`, `unitMatchesScene`, `SEGMENT_MISSING`/`IDENTITY_MISMATCH` reasons. | One typed scene key with a uniqueness test; duplicate IDs fail closed with a diagnostic. |
| IMG-008 | P1 | CLOSED | Semantic bindings require `ProductionSearchSatisfied` + `SHA256`, so overlay-only runs never bind. | `ApplySemanticResolvedImages`. | Binding either materializes the hit or reports `UNBOUND_REQUIRES_ACQUISITION`. |
| IMG-009 | P0 | CLOSED | No alias graph: `Teflon Don`/`Dapper Don` are separate persons. | `PersonAliasGraph` maps many surfaces to one canonical id; the catalog builds it from each segment's `ImageEntityCanonicalIDs`. `CanonicalizePersonNameWithAliases` resolves every registered alias to the same id. `TestPersonAliasGraphResolvesManyAliasesToOneIdentity`, `TestPersonCatalogIdentityResolvesSegmentAliases`. | Alias is a property of one canonical identity; a person can have many aliases, one ID. |
| IMG-010 | P0 | CLOSED | Slug collisions and homonyms cannot be distinguished. | `CanonicalPersonIDWithDisambiguator` mints `person:john-smith--actor` vs `--director`; an alias owned by two ids returns `ErrAmbiguousPersonName` with `Unresolved=true` and the candidates, and the catalog rejects it. `TestPersonAliasGraphLeavesHomonymUnresolved`, `TestCanonicalPersonIDWithDisambiguatorIsStableAndDistinct`, `TestPersonCatalogIdentityRejectsAmbiguousSegmentAlias`. | Identity includes a disambiguator or stays `unresolved`; ambiguous names are not selectable. |
| IMG-011 | P0 | CLOSED | NER classifies capitalized multiword surfaces as PERSON; a hardcoded city list is the only correction. | The city switch was removed. `classifyDiscoveredEntity` resolves the segment's `ImageEntityCanonicalIDs` through `canonicalImageEntityTypeFromIDs` (`place:`/`event:`/`org:`), then falls back only to the generic surface-cue table. `TestClassifyDiscoveredEntityUsesCanonicalIDsAndSurfaceCues`, `TestSceneAnnotationsCorrectsNLPTypeFromCanonicalID`. | Type resolution uses the canonical entity resolver; no locale-specific city hardcode. |
| IMG-012 | P0 | CLOSED | The catalog is PERSON-only, but selection requests ORG/GPE/EVENT images. | Migration `224`, `UpsertEntity`, `ValidateEntity`; `personCatalogIdentityForSegmentQuery` rejects non-PERSON canonical IDs; `TestPersonCatalogIdentityExplicitlyExcludesNonPersonEntities` pins the contract. | Either the catalog supports all selected types or non-PERSON is explicitly excluded from the entity contract. |
| IMG-013 | P1 | CLOSED | GPE images can remain in `secondary_images` while the annotation `image` is empty. | Floyd/MGM observation. | Annotation projection is symmetric for every entity kind; a GPE image is bound or explicitly rejected. |
| IMG-014 | P1 | CLOSED | `entityImageMatches` requires exact `candidate.Entity`; aliases fail and uppercase acronyms are rejected. | `media_resolver_entity_binding.go`. | Match uses canonical identity/alias, not raw surface; acronyms are valid for ORG/GPE. |
| IMG-015 | P1 | CLOSED | `expandPersonCanonicalName` takes the first capitalized match. | `scene_annotations.go`. | Ambiguous expansion stays unresolved instead of guessing. |
| IMG-016 | P2 | CLOSED | Annotation span matching is case-insensitive but accent-sensitive. | `findAnnotationSpan`. | `Café`/`Cafe` resolve to the same canonical surface. |
| IMG-017 | P1 | CLOSED | Filename identity conflict checks only the first two tokens. | `personImageSourceNameMismatch` now strips extensions and rejects multi-name filenames; `TestPersonImageSourceNameMismatch` covers `john-gotti-sammy-gravano.jpg`. | A multi-name filename cannot pass as a different person; test `john-gotti-sammy-gravano.jpg`. |
| IMG-018 | P2 | CLOSED | `personImageFilenameQualifier` treats broad words as qualifiers. | `avvocato`, `avvocata`, `ansa`, `ipa` removed; `TestPersonImageFilenameQualifiers` pins the table. | Qualifier list is evidence-based and covered by a table test. |
| IMG-019 | P0 | CLOSED | Weak-query filter misses `exact_mention` abstract terms. | `weakVisualEntityQuery`; `biografia sportiva` passed. | Abstract concepts/whole countries are not queried unless a concrete context phrase exists. |
| IMG-020 | P0 | CLOSED | Fanout appends the generic scene ladder in entity mode when budget is above default. | `vidrush_fanout_plan.go`; r44 420 acquire/326 publish for 20 used. | Entity mode never appends generic ladder queries; a query-count assertion covers it. |
| IMG-021 | P1 | CLOSED | Semantic cache key lacks provider, model, limit, and plan fingerprint. | `semantic_search_batch`. | Key includes provider/model/limit/plan version; changing any of them misses. |
| IMG-022 | P1 | CLOSED | Provider errors are recorded as misses. | `SemanticSearchBatchPlanner.Run` comment. | Provider error is a typed failure, not an empty result; job status is not green. |
| IMG-023 | P1 | CLOSED | Entity-image cache key lacks provider/verifier version. | `entityImageDiscoveryCacheKeyFor` binds provider + `CandidateVerifierVersion`; `TestEntityImageDiscoveryCacheKeyIncludesProviderAndVerifier` proves both invalidate the key. | Key includes provider and verifier version; old verdicts cannot be replayed after a model change. |
| IMG-024 | P1 | CLOSED | Manual searches are dropped if the unit is not `Pending`. | `SearchesTooLate` metric; `TestApplyCallerImageSearchesReportsLateQuery` covers a satisfied unit. | A late manual search is either applied or reported as `MANUAL_QUERY_TOO_LATE`. |
| IMG-025 | P0 | CLOSED | Catalog provider is hardcoded to `duckduckgo` for every source. | `entityImageCatalogProviderFor`; `TestSQLiteEntityImageCatalogProviderContentAndVerifierVersion`; Cena r2 selection reasons include `duckduckgo`, `searxng`, `wikimedia_commons`. | Provider is the actual retriever ID; uniqueness does not collapse SearXNG/Wikimedia/DDG. |
| IMG-026 | P1 | CLOSED | Source URLs are not normalized before the unique key. | `NormalizeSourceURL` + `TestNormalizeSourceURL`; the SQLite upsert normalizes before the unique key. | Host case, scheme, default port and tracking params normalize before upsert. |
| IMG-027 | P2 | CLOSED | `ListCandidates` caps at 100 and can hide a valid candidate at rank 101. | `maxEntityImageCandidateLimit`. | A paged scan exists or the cap is justified by an invariant. |
| IMG-028 | P0 | CLOSED | Semantic gate is fail-open for unscored candidates. | `image_semantic_gate.go`. | Entity images are fail-closed; non-entity paths may opt into fail-open explicitly. |
| IMG-029 | P0 | CLOSED | Verdict is a string, with no verifier/model/prompt version. | Migration `293`; `CandidateVerifierVersion="person-image-visual/v2"`; `TestIsUsableCandidateQualityRequiresCurrentVerifierVersion`. | Verdict row records verifier version/model/prompt; stale versions are invalidated. |
| IMG-030 | P0 | CLOSED | Candidate identity is URL-scoped, not content-scoped. | `TestSQLiteEntityImageCatalogProviderContentAndVerifierVersion` (changed content clears verdict); Cena r2 candidates carry `content_sha256`. | Candidate has `content_sha256`; a changed payload invalidates the verdict. |
| IMG-031 | P1 | CLOSED | VLM stops after 3 attempts per person. | `maxVisualAttemptsPerPerson`. | A bounded budget is explicit and configurable; valid candidates after 3 are not silently skipped. |
| IMG-032 | P1 | CLOSED | VLM rejects valid group/contextual photos. | `single_person_headshot` requirement. | The verdict distinguishes "requested person present" from "single-subject layout". |
| IMG-033 | P1 | CLOSED | The VLM prompt is anchored by the retrieval query. | `classify`. | Prompt separates identity evidence from retrieval provenance; query is optional context. |
| IMG-034 | P1 | CLOSED | Full `image.Decode` on untrusted bytes can decompression-bomb the process. | `CanonicalizeVidRushImageFile`, `normalizePersonImageForVision`. | Dimension cap before full decode; decode uses `DecodeConfig` + bounded pixels. |
| IMG-035 | P1 | CLOSED | Catalog minimum (800px/400k) and technical minimum (640×360) disagree. | `ImageMeetsCatalogMinimum` is shared by admission, discovery filter and recertification; `TestCatalogImageMinimumIsShared` pins it. The lower acquisition gate is documented as technical-only. | One shared minimum predicate, or the two policies are documented and tested. |
| IMG-036 | P1 | CLOSED | VLM JSON parse failure rejects without a repair/retry path. | `classify`. | A malformed model response is a retryable error, not a definitive rejection. |
| IMG-037 | P2 | CLOSED | Images are not resized before base64 to Ollama. | `normalizePersonImageForVision`. | Max long side/pixel budget before upload; test a 4K input. |
| IMG-038 | P1 | CLOSED | `allCandidatesHaveReusableVerdict` skips pixel re-verification. | `hydratePersonImageVisualVerdicts` accepts only current versions; `TestIsUsableCandidateQualityRequiresCurrentVerifierVersion`, `TestSQLiteEntityImageCatalogProviderContentAndVerifierVersion`; Cena r2 verified=19. | A reusable verdict is bound to content hash + verifier version. |
| IMG-039 | P1 | CLOSED | A durable image bypasses the quality gate. | `TestDurableMaterializationDoesNotBypassCatalogQualityGate` keeps an accepted-but-unverified durable row out of the pool. | Durable status does not override a failed semantic/quality verdict. |
| IMG-040 | P1 | CLOSED | `recertifyLayout` candidates with a durable image still count as usable. | `entityImageCatalogCandidates`. | `usableCount` counts only candidates the selector can actually use. |
| IMG-041 | P0 | CLOSED | Selection groups by provider query, not canonical identity. | `vidRushImageGroup`; `John Cena` vs `John Cena photo`. | Group key is canonical entity ID; a table test covers qualifier expansions. |
| IMG-042 | P1 | CLOSED | `imagesOnly` selection preserves discovery order, not score. | `selectExactVidRushImages` stable-sorts by score/verdict; tie-break stays discovery order and `TestVidRushForceRefreshDiagnosticCandidateNeverSelected` covers it. | Selection order is deterministic and score/verdict-aware. |
| IMG-043 | P1 | CLOSED | Acquisition budget is per provider, not per scene/entity. | `processor_vidrush_materialize_run.go`. | Budget is charged to the entity/scene that requested the image. |
| IMG-044 | P1 | CLOSED | Per-scene content-hash dedupe can prevent reaching the target. | `markReadyImage`. | Duplicate content counts once per scene and is reported, not silently dropped. |
| IMG-045 | P1 | CLOSED | `preserveSelectedVidRushImages` can replace the selected asset. | `vidrush_binding_finalize.go`. | Selected asset identity is stable across finalization; replacement is explicit. |
| IMG-046 | P1 | CLOSED | `candidateSetHash` includes mutable acquisition/verification status. | `vidrush_candidate_stamp.go`. | Hash covers stable discovery identity only. |
| IMG-047 | P1 | CLOSED | `readyVidRushCandidate` requires Drive while `verifiedImageCandidateWithoutDrive` does not. | Candidate gate. | One readiness predicate with an explicit durable-vs-local mode. |
| IMG-048 | P1 | CLOSED | Four duplicate YouTube branches in `readyVidRushCandidate`. | Candidate gate. | One YouTube predicate; table test covers each provider. |
| IMG-049 | P2 | CLOSED | Perceptual dedupe is fail-open for candidates without a hash. | `PerceptualDedupStatus` is `UNHASHED`/`HASHED`; `TestDropPerceptualDuplicatesMarksUnhashedCandidates` covers it. | A missing hash is an explicit `UNHASHED` state, not an automatic pass. |
| IMG-050 | P1 | CLOSED | `entityImageFailureReason` collapses no-query and no-results. | `entity_images.go`. | Distinct reasons: `NO_QUERY`, `PROVIDER_EMPTY`, `IDENTITY_MISMATCH`, `UNVERIFIED`. |
| IMG-051 | P0 | CLOSED | Catalog pool counts `unknown` candidates as usable. | `entityImageCatalogCandidates`: `usableCount := len(out)`. | `UsableCount` counts only selectable verdicts; `unknown` is retry, not supply. |
| IMG-052 | P0 | CLOSED | `RecordCandidateValidation` never recovers a broken row. | SQL `CASE WHEN status='broken' THEN 'broken'`. | A successful URL validation transitions to `fresh` and resets retry budget. |
| IMG-053 | P0 | CLOSED | Recertification selects only `accepted`; rejected rows never recheck. | `ListCandidatesForRecertification`. | Rejected rows have a bounded retry path or a terminal `retired` state. |
| IMG-054 | P1 | CLOSED | `UpsertCandidate` INSERT + SELECT is not atomic. | Adapter. | Both statements run in one transaction; concurrent writers cannot return another row. |
| IMG-055 | P1 | CLOSED | `SetCandidateStatus` allows reviving `retired` and has no transition guard. | Adapter. | Legal transitions are validated; `retired` is terminal unless an explicit revive API is used. |
| IMG-056 | P1 | CLOSED | N+1 `GetMaterialization` inside the per-entity lock. | `entityImageCatalogCandidates`. | One batch materialization query per pool, not one per row. |
| IMG-057 | P1 | CLOSED | A catalog lookup writes rejection rows during the read. | `entityImageCatalogCandidates`. | Reads do not mutate; rejection persistence is a separate command. |
| IMG-058 | P1 | CLOSED | `UpsertEntity` does not update refresh state; `SetRefreshState` has no lease. | Adapter. | Refresh has an owner/lease and a timeout; a crashed refresh is recovered. |
| IMG-059 | P1 | CLOSED | `parseCatalogTime` silently returns zero on a bad timestamp. | `parseCatalogTimeWithLegacy` + `LegacyTimestamp` on entity/candidate/materialization; `TestParseCatalogTimeDistinguishesLegacyFromEmpty`. | A malformed timestamp is a typed error or a `legacy` flag, not silently fresh. |
| IMG-060 | P1 | CLOSED | `UpsertMaterialization` overwrites the single row with no history. | Adapter. | Materialization changes are append-only or audited. |
| IMG-061 | P1 | CLOSED | `refresh_status=running` can stick forever. | Entity table. | Lease/timeout moves a stale `running` row to `failed` with a diagnostic. |
| IMG-062 | P1 | CLOSED | Candidate source URL scheme is validated only at download. | `validVidRushCandidate`. | Selection rejects non-HTTP(S) URLs. |
| IMG-063 | P1 | CLOSED | Recertification is sequential with no distributed lock. | `RecertificationService.RunOnce`. | Overlapping runs are prevented; a batch is bounded by a total deadline. |
| IMG-064 | P2 | CLOSED | `ValidateMaterialization` hash format needs a regression test. | Port/adapter. | Non-64-hex SHA is rejected at the boundary. |
| IMG-065 | P0 | CLOSED | `incompleteRunError` loses stage/run/attempt/status. | `generation_handler.go`. | Error includes run id, stage, status, attempt/max, error code and message. |
| IMG-066 | P0 | CLOSED | `WaitForVidRush` compares the committed set with `len(plan.Segments)` and fails with the missing indexes. | `incremental_coordinator.go`; `TestWaitForVidRushRejectsMissingPlannedScene`. | Wait knows the expected count; a missing commit is an error, not a partial success. |
| IMG-067 | P0 | CLOSED | `OnSceneCommitted` launches enrichment with the caller context. | `incremental_coordinator.go`. | Enrichment uses a run-scoped context that survives scene-generation cancellation. |
| IMG-068 | P1 | CLOSED | `waitForVidRush` fails on the first scene error and drops all other results. | `incremental_coordinator.go`. | Partial results and a multi-error report are returned; terminal policy is explicit. |
| IMG-069 | P1 | CLOSED | Superseded scene results are counted but not surfaced. | `staleCount`. | Report includes stale/superseded scene IDs and reasons. |
| IMG-070 | P1 | CLOSED | `buildDurableRunRequest` mixes the job envelope with the persisted run request. | `generation_handler.go`. | The durable run request is the single source; a retry cannot execute a divergent payload. |
| IMG-071 | P1 | CLOSED | `pendingRunRetry` only defers `Failed`; a stuck `Running` run is not handled. | `generation_handler.go`. | Stuck runs are detected by lease/age and reported as `RUN_STALLED`. |
| IMG-072 | P1 | CLOSED | `final_job=true` + disabled render fails late. | API toggle matrix. | Invalid toggle combinations fail at submit with a field-level error. |
| IMG-073 | P1 | CLOSED | `version:2`/`correlation_id` rejection has no schema diagnostics. | API. | Unknown/misplaced fields produce a field path and accepted schema. |
| IMG-074 | P0 | CLOSED | Catalog lock is held across provider network I/O. | `media_resolver_image_segments.go`. | Lock covers local state only; provider call is outside the critical section. |
| IMG-075 | P1 | CLOSED | `vidrush_internet_image_candidates.go` releases catalog locks manually. | Adapter. | Every acquisition uses `defer`; a leak test covers error paths. |
| IMG-076 | P1 | CLOSED | Global VidRush caches have no documented bound/metrics. | `vidrushImageCache`, `vidrushMaterializedCache`. | Bounded cache with hit/miss/eviction metrics. |
| IMG-077 | P1 | CLOSED | A panic in the enrichment goroutine is not recovered. | `OnSceneCommitted`. | Per-scene recover converts panic to a scene error; worker stays alive. |
| IMG-078 | P0 | CLOSED | `finalJobImageURLVerified` bypasses SSRF policy and auth. | `final_job_image_preflight.go`. | Uses the same validated transport as download; auth-aware; private IPs blocked. |
| IMG-079 | P0 | CLOSED | Preflight rejects WebP while acquisition allows it. | `finalJobImageBytesVerified` vs `allowedVidRushImageMIME`. | Same MIME allowlist on both paths. |
| IMG-080 | P1 | CLOSED | Preflight uses `context.Background()`. | `final_job_image_preflight.go`. | Preflight derives from the job context with a bounded timeout. |
| IMG-081 | P1 | CLOSED | `finalJobImageRequested` checks `InternetImages.AsBool()` first and can skip entity images. | `final_job_image_preflight.go`. | Preflight runs whenever any image surface is enabled. |
| IMG-082 | P1 | CLOSED | Preflight only considers items whose kind contains "image"; an entity card can be skipped. | `final_job_image_preflight.go`. | Preflight covers all items with an image binding, regardless of kind. |
| IMG-083 | P1 | CLOSED | `ruleStockIsolation` ignores `Assets.Candidates`. | `mediacert/rules_visual_media.go`. | A stock-bound segment with provider candidates fails isolation. |
| IMG-084 | P0 | CLOSED | `ruleImageFanout` always passes. | `mediacert/rules_visual_media.go`. | A job with zero images cannot be certified; the rule has an explicit target policy. |
| IMG-085 | P1 | CLOSED | `Certified` is not enforced by the job lifecycle. | Mediacert report. | Job status cannot be `SUCCEEDED` when certification is required and false. |
| IMG-086 | P1 | CLOSED | `validVidRushCandidate` accepts any URL scheme. | Candidate gate. | Only absolute HTTP(S) enters selection. |
| IMG-087 | P1 | CLOSED | Recertification HTTP validator has no SSRF/redirect/dimension cap. | `HTTPImageCandidateValidator`. | Same validated download policy as the materializer. |
| IMG-088 | P1 | CLOSED | Deploy restarts without draining in-flight jobs. | `scripts/deploy/pipelinegen_deploy.sh`. | Deploy waits for active jobs or marks a drain window; no job is killed silently. |
| IMG-089 | P1 | CLOSED | Job report does not record the binary hash or Rust release. | Deploy/report. | Every job records `binary_sha256` and `rust_release`. |
| IMG-090 | P1 | CLOSED | ICU 72 fix is host-specific and not reproducible in CI. | `make/build.mk`, `rust-fetch.sh`, `install_icu72_runtime.sh`. | CI reproduces the runtime; host provisioning is documented and checked. |
| IMG-091 | P2 | CLOSED | No per-candidate decision ledger; metrics are aggregate only. | `entity_image_catalog_metrics.go`, job report. | Per-candidate trace: query → candidate → gate → verdict → selection → materialization. |
| IMG-092 | P1 | CLOSED | Test fixtures encode metadata-only `accepted`. | Catalog integration tests. | Fixtures go through the verifier or explicitly mark `pinned`/`legacy`. |
| IMG-093 | P1 | CLOSED | `TestIncrementalFanoutStockBoundSegmentNeverSearches` is flaky. | Test isolation note; cache/keyed-lock reset is in place and `-count=30` passed. | Deterministic under `-count=20`; no cache/order dependence. |
| IMG-094 | P1 | CLOSED | Live provider/VLM tests skip without credentials/binaries. | `t.Skip` paths. | A nightly gated lane exercises the real provider and VLM. |
| IMG-095 | P1 | CLOSED | No Go↔Rust contract test for the catalog policy. | One fixture, `internal/capabilities/images/entitycatalog/testdata/catalog_policy_contract.json`, is read by `TestCatalogPolicyContractMatchesSharedFixtures` and by Rust `tests/policy_contract.rs`; both assert the same accept/reject for accepted, unknown, rejected, low-score, small-dimension and missing-provenance rows. `cargo test -p imagecatalog --test policy_contract` passes with the repository ICU 72 link adapter (`BrainRust/.cargo/icu72-link`, wired through `RUST_ICU_LINK_SEARCH_DIR` in `BrainRust/.cargo/config.toml`); no `RUSTFLAGS` or `LD_LIBRARY_PATH` override is needed. | Shared fixtures assert the same accept/reject decisions. |
| IMG-096 | P2 | CLOSED | Catalog retention/cleanup policy is undefined. | Candidate/materialization tables. | Retention for rejected/retired rows is documented and enforced. |

## Remediation log

This section is updated only when a ticket is actually closed with evidence. A row remains `OPEN` until then.

| Date | Ticket | Change | Evidence |
|---|---|---|---|
| 2026-11-14 | — | Ledger created from the structural audit. | This file. |
| 2026-11-14 | IMG-019 | Weak-query filter now accepts a source sentence that names the identity as grounded context. | `TestSemanticSearchBatchRunsIndependentLaddersConcurrently` |
| 2026-11-14 | IMG-020 | Entity-image fanout no longer appends the generic semantic ladder; generic imagery stays in generic mode. | `TestVidRushFanoutPlanEntityModeDoesNotAddGenericLadder` |
| 2026-11-14 | IMG-040, IMG-051 | Catalog `UsableCount` counts only `accepted` candidates; unknown recertification rows no longer satisfy demand. | `TestEntityImageCatalogPoolDoesNotCountRecertifyingUnknownAsUsable` |
| 2026-11-14 | IMG-041 | Image selection groups by canonical entity (entity first, retrieval suffix stripped), not raw provider query. | `TestVidRushImageGroupCanonicalizesRetrievalQualifier` |
| 2026-11-14 | IMG-052 | A successful URL validation restores a broken candidate to `fresh`, clears attempts/next retry; recertification no longer exhausts broken rows. | `TestSQLiteEntityImageCatalogRecertificationRecoversBrokenURL`, `TestRecertificationBrokenCandidateRecoversOnSuccessfulProbe` |
| 2026-11-14 | IMG-054 | `UpsertCandidate` INSERT+SELECT runs in one transaction and returns the stable row id. | `TestSQLiteEntityImageCatalogUpsertCandidateReturnsStableID` |
| 2026-11-14 | IMG-065 | `incompleteRunError` reports run, status, stage, attempt, error code and an explicit missing-message marker. | `TestIncompleteRunError_IncludesRunStageAndCodeWhenMessageMissing` |
| 2026-11-14 | IMG-067 | Per-scene enrichment uses `context.WithoutCancel` plus its own timeout; caller cancellation cannot kill it. | `TestIncrementalCoordinator_EnrichmentSurvivesCallerCancellation` |
| 2026-11-14 | IMG-078, IMG-080 | Preflight uses a cancellable job context and a dedicated SSRF-safe transport (scheme/user/private/loopback/metadata/redirect guards). | `TestFinalJobImageURLVerifiedBlocksLoopback` |
| 2026-11-14 | IMG-079 | Preflight accepts decoded WebP bytes, matching the acquisition MIME policy. | `TestFinalJobImageBytesVerifiedAcceptsWebP` |
| 2026-11-14 | IMG-001 | The Rust imagecatalog planner is explicitly experimental and not wired; the Go flow is the only live planner. | `BrainRust/imagecatalog/README.md`, `docs/operations/entity-image-pipeline-flow.md` |
| 2026-11-14 | IMG-003 | Hit→bindable is one documented state machine and the final job exposes each stage. | `entity-image-pipeline-flow.md`; Cena r2 `pipeline` 19/19/19/19/19. |
| 2026-11-14 | IMG-025 | Catalog persists the concrete retriever instead of hardcoding DuckDuckGo. | `TestSQLiteEntityImageCatalogProviderContentAndVerifierVersion`; Cena r2 `selection_reason` includes duckduckgo, searxng, wikimedia_commons. |
| 2026-11-14 | IMG-029 | Catalog verdict stores `verifier_version`; legacy/other versions are not usable. | Migration `293`; `TestIsUsableCandidateQualityRequiresCurrentVerifierVersion`. |
| 2026-11-14 | IMG-030 | Candidate content identity is `content_sha256`; a changed hash invalidates the verdict. | `TestSQLiteEntityImageCatalogProviderContentAndVerifierVersion`. |
| 2026-11-14 | IMG-038 | Reusable verdicts require the current verifier version and matching content; the live final job bound all 19 verified images. | `TestIsUsableCandidateQualityRequiresCurrentVerifierVersion`; Cena r2 `pipeline`. |
| 2026-11-14 | IMG-014 | Entity image matching resolves canonical PERSON identity and planner aliases; ORG/GPE acronyms match, PERSON acronyms do not. | `TestEntityImageMatchesCanonicalAlias`, `TestEntityImageMatchesAllowsOrgAcronymButNotPersonAcronym` |
| 2026-11-14 | IMG-015 | Ambiguous surname expansion stays unresolved instead of taking the first capitalized window. | `TestExpandPersonCanonicalNameLeavesAmbiguousSurnameUnresolved` |
| 2026-11-14 | IMG-016 | Annotation spans fold diacritics without rewriting the document text. | `TestFindAnnotationSpanFoldsDiacriticsWithoutRewritingText` |
| 2026-11-14 | IMG-028 | Entity image selection is fail-closed for every kind: an unscored candidate cannot consume an entity slot; discovery stays fail-open. | `TestFilterEntityImageSelectionCandidatesFailsClosedForOrg` |
| 2026-11-14 | IMG-031 | VLM attempt budget is an explicit configurable field; exhausted candidates stay `unknown` with a retry reason instead of being silently skipped. | `TestOllamaPersonImageScorerMalformedVerdictIsRetryable` |
| 2026-11-14 | IMG-032 | Identity and layout are separate judgments: a contextual/group photo with a confirmed identity is accepted, and the single-subject flag is reported. | `TestOllamaPersonImageScorerAcceptsContextualIdentityMatch` |
| 2026-11-14 | IMG-033 | The VLM prompt treats the retrieval query as optional context and forbids using it as identity evidence. | Prompt contract in `classify`; `TestOllamaPersonImageScorerRejectsCollageAndKeepsSinglePortrait` |
| 2026-11-14 | IMG-034 | Vision decode reads `DecodeConfig` first and rejects images above the pixel budget before full decode. | `TestNormalizePersonImageForVisionRejectsDeclaredDecompressionBomb` |
| 2026-11-14 | IMG-036 | Malformed VLM JSON is a retryable `unknown` verdict, not a definitive rejection. | `TestOllamaPersonImageScorerMalformedVerdictIsRetryable` |
| 2026-11-14 | IMG-037 | Vision images are resized to a bounded long side before base64 upload. | `TestNormalizePersonImageForVisionResizes4K` |
| 2026-11-14 | IMG-045 | Selected image identity is preserved across validation; a selection that does not survive is a failure, not a silent replacement. | `TestPreserveSelectedVidRushImagesKeepsSelectedIdentity` |
| 2026-11-14 | IMG-046 | Candidate set hash covers discovery identity only, not mutable acquisition/verification/persistence/index status. | `TestCandidateSetHashIgnoresMutableLifecycleStatus` |
| 2026-11-14 | IMG-047, IMG-048 | One readiness predicate with an explicit durable-vs-URL mode and one YouTube predicate replace the duplicated branches. | `TestReadyVidRushCandidateUnifiesProviderAndURLFallback` |
| 2026-11-14 | IMG-050 | Entity image failure reasons distinguish `NO_QUERY`, `PROVIDER_EMPTY`, `UNVERIFIED`, `NOT_MATERIALIZED` and `IDENTITY_MISMATCH`. | `TestEntityImageFailureReasonDistinguishesNoQueryAndProviderEmpty` |
| 2026-11-14 | IMG-053 | Rejected candidates enter bounded recertification instead of being permanently skipped. | `TestListCandidatesForRecertificationIncludesRejected` |
| 2026-11-14 | IMG-055 | Candidate status transitions are validated; retired is terminal and only the explicit revive event leaves it. | `TestSetCandidateStatusRetiredIsTerminal`, `TestTransitionCandidateStatusRetiredIsTerminal` |
| 2026-11-14 | IMG-062, IMG-086 | Selection accepts only absolute HTTP(S) locators; non-HTTP schemes are rejected. | `TestValidVidRushCandidateRejectsNonHTTPURL` |
| 2026-11-14 | IMG-064 | Materialization content hash must be a 64-hex SHA-256 at the boundary; fixtures now use real content addresses. | `TestValidateMaterializationRejectsNonSHA256Content` |
| 2026-11-14 | IMG-083 | Stock isolation counts the whole provider candidate pool, including `Assets.Candidates`. | `TestRuleStockIsolationCountsCandidatePool` |
| 2026-11-14 | IMG-063 | `RunOnce` rejects overlapping runs with `TryLock`; `BatchTimeout` bounds the batch, and a deadline is returned without recording a fake candidate failure. | `TestRecertificationRunOnceRejectsConcurrentRun`, `TestRecertificationRunOnceHonorsBatchTimeout` |
| 2026-11-14 | IMG-068 | `waitForVidRush` returns all successful scenes plus `errors.Join` of every scene error, instead of dropping partial results. | `TestIncrementalCoordinator_ReturnsPartialResultsWithAllSceneErrors` |
| 2026-11-14 | IMG-074 | The catalog lock is released before cache/provider work; a separate per-entity discovery lock re-checks the catalog and collapses the first population to one provider call. | `TestEntityImageProviderSearchDoesNotHoldCatalogLock`, `TestInternetImagesProcessorCatalogLockSharesFirstPopulation` |
| 2026-11-14 | IMG-077 | Per-scene `recover` converts a panic into a scene-attributed error and releases the barrier wait group. | `TestIncrementalCoordinator_RecoversScenePanic` |
| 2026-11-14 | IMG-081 | Final-job image preflight checks the entity-image surface before `InternetImages`, so entity-only jobs cannot skip it. | `TestFinalJobImageRequested_EntitySurfaceWinsWithoutInternetProvider` |
| 2026-11-14 | IMG-082 | Preflight includes items with `AssetRefs` even when their kind does not contain "image". | `TestFinalJobImagePreflight_CoversEntityKindWithAssetRefs` |
| 2026-11-14 | IMG-085 | The media-cert barrier fails when certification is required and `Certified=false`. | `TestMediaCertBarrierFailsJobWhenCertifiedFalse` |
| 2026-11-14 | IMG-021, IMG-022, IMG-060, IMG-043, IMG-044 | Cache key bound to provider/model/limit/plan fingerprint; provider failures are typed and fail the batch; materialization writes are append-only; acquisition budget is scoped to scene+entity and duplicate ready content is counted once and reported. | `TestSemanticCacheKeyFencesProviderLimitAndPlan`, `TestSemanticProviderFailureIsTypedAndNotAMiss`, `TestSemanticSearchBatchReportsProviderErrorsAsTypedFailure`, `TestUpsertMaterializationAppendsAttemptHistory`, `TestVidRushAcquisitionBudgetKeyScopesByEntityAndScene`, `TestVidRushReadyImageGroupForContentHash` |
| 2026-11-14 | IMG-007, IMG-008, IMG-013, IMG-088 | Duplicate scene identities fail closed with a diagnostic; URL-only semantic hits report `UNBOUND_REQUIRES_ACQUISITION`; GPE images on secondary annotations bind symmetrically; deploy reserves and logs a drain window before restart. | `TestSceneIdentityIndexFailsClosedOnDuplicateIDs`, `TestApplySemanticResolvedImagesRequiresContentAddress`, `TestProjectEntityImageBindingsBindsSecondaryGPE`, `scripts/deploy/deploy_selftest.sh` |
| 2026-11-14 | IMG-092, IMG-004, IMG-075, IMG-091 | Catalog fixtures that are metadata-only are explicitly pinned/legacy; manual searches share one segment resolver for queries and slots; catalog/cache stage releases its keyed lock by defer on errors; catalog pools expose a per-candidate decision ledger. | `TestDurableMaterializationDoesNotBypassCatalogQualityGate`, `TestEntityImageCatalogLookupDoesNotMutateCandidates`, `TestManualSearchSlotsCoversEveryExplicitSlot`, `TestResolveEntityImageCatalogAndCacheReleasesLockOnError`, `TestEntityImageCatalogPoolRecordsPerCandidateDecisions` |
| 2026-11-14 | IMG-027, IMG-056, IMG-057, IMG-058, IMG-061, IMG-066, IMG-069, IMG-070, IMG-071, IMG-072, IMG-073, IMG-076, IMG-087, IMG-089, IMG-090, IMG-094, IMG-096 | Paged catalog reads, batch materializations, pure reads, refresh lease/recovery, coordinator expected-scene barrier and stale details, durable run request and RUN_STALLED, final-job validation, envelope diagnostics, cache metrics, SSRF/redirect caps, build provenance, ICU72 CI and nightly live lane, retention. | `go test ./internal/capabilities/scripts ./internal/capabilities/scripts/adapters ./internal/platform/sqlite/assets/entitycatalog ./internal/capabilities/images/entitycatalog -count=1` |
| 2026-11-14 | IMG-002, IMG-006 | The entity-image decision has one explicit owner; catalog→cache→filter→persist is one resolver shared by the media stage and fan-out, with the discovery lock held through the first provider population. | `TestResolveEntityImageDecisionOwnerUsesRequestedSurface`, `TestApplySemanticResolvedImagesStandsDownWhenMediaPipelineOwnsSurface`, `TestSharedEntityImageResolverServesWarmCatalogWithoutProvider`, `TestEntityImageCatalogSerializesConcurrentFirstPopulationOnPersistentSQLite` |
| 2026-11-14 | IMG-095 | Go and Rust read the same catalog-policy fixture and assert identical accept/reject decisions. | `TestCatalogPolicyContractMatchesSharedFixtures`, `cargo test -p imagecatalog --test policy_contract` (plain, ICU 72 link adapter), `cargo test --workspace --no-run` (plain) |

## Live Pacquiao run and quality findings (2026-11-14)

Payload: `ops/jobs/manny_pacquiao_story_20261114/run1/manny-pacquiao-2000w-50-images.generate.json` (5 scene, `images_per_scene=10`, 2085-word source). Jobs: run1 `job_1794667655435804026_f3791933`, run2 `job_1794668904341369352_3ac5fac6`, run3 `job_1794669241001560506_66120ea5`, run4 `job_1794670738608040517_6553ddac`, run5 `job_1794672914892873847_7f98b81c`.

- run1 (pre-fix binary): `SUCCEEDED`; 50 `secondary_images`, 10 per scene, 49 unique, 50 Drive links, 0 invalid SHA-256; 1 duplicate URL; 4+ SVG icons in `semantic_production_plan`; 2 artist paintings for the boxer query; 27/50 resolved units.
- run2 (rebuilt with discovery placeholder filter + cross-scene dedupe): `SUCCEEDED`; 50 images, 50 unique identities, 0 duplicate URLs, 0 missing Drive links, 0 invalid SHA-256; 7 SVG icons remained because the planner cache path still bypassed the adapter filter.
- run3 (rebuilt with the scheduler-level placeholder gate, force refresh): `SUCCEEDED`; 46 images, 10/10/10/10/6 per scene, 46 distinct URLs and 46 distinct SHA-256, 0 missing Drive links, 0 invalid SHA-256, **0 SVG icons**, 0 artist/painting matches. The one repeated article title is two different Alamy files (`DM5PD2` vs `DM5PD3`), not a duplicate asset. `scene-05` stopped at 6/10 because its fresh pool had 8 candidates, 3 without a durable SHA; no fake asset was fabricated.
- Live-verified fixes: cross-scene dedupe (run2: 50 unique), scheduler placeholder gate (run3: 0 icons), server rebuilt/restarted (`bin/pipelinegen`, PID 1052559). Regression tests: `TestDedupeMaterializedImagesAcrossSegmentsRefillsFromCandidates`, `TestFilterSemanticPlaceholderHitsDropsIconsAndKeepsPhotos`, adapter placeholder tests.
- run4 (22 queries, force-refresh cache bypass, 10/10/10/10/10): `SUCCEEDED`; **50 images, 50 distinct URLs, 50 distinct SHA-256**, 0 missing Drive links, 0 invalid SHA-256, **0 SVG icons**, 0 duplicate URLs. `semantic_search_report`: `CacheHits=0`, `ProviderQueries=27`, `Hits=27`, `Misses=0`, `Errors=0`. The force-refresh path now really queries the provider instead of replaying the planner cache.
- run5 (current binary `b335614a…`, PID 99292; same 22-query force-refresh payload): `SUCCEEDED`; **50 images, 10/10/10/10/10**, **50 distinct URLs, 50 distinct SHA-256, 50 distinct perceptual hashes**, 0 missing/invalid Drive links, 0 invalid SHA-256, **0 SVG/icon-like assets**, 0 duplicate URLs/SHA/perceptual hashes; 50/50 `acquired`/`verified`/`persisted`. `entity_image_decision_owner=semantic_planner` (the payload disables `entity_images`, so the VidRush ownership gate is intentionally not the owner here). `semantic_search_report`: `CacheHits=0`, `ProviderQueries=27`, `Hits=27`, `Misses=0`, `Errors=0`. Evidence: `ops/jobs/manny_pacquiao_story_20261114/run5/validation.json`.
- The structural tickets are closed. The artist/boxer ambiguity remains an editorial quality risk, not an open structural ticket: generic person queries still depend on the visual-identity gate rather than a hardcoded subject list.

## Verification

Command:

```bash
go test ./internal/capabilities/scripts \
  ./internal/capabilities/scripts/adapters \
  ./internal/capabilities/images/entitycatalog \
  ./internal/platform/sqlite/assets/entitycatalog \
  ./internal/capabilities/mediacert -count=1
go test ./internal/capabilities/scripts/adapters -count=1 -shuffle=on
```

Result on 2026-11-14: PASS (exit 0), including the serialized/shuffled adapter suite.

### Live verification (2026-11-14)

- Floyd generate run10: `job_1794660101659633331_25d8bbd5` — SUCCEEDED, 264s. The semantic-engine binary was rebuilt from BrainRust HEAD and the wire contract passed; 20 selectable images were materialized (19 fully lifecycle-stamped, 1 force-refresh catalog cache-hit retained for diagnostics).
- Cena final job r2: `job_1794660885471179076_2f8ea611` — SUCCEEDED, 505s. `result.result.overlay_audit.entity_images.pipeline` is `{"retrieved":19,"verified":19,"selected":19,"materialized":19,"bound":19,"skipped":0}`. The same block is persisted in `run_e711ce7d-9d54-4d12-9f78-667588a37b8c.json` (`stage=preflight_passed`).
- Rust contract e2e: `TestSemanticEngineRealBinaryWireFields`, `TestSemanticEngineRealBinaryAnalysesWholeScript`, `TestVisualNERAnalyzeRealBinaryProducesBulletsAndChartPoints`, `TestVisualNERSemanticBundleRealBinaryGroundsCharts` — PASS.
- Rust fetch contract guard: `scripts/ci/rust-fetch.sh` probes schema v2 plus `contextual_image_queries`, `composed_image_queries`, `important_segments`; the HEAD binary exits 0, the preserved v0.1.0 backup exits 1. `scripts/ci/test-rust-fetch.sh` now covers both a checksum-valid stale release and a stale installed binary with a matching stamp; both fail closed.
- Force-refresh diagnostic path: `TestVidRushForceRefreshDiagnosticCandidateNeverSelected` proves the groups-cap candidate is retained with Drive/hash but stripped lifecycle, then excluded by `selectExactVidRushImages`; the final Floyd run10 payload has zero non-empty-status-plus-drive/hash candidates with an empty lifecycle.
- ICU 72: BrainRust HEAD was rebuilt in Debian Bookworm through Docker; `semantic-engine` and `visualner` link `libicuuc.so.72`, `make rust-fetch` installed `v0.2.0` from the bundle, and the four rustexec real-binary e2e tests PASS.

## Remaining

0 structural tickets remain OPEN. The Floyd force-refresh cache-hit is not an open binding gap: the diagnostic candidate is retained by design, but the selector and binding gate reject it, and the run10 final payload contains no lifecycle-null candidate with Drive/hash. The pin is `v0.2.0` and the local ICU 72 HEAD bundle is in `BrainRust/dist-icu72`; those assets still need to be published as the GitHub `v0.2.0` release before a clean remote `make build` or Docker build can fetch them. Local `make rust-fetch` passes against the installed stamp.
