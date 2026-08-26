# make/vidrush.mk — deterministic VidRush component gates.
#
# These targets are deliberately independent from verify-main and from live
# providers. Live canaries remain explicit operator actions.

VIDRUSH_GO_PACKAGES := ./internal/capabilities/scripts ./internal/capabilities/scripts/adapters
VIDRUSH_WORKER_PACKAGE := ./internal/capabilities/jobs/worker
VIDRUSH_LEASE_TESTS := TestRunLease_RenewalError_NoCompleteCall|TestPostRenewFailClosedCheck

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
