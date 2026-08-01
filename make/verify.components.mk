# make/verify.components.mk - component verification target aliases.
#
# Keep component targets declarative and thin. The shared Python runner owns
# registry loading, dependency resolution, command deduplication, timeouts,
# modes, and JSON reports.

PYTHON ?= python3
VERIFY_COMPONENT_RUNNER ?= $(PYTHON) scripts/ci/verify-component.py
VERIFY_COMPONENT_FLAGS ?=
VERIFY_CHANGED_COMPONENTS_RUNNER ?= $(PYTHON) scripts/ci/verify-changed-components.py
# Aggregate gates fail closed at the component level while still covering
# the current working tree when the registry has not yet claimed every path.
# Direct CLI users retain strict unmapped-file behavior unless they opt in.
VERIFY_CHANGED_COMPONENTS_FLAGS ?= --run-all-when-unmapped

verify-changed-components:
	@$(VERIFY_CHANGED_COMPONENTS_RUNNER) $(VERIFY_CHANGED_COMPONENTS_FLAGS)

verify-components:
	@$(VERIFY_COMPONENT_RUNNER) --all $(VERIFY_COMPONENT_FLAGS)

verify-race-components:
	@$(VERIFY_COMPONENT_RUNNER) --all --race $(VERIFY_COMPONENT_FLAGS)

verify-script:
	@$(VERIFY_COMPONENT_RUNNER) script $(VERIFY_COMPONENT_FLAGS)

# Preserve the historical race-tested behavior of verify-stock while routing
# execution through the shared component runner.
verify-stock:
	@$(VERIFY_COMPONENT_RUNNER) stock --race $(VERIFY_COMPONENT_FLAGS)

verify-clips:
	@$(VERIFY_COMPONENT_RUNNER) clips $(VERIFY_COMPONENT_FLAGS)

verify-drive:
	@$(VERIFY_COMPONENT_RUNNER) drive $(VERIFY_COMPONENT_FLAGS)

verify-research:
	@$(VERIFY_COMPONENT_RUNNER) research $(VERIFY_COMPONENT_FLAGS)

verify-qdrant:
	@$(VERIFY_COMPONENT_RUNNER) qdrant $(VERIFY_COMPONENT_FLAGS)

verify-indexing:
	@$(VERIFY_COMPONENT_RUNNER) indexing $(VERIFY_COMPONENT_FLAGS)

verify-docs:
	@$(VERIFY_COMPONENT_RUNNER) docs $(VERIFY_COMPONENT_FLAGS)

verify-voiceover:
	@$(VERIFY_COMPONENT_RUNNER) voiceover $(VERIFY_COMPONENT_FLAGS)

verify-database:
	@$(VERIFY_COMPONENT_RUNNER) database $(VERIFY_COMPONENT_FLAGS)

verify-jobs:
	@$(VERIFY_COMPONENT_RUNNER) jobs $(VERIFY_COMPONENT_FLAGS)
