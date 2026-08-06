# Aggregate pipeline targets.  The pipeline registry owns component order and
# operational commands; this file only exposes stable Make entry points.

PYTHON ?= python3
VERIFY_PIPELINE_RUNNER ?= $(PYTHON) scripts/ci/verify-pipeline.py
VERIFY_PIPELINE_FLAGS ?=

test-pipeline-stock-only:
	@$(VERIFY_PIPELINE_RUNNER) stock-only $(VERIFY_PIPELINE_FLAGS)

verify-pipeline-clip-only:
	@$(VERIFY_PIPELINE_RUNNER) clip-only $(VERIFY_PIPELINE_FLAGS)

verify-pipeline-research:
	@$(VERIFY_PIPELINE_RUNNER) research $(VERIFY_PIPELINE_FLAGS)

verify-pipeline-document:
	@$(VERIFY_PIPELINE_RUNNER) document $(VERIFY_PIPELINE_FLAGS)

verify-pipeline-voiceover:
	@$(VERIFY_PIPELINE_RUNNER) voiceover $(VERIFY_PIPELINE_FLAGS)

verify-pipeline-script:
	@$(VERIFY_PIPELINE_RUNNER) script $(VERIFY_PIPELINE_FLAGS)

test-pipeline-youtube-stock:
	@$(VERIFY_PIPELINE_RUNNER) youtube-stock $(VERIFY_PIPELINE_FLAGS)

verify-pipeline-vidrush:
	@$(VERIFY_PIPELINE_RUNNER) vidrush $(VERIFY_PIPELINE_FLAGS)
