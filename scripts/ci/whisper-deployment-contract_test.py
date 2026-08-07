#!/usr/bin/env python3
"""Static contract tests for the reproducible worker Whisper runtime."""

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "scripts/requirements-whisper.txt"


class WhisperDeploymentContractTest(unittest.TestCase):
    def test_manifest_pins_required_runtime_packages(self):
        manifest = MANIFEST.read_text()
        required = (
            "faster-whisper",
            "ctranslate2",
            "nvidia-cublas-cu12",
            "nvidia-cudnn-cu12",
        )
        for package in required:
            self.assertRegex(
                manifest,
                rf"(?m)^{re.escape(package)}==[^#\s]+$",
            )

    def test_worker_copies_and_installs_manifest_before_scripts(self):
        dockerfile = (ROOT / "Dockerfile").read_text()
        self.assertIn("AS worker-runtime", dockerfile)
        self.assertIn("python3-venv", dockerfile)
        self.assertIn(
            "COPY scripts/requirements-whisper.txt /opt/whisper/requirements.txt",
            dockerfile,
        )
        self.assertIn(
            "/opt/venv/bin/python -m pip install --no-cache-dir",
            dockerfile,
        )
        self.assertIn("--requirement /opt/whisper/requirements.txt", dockerfile)
        self.assertIn('ENV PATH="/opt/venv/bin:${PATH}"', dockerfile)
        self.assertIn(
            'LD_LIBRARY_PATH="/opt/venv/cublas-lib:/opt/venv/cudnn-lib"',
            dockerfile,
        )
        self.assertLess(
            dockerfile.index("COPY scripts/requirements-whisper.txt"),
            dockerfile.index("COPY scripts/ /app/scripts/"),
        )

    def test_probe_and_make_target_are_registered(self):
        probe = ROOT / "scripts/verify-whisper.sh"
        self.assertTrue(probe.is_file())
        self.assertTrue(probe.stat().st_mode & 0o111)
        probe_text = probe.read_text()
        self.assertIn("faster_whisper", probe_text)
        self.assertIn("ctranslate2", probe_text)
        self.assertIn("MANIFEST=/opt/whisper/requirements.txt", probe_text)
        self.assertIn('expected-${package}', probe_text)

        makefile = (ROOT / "make/docker.mk").read_text()
        self.assertIn("docker-verify-whisper:", makefile)
        self.assertIn("scripts/verify-whisper.sh", makefile)
        root_makefile = (ROOT / "Makefile").read_text()
        self.assertIn("docker-verify-whisper", root_makefile)


if __name__ == "__main__":
    unittest.main()
