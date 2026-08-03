# Registry-backed component targets.  Keep these recipes thin: the Python
# runners own package selection, dependency resolution, deduplication,
# timeout enforcement, and reports.  No component target invokes verify-fast.

PYTHON ?= python3
VERIFY_COMPONENT_RUNNER ?= $(PYTHON) scripts/ci/verify-component.py
VERIFY_ALL_COMPONENTS_RUNNER ?= $(PYTHON) scripts/ci/verify-all-components.py
VERIFY_CHANGED_COMPONENTS_RUNNER ?= $(PYTHON) scripts/ci/verify-changed-components.py
VERIFY_COMPONENT_FLAGS ?=
VERIFY_CHANGED_COMPONENTS_FLAGS ?=
VERIFY_COMPONENT_COVERAGE_RUNNER ?= $(PYTHON) scripts/ci/verify-component-coverage.py

verify-component-coverage:
	@$(VERIFY_COMPONENT_COVERAGE_RUNNER) --report artifacts/verify/component-coverage.json

verify-changed-components:
	@$(VERIFY_CHANGED_COMPONENTS_RUNNER) $(VERIFY_CHANGED_COMPONENTS_FLAGS)

verify-components:
	@$(VERIFY_ALL_COMPONENTS_RUNNER) --mode fast $(VERIFY_COMPONENT_FLAGS)

verify-race-components:
	@$(VERIFY_ALL_COMPONENTS_RUNNER) --mode race $(VERIFY_COMPONENT_FLAGS)

verify-script:
	@$(VERIFY_COMPONENT_RUNNER) script $(VERIFY_COMPONENT_FLAGS)

verify-research:
	@$(VERIFY_COMPONENT_RUNNER) research $(VERIFY_COMPONENT_FLAGS)

verify-clips:
	@$(VERIFY_COMPONENT_RUNNER) clips $(VERIFY_COMPONENT_FLAGS)

verify-stock:
	@$(VERIFY_COMPONENT_RUNNER) stock $(VERIFY_COMPONENT_FLAGS)

verify-qdrant:
	@$(VERIFY_COMPONENT_RUNNER) qdrant $(VERIFY_COMPONENT_FLAGS)

verify-indexing:
	@$(VERIFY_COMPONENT_RUNNER) indexing $(VERIFY_COMPONENT_FLAGS)

verify-drive:
	@$(VERIFY_COMPONENT_RUNNER) drive $(VERIFY_COMPONENT_FLAGS)

verify-docs:
	@$(VERIFY_COMPONENT_RUNNER) docs $(VERIFY_COMPONENT_FLAGS)

verify-voiceover:
	@$(VERIFY_COMPONENT_RUNNER) voiceover $(VERIFY_COMPONENT_FLAGS)

verify-images:
	@$(VERIFY_COMPONENT_RUNNER) images $(VERIFY_COMPONENT_FLAGS)

verify-translation:
	@$(VERIFY_COMPONENT_RUNNER) translation $(VERIFY_COMPONENT_FLAGS)

verify-timeline:
	@$(VERIFY_COMPONENT_RUNNER) timeline $(VERIFY_COMPONENT_FLAGS)

verify-storage:
	@$(VERIFY_COMPONENT_RUNNER) storage $(VERIFY_COMPONENT_FLAGS)

verify-database:
	@$(VERIFY_COMPONENT_RUNNER) database $(VERIFY_COMPONENT_FLAGS)

verify-jobs:
	@$(VERIFY_COMPONENT_RUNNER) jobs $(VERIFY_COMPONENT_FLAGS)

verify-api:
	@$(VERIFY_COMPONENT_RUNNER) api $(VERIFY_COMPONENT_FLAGS)

verify-ollama:
	@$(VERIFY_COMPONENT_RUNNER) ollama $(VERIFY_COMPONENT_FLAGS)

verify-youtube:
	@$(VERIFY_COMPONENT_RUNNER) youtube $(VERIFY_COMPONENT_FLAGS)

verify-artlist:
	@$(VERIFY_COMPONENT_RUNNER) artlist $(VERIFY_COMPONENT_FLAGS)

verify-node-scraper:
	@$(VERIFY_COMPONENT_RUNNER) node-scraper $(VERIFY_COMPONENT_FLAGS)

verify-kernel:
	@$(VERIFY_COMPONENT_RUNNER) kernel $(VERIFY_COMPONENT_FLAGS)

# Race is opt-in per component.  These aliases use the same resolver and only
# differ in mode, so a race run cannot silently fall back to the fast suite.
verify-race-script:
	@$(VERIFY_COMPONENT_RUNNER) script --race $(VERIFY_COMPONENT_FLAGS)

verify-race-research:
	@$(VERIFY_COMPONENT_RUNNER) research --race $(VERIFY_COMPONENT_FLAGS)

verify-race-clips:
	@$(VERIFY_COMPONENT_RUNNER) clips --race $(VERIFY_COMPONENT_FLAGS)

verify-race-stock:
	@$(VERIFY_COMPONENT_RUNNER) stock --race $(VERIFY_COMPONENT_FLAGS)

verify-race-qdrant:
	@$(VERIFY_COMPONENT_RUNNER) qdrant --race $(VERIFY_COMPONENT_FLAGS)

verify-race-indexing:
	@$(VERIFY_COMPONENT_RUNNER) indexing --race $(VERIFY_COMPONENT_FLAGS)

verify-race-drive:
	@$(VERIFY_COMPONENT_RUNNER) drive --race $(VERIFY_COMPONENT_FLAGS)

verify-race-docs:
	@$(VERIFY_COMPONENT_RUNNER) docs --race $(VERIFY_COMPONENT_FLAGS)

verify-race-voiceover:
	@$(VERIFY_COMPONENT_RUNNER) voiceover --race $(VERIFY_COMPONENT_FLAGS)

verify-race-images:
	@$(VERIFY_COMPONENT_RUNNER) images --race $(VERIFY_COMPONENT_FLAGS)

verify-race-translation:
	@$(VERIFY_COMPONENT_RUNNER) translation --race $(VERIFY_COMPONENT_FLAGS)

verify-race-timeline:
	@$(VERIFY_COMPONENT_RUNNER) timeline --race $(VERIFY_COMPONENT_FLAGS)

verify-race-storage:
	@$(VERIFY_COMPONENT_RUNNER) storage --race $(VERIFY_COMPONENT_FLAGS)

verify-race-database:
	@$(VERIFY_COMPONENT_RUNNER) database --race $(VERIFY_COMPONENT_FLAGS)

verify-race-jobs:
	@$(VERIFY_COMPONENT_RUNNER) jobs --race $(VERIFY_COMPONENT_FLAGS)

verify-race-api:
	@$(VERIFY_COMPONENT_RUNNER) api --race $(VERIFY_COMPONENT_FLAGS)

verify-race-ollama:
	@$(VERIFY_COMPONENT_RUNNER) ollama --race $(VERIFY_COMPONENT_FLAGS)

verify-race-youtube:
	@$(VERIFY_COMPONENT_RUNNER) youtube --race $(VERIFY_COMPONENT_FLAGS)

verify-race-artlist:
	@$(VERIFY_COMPONENT_RUNNER) artlist --race $(VERIFY_COMPONENT_FLAGS)

verify-race-node-scraper:
	@$(VERIFY_COMPONENT_RUNNER) node-scraper --race $(VERIFY_COMPONENT_FLAGS)

verify-race-kernel:
	@$(VERIFY_COMPONENT_RUNNER) kernel --race $(VERIFY_COMPONENT_FLAGS)
