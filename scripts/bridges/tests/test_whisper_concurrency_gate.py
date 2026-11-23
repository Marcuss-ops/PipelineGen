"""Hermetic tests for the host-wide ASR concurrency gate.

The gate is the fix for CUDA OOM under the clip.render fan-out: without it every
parallel clip process loaded its own Whisper model into VRAM. These tests prove
the limit parsing and the mutual exclusion itself, without loading a model.
"""

import os
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[3]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from scripts.tools import transcribe_detect_lang as asr  # noqa: E402


class ConcurrencyLimitTests(unittest.TestCase):
    def _limit(self, raw):
        env = {} if raw is None else {"VELOX_WHISPER_MAX_CONCURRENCY": raw}
        with mock.patch.dict(os.environ, env, clear=False):
            if raw is None:
                os.environ.pop("VELOX_WHISPER_MAX_CONCURRENCY", None)
            return asr._asr_concurrency_limit()

    def test_default_is_one(self):
        self.assertEqual(self._limit(None), 1)

    def test_explicit_limit_is_honoured(self):
        self.assertEqual(self._limit("2"), 2)

    def test_invalid_and_absurd_values_are_clamped(self):
        self.assertEqual(self._limit(""), 1)
        self.assertEqual(self._limit("abc"), 1)
        self.assertEqual(self._limit("0"), 1)
        self.assertEqual(self._limit("-3"), 1)
        self.assertEqual(self._limit("999"), asr._ASR_SLOT_MAX)


@unittest.skipIf(asr.fcntl is None, "advisory file locks unavailable on this host")
class GateExclusionTests(unittest.TestCase):
    def setUp(self):
        self._dir = tempfile.TemporaryDirectory()
        self.addCleanup(self._dir.cleanup)
        self._env = mock.patch.dict(
            os.environ,
            {
                "VELOX_WHISPER_LOCK_DIR": self._dir.name,
                "VELOX_WHISPER_MAX_CONCURRENCY": "1",
            },
            clear=False,
        )
        self._env.start()
        self.addCleanup(self._env.stop)

    def test_limit_one_serializes_two_holders(self):
        first_in = threading.Event()
        release_first = threading.Event()
        second_in = threading.Event()

        def hold_first():
            with asr._asr_concurrency_slot():
                first_in.set()
                release_first.wait(5)

        def hold_second():
            with asr._asr_concurrency_slot():
                second_in.set()

        first = threading.Thread(target=hold_first, daemon=True)
        first.start()
        self.assertTrue(first_in.wait(2), "first slot was never acquired")

        second = threading.Thread(target=hold_second, daemon=True)
        second.start()
        time.sleep(0.4)
        self.assertFalse(
            second_in.is_set(),
            "a second holder entered the gate while the only slot was taken",
        )

        release_first.set()
        self.assertTrue(second_in.wait(2), "the second holder never acquired the freed slot")
        second.join(2)
        first.join(2)

    def test_limit_two_allows_parallel_holders(self):
        with mock.patch.dict(os.environ, {"VELOX_WHISPER_MAX_CONCURRENCY": "2"}, clear=False):
            both_in = threading.Barrier(2, timeout=2)
            errors = []

            def hold():
                try:
                    with asr._asr_concurrency_slot():
                        both_in.wait()
                except Exception as exc:  # pragma: no cover - reported below
                    errors.append(exc)

            threads = [threading.Thread(target=hold, daemon=True) for _ in range(2)]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join(3)

            self.assertFalse(errors, "two slots should admit two holders: %r" % (errors,))
            self.assertTrue(all(not thread.is_alive() for thread in threads))


if __name__ == "__main__":
    unittest.main()
