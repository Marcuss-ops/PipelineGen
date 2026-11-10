"""Local (open-weight) TTS bridge for the English-only pilot.

The package keeps one HTTP contract for every engine:
`pocket` (Kyutai Pocket TTS), `phonon` (Gradium Phonon 31M) and the
`command` adapters used to plug the larger GPU models without inventing
their Python APIs.

No engine is imported at module load: `pocket_tts` / `ptts` are imported
lazily by the engine that needs them, so the sidecar can start and report
availability even when only one engine is installed.
"""

__all__ = ["engines", "request", "server"]
