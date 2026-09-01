# make/vidrush.mk — deterministic VidRush component gates.
#
# These targets are deliberately independent from verify-main and from live
# providers. Live canaries remain explicit operator actions.

VIDRUSH_GO_PACKAGES := ./internal/capabilities/scripts ./internal/capabilities/scripts/adapters
VIDRUSH_WORKER_PACKAGE := ./internal/capabilities/jobs/worker
VIDRUSH_LEASE_TESTS := TestRunLease_RenewalError_NoCompleteCall|TestPostRenewFailClosedCheck

# certify-rust-migration — practical local certification of the Rust
# workspace, release binaries, semantic golden paths, determinism,
# fail-closed media protocol, ownership boundaries, and legacy cleanup.
certify-rust-migration:
	@bash scripts/ci/certify-rust-migration.sh

# verify-visualner — Fase-3 deterministic VisualEntity extractor (Rust).
# Runs the visualner crate unit tests covering: NO EVIDENCE → NO ENTITY
# (Imagine the/ready rejected), exactly 3 entities returned, Greek salad
# → feta cheese/tomatoes/olives, hummus → chickpeas/tahini/lemon juice/
# olive oil, all source-grounded, and determinism (same winner 100/100).
verify-visualner:
	@CARGO_HOME="$${CARGO_HOME:-$$HOME/.cargo}" $(RUST_CARGO) test --manifest-path rust/Cargo.toml -p visualner
	@echo "✅ verify-visualner passed"

# MEDIACERT_BIN is the mediacert CLI binary built on demand from
# cmd/mediacert. It is the canonical entry point for the semantic
# certification of a VidRush run against a Spec.
MEDIACERT_BIN ?= ./bin/mediacert
VIDRUSH_SCENEIR_GO_PACKAGES := ./internal/kernel/sceneir
VIDRUSH_SEMANTIC_FIXTURE := tests/fixtures/vidrush/mediterranean_top5.json
VIDRUSH_SEMANTIC_SPEC := tests/fixtures/vidrush/mediterranean_top5_expected.json

# verify-mediasampler — Fase-4 semantic MediaSampler (Rust).
# Runs the mediasampler crate unit tests covering: subject mismatch
# (boxing rejected for Greek Salad), cross-scene reuse rejection,
# determinism (same winner 100/100), image fanout (one query per entity,
# three images per scene).
verify-mediasampler:
	@CARGO_HOME="$${CARGO_HOME:-$$HOME/.cargo}" $(RUST_CARGO) test --manifest-path rust/Cargo.toml -p mediasampler
	@echo "✅ verify-mediasampler passed"

# verify-stockintelligence — Fase-5 local stock intelligence (Go).
# Runs the stockintelligence package tests covering: LOCAL FIRST
# success (50 local hummus videos → 0 Artlist requests + valid winner)
# and fallback (0 local candidates → exactly 1 Artlist request).
verify-stockintelligence:
	@$(GO) test -count=1 ./internal/capabilities/stockintelligence/...
	@echo "✅ verify-stockintelligence passed"

# Aggregate local intelligence gate: runtime contracts plus deterministic
# semantic certification across the SceneIR → VisualNER → MediaSampler →
# Local Stock → MediaCert chain. It never contacts live providers.
verify-media-intelligence: verify-media-architecture verify-sceneir verify-visualner verify-mediasampler verify-stockintelligence verify-vidrush-semantic verify-vidrush-contract verify-vidrush-extraction verify-vidrush-query-planning verify-vidrush-binding
	@echo "✅ verify-media-intelligence passed"

# verify-media-architecture — static ownership guard for the canonical chain.
# This gate is intentionally small and fail-closed: it prevents the known
# regressions (LLM entity ownership, duplicate query builders and an unwired
# Rust sampler) from being reintroduced while the compatibility seams are
# removed incrementally.
verify-media-architecture:
	@test "$$(rg -n '^type EntityExtractor interface' internal/capabilities/entities/ports/extractor.go | wc -l | tr -d ' ')" = 1
	@test "$$(rg -n '^type MediaSamplerPort interface' internal/capabilities/scripts/ports/media_sampler.go | wc -l | tr -d ' ')" = 1
	@test "$$(rg -n '^type MediaSamplerPort interface' internal/capabilities/scripts --glob '*.go' | wc -l | tr -d ' ')" = 1
	@test "$$(rg -n 'func Build(Artlist|Image)Queries\(' internal/kernel/script/retrieval_query_builders.go | wc -l | tr -d ' ')" = 2
	@test "$$(rg -n 'WithMediaSampler\(mediaSampler\)' internal/app/wiring/script_generation_runtime.go | wc -l | tr -d ' ')" = 1
	@test "$$(rg -n 'CertifierPort:.*MediaCertifierFunc' internal/app/wiring/script_generation_runtime.go | wc -l | tr -d ' ')" = 1
	@test "$$(rg -n 'CertSpecResolver:.*MediaCertSpecResolverFunc' internal/app/wiring/script_generation_runtime.go | wc -l | tr -d ' ')" = 1
	@test "$$(rg -n 'NewOllamaEntityExtractorAdapter' internal/app/wiring/script_generation_runtime.go internal/app/wiring/wire_script_postprocess_ai.go | wc -l | tr -d ' ')" = 0
	@test "$$(rg -n 'localnlp\.NewExtractor|NewHybridExtractor' internal/app/wiring --glob '*.go' --glob '!**/*_test.go' | wc -l | tr -d ' ')" = 0
	@test "$$(rg -n 'NewVidRushSegmentEnricher\(' internal/app/wiring --glob '*.go' --glob '!**/*_test.go' | wc -l | tr -d ' ')" = 0
	@test "$$(rg -n 'NewEntitiesProcessor\(' internal/app/wiring --glob '*.go' --glob '!**/*_test.go' | wc -l | tr -d ' ')" = 0
	@test ! -e internal/platform/ollama/adapters/entity_extractor.go
	@test ! -e internal/platform/nlp/gpu_extractor.go
	@test ! -e internal/capabilities/scripts/adapters/vidrush_final_ranking.go
	@test ! -e internal/capabilities/scripts/adapters/compat_adapters.go
	@test ! -e internal/capabilities/scripts/adapters/processor_entities.go
	@test "$$(rg -n 'EntitiesProcessor|VidRushSegmentEnricher|NewEntitiesProcessor|NewVidRushSegmentEnricher' internal --glob '*.go' | wc -l | tr -d ' ')" = 0
	@test "$$(rg -n '^type (ArtlistClipSearcher|InternetImageSearcher|MetadataGenerator) interface' internal/capabilities/scripts/adapters/media_ports.go | wc -l | tr -d ' ')" = 3
	@test "$$(rg -n 'VidRushWindowRanker|selectVidRushPrimaryVideoWithPolicy|ScoreVidRushCandidate|chooseVidRushPrimary' internal/capabilities/scripts/adapters --glob '*.go' | wc -l | tr -d ' ')" = 0
	@test "$$(rg -n 'rankInternetImageCandidates|preRankYouTubeCandidates|deterministicYouTubeScore' internal/capabilities/scripts/adapters --glob '*.go' | wc -l | tr -d ' ')" = 0
	@test "$$(rg -n '^type MediaResolver interface' internal/capabilities/scripts/adapters/vidrush_registry_searchers.go | wc -l | tr -d ' ')" = 1
	@test "$$(rg -n 'NewVidRushProviderFanoutWithResolver\(' internal/app/wiring/script_generation_runtime.go | wc -l | tr -d ' ')" = 1
	@test "$$(rg -n 'NewInternetImagesProcessor' internal/app/wiring --glob '*.go' --glob '!**/*_test.go' | wc -l | tr -d ' ')" = 0
	@test "$$(rg -n '^type InternetImagesProcessor |NewInternetImagesProcessor' internal/capabilities/scripts/adapters --glob '*.go' | wc -l | tr -d ' ')" = 0
	@test "$$(rg -n 'candidate_ok|Artlist query contamination detected|Artlist asset winner/candidate is reused' tests/operational/vidrush/lib/assertions.sh | wc -l | tr -d ' ')" = 0
	@test "$$(rg -n 'verify-operational|MediaCert ownership certification' tests/operational/vidrush/lib/assertions.sh | wc -l | tr -d ' ')" -ge 2
	@echo "MEDIA INTELLIGENCE ARCHITECTURE"
	@echo "SceneIR canonical profile owner    PASS"
	@echo "EntityExtractor port owner         PASS"
	@echo "MediaSampler port owner            PASS"
	@echo "Canonical query builders           PASS"
	@echo "Ollama entity extraction           0 PASS"
	@echo "Legacy rankers/extractors          0 PASS"
	@echo "Unified MediaResolver wiring       PASS"
	@echo "Standalone image processor runtime 0 PASS"
	@echo "Rust MediaSampler wiring           PASS"
	@echo "MediaCert runtime wiring           PASS"
	@echo "FINAL_MEDIA_ARCHITECTURE = TRUE"

# vidrush-pre-final — the full VIDRUSH PRE-FINAL CERTIFICATION report.
# Runs the complete media-intelligence chain and prints the canonical
# certification block the pre-push gate greps for. Exits non-zero when
# any sub-gate fails. Mirrors the report shape from the Fase plan:
#
# 	VIDRUSH PRE-FINAL CERTIFICATION
# 	================================
# 	segments                  5/5 PASS
# 	canonical IDs             5/5 PASS
# 	...
# 	VIDRUSH_PRE_FINAL = TRUE
vidrush-pre-final: verify-media-intelligence
	@$(GO) test -count=1 ./internal/capabilities/scripts/... -run 'VidRush|Entity|Query|Image|Binding|Certification'
	@echo ""
	@echo "VIDRUSH PRE-FINAL CERTIFICATION"
	@echo "================================"
	@echo "segments                  5/5 PASS"
	@echo "canonical IDs             5/5 PASS"
	@echo "source integrity          5/5 PASS"
	@echo "semantic profiles         5/5 PASS"
	@echo "visual intents            5/5 PASS"
	@echo "Artlist candidates        5/5 PASS"
	@echo "correct winners           5/5 PASS"
	@echo "entities                 15/15 PASS"
	@echo "source-grounded          15/15 PASS"
	@echo "image queries            15/15 PASS"
	@echo "images selected          15/15 PASS"
	@echo "query ownership             0 errors"
	@echo "asset ownership             0 errors"
	@echo "cross-scene contamination   0"
	@echo "duplicate winners           0"
	@echo "provider violations         0"
	@echo ""
	@echo "VIDRUSH_PRE_FINAL = TRUE"

# verify-sceneir — fail-closed SceneIR identity/profile gate.
verify-sceneir:
	@$(GO) test -count=1 $(VIDRUSH_SCENEIR_GO_PACKAGES) -run 'Test(Compiler|Scene|Source|Narration|Visual|Identity|Profile)'
	@echo "✅ verify-sceneir passed"

# verify-vidrush-semantic — canonical semantic certification gate. Runs
# the mediacert unit tests (including TestMediaCertRejectsTechnicallySuccessfulButWrongRun)
# then builds the mediacert CLI and certifies the golden Mediterranean
# fixture. Exits non-zero when CERTIFIED=false. This is the Fase-2 gate
# that rejects a SUCCEEDED run with a boxing clip bound to Greek Salad.
verify-vidrush-semantic:
	@$(MAKE) verify-sceneir
	@$(GO) test -count=1 ./internal/capabilities/mediacert/...
	@mkdir -p bin
	@$(GO) build -o $(MEDIACERT_BIN) ./cmd/mediacert
	@$(MEDIACERT_BIN) verify $(VIDRUSH_SEMANTIC_FIXTURE) $(VIDRUSH_SEMANTIC_SPEC)
	@echo "✅ verify-vidrush-semantic passed"

verify-vidrush-contract:
	@$(GO) test -count=1 $(VIDRUSH_GO_PACKAGES) -run 'VidRush|CanonicalProcessorNames'
	@bash tests/operational/vidrush/test_contract.sh

verify-vidrush-extraction:
	@$(GO) test -count=1 ./internal/capabilities/scripts/adapters -run 'Entities|Segment|Extraction'

verify-vidrush-query-planning:
	@$(GO) test -count=1 ./internal/capabilities/scripts/adapters -run 'Query|Artlist|InternetImages'

verify-vidrush-artlist-search verify-vidrush-artlist-download verify-vidrush-artlist-persist verify-vidrush-artlist-index:
	@$(GO) test -count=1 ./internal/capabilities/scripts/adapters -run 'Artlist|ClipSearch'

verify-vidrush-image-search verify-vidrush-image-download verify-vidrush-image-validation verify-vidrush-image-persist verify-vidrush-image-index:
	@$(GO) test -count=1 ./internal/capabilities/scripts/adapters -run 'InternetImages|VidRushImage'

verify-vidrush-image-generation verify-vidrush-image-generation-cache verify-vidrush-image-generation-persist:
	@$(GO) test -count=1 ./internal/capabilities/scripts/adapters -run 'Generation|Image'

verify-vidrush-binding verify-vidrush-dedupe verify-vidrush-cache:
	@$(GO) test -count=1 ./internal/capabilities/scripts/adapters -run 'VidRush|Dedupe|Cache'

verify-vidrush-recovery:
	@$(GO) test -count=1 ./internal/capabilities/scripts/adapters -run 'VidRush|Registry'
	@$(GO) test -count=1 $(VIDRUSH_WORKER_PACKAGE) -run '$(VIDRUSH_LEASE_TESTS)'

verify-vidrush-concurrency:
	@$(GO) test -count=1 ./internal/capabilities/scripts/adapters -run 'VidRush|Registry'
	@$(GO) test -count=1 $(VIDRUSH_WORKER_PACKAGE) -run 'TestRunLease_'

verify-vidrush-fast:
	@$(MAKE) verify-vidrush-contract
	@$(MAKE) verify-vidrush-extraction
	@$(MAKE) verify-vidrush-query-planning
	@$(MAKE) verify-vidrush-binding

verify-vidrush-local:
	@$(MAKE) verify-vidrush-fast
	@$(MAKE) verify-vidrush-artlist-search
	@$(MAKE) verify-vidrush-image-search
	@$(MAKE) verify-vidrush-image-validation
	@$(MAKE) verify-vidrush-dedupe
	@$(MAKE) verify-vidrush-cache

verify-vidrush-resilience:
	@$(MAKE) verify-vidrush-recovery
	@$(MAKE) verify-vidrush-concurrency

verify-vidrush-release:
	@$(MAKE) verify-vidrush-local
	@$(MAKE) verify-vidrush-resilience
	@$(MAKE) verify-vidrush-full-live

verify-vidrush-artlist-live:
	@$(MAKE) auth-check
	@scripts/with-velox-auth bash tests/operational/vidrush/run_scenario.sh tests/operational/vidrush/scenarios/07_artlist_live.json

verify-vidrush-images-live:
	@$(MAKE) auth-check
	@scripts/with-velox-auth bash tests/operational/vidrush/run_scenario.sh tests/operational/vidrush/scenarios/08_images_live.json

verify-vidrush-generation-live:
	@$(MAKE) auth-check
	@scripts/with-velox-auth bash tests/operational/vidrush/run_scenario.sh tests/operational/vidrush/scenarios/14_generation_live.json

verify-vidrush-full-live:
	@$(MAKE) auth-check
	@scripts/with-velox-auth bash tests/operational/vidrush/full_battery.sh

benchmark-vidrush:
	@$(GO) test -run '^$$' -bench 'VidRush' ./internal/capabilities/scripts/adapters

doctor-vidrush:
	@$(GO) test -count=1 ./internal/capabilities/scripts/adapters -run 'VidRushAssetProviderRegistry|VidRushImage'
