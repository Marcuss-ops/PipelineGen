# make/verify.components.mk - component verification target aliases.
#
# Keep component targets declarative and thin. The shared Python runner owns
# registry loading, dependency resolution, command deduplication, timeouts,
# modes, and JSON reports.

PYTHON ?= python3
VERIFY_COMPONENT_RUNNER ?= $(PYTHON) scripts/ci/verify-component.py
VERIFY_COMPONENT_FLAGS ?=

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
