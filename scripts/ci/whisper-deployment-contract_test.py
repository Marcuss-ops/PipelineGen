#!/usr/bin/env python3
"""Static contract tests for the reproducible worker Whisper runtime."""

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "scripts/requirements-whisper.txt"
LOCKFILE = ROOT / "requirements/whisper.lock.txt"


class WhisperDeploymentContractTest(unittest.TestCase):
    def test_manifest_pins_required_runtime_packages(self):
        manifest = MANIFEST.read_text()
        required = (
            "faster-whisper",
            "ctranslate2",
            "nvidia-cublas-cu12",
            "nvidia-cuda-nvrtc-cu12",
            "nvidia-cudnn-cu12",
        )
        for package in required:
            self.assertRegex(
                manifest,
                rf"(?m)^{re.escape(package)}==[^#\s]+$",
            )

    def test_lockfile_contains_verified_runtime_closure(self):
        lockfile = LOCKFILE.read_text()
        required = (
            "faster-whisper==1.2.1",
            "ctranslate2==4.8.1",
            "nvidia-cublas-cu12==12.9.2.10",
            "nvidia-cuda-nvrtc-cu12==12.9.86",
            "nvidia-cudnn-cu12==9.24.0.43",
        )
        for package in required:
            self.assertRegex(lockfile, rf"(?m)^{re.escape(package)}$")
        package_lines = [
            line.strip()
            for line in lockfile.splitlines()
            if line.strip() and not line.lstrip().startswith("#")
        ]
        self.assertGreaterEqual(len(package_lines), 30)
        for line in package_lines:
            self.assertRegex(line, r"^[A-Za-z0-9_.-]+(?:\[[^\]]+\])?==[^\s]+$")
        self.assertNotRegex(
            lockfile,
            r"(?i)https?://|-----BEGIN .*PRIVATE KEY-----|(?:token|secret|password|credential|api[_-]?key|authorization)\s*[:=]",
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
            "COPY requirements/whisper.lock.txt /opt/whisper/requirements.lock.txt",
            dockerfile,
        )
        self.assertIn(
            "/opt/venv/bin/python -m pip install --no-cache-dir",
            dockerfile,
        )
        self.assertIn("--requirement /opt/whisper/requirements.lock.txt", dockerfile)
        self.assertIn('ENV PATH="/opt/venv/bin:${PATH}"', dockerfile)
        self.assertIn(
            'LD_LIBRARY_PATH="/opt/venv/cublas-lib:/opt/venv/cuda-nvrtc-lib:/opt/venv/cudnn-lib"',
            dockerfile,
        )
        self.assertIn(
            'VELOX_WHISPER_CUDA_LIB_DIR="/opt/venv/cublas-lib"',
            dockerfile,
        )
        self.assertLess(
            dockerfile.index("COPY requirements/whisper.lock.txt"),
            dockerfile.index("COPY scripts/ /app/scripts/"),
        )
        self.assertIn('VELOX_WHISPER_DEVICE="auto"', dockerfile)
        self.assertIn('VELOX_WHISPER_MODEL="base"', dockerfile)

    def test_systemd_dropin_centralizes_whisper_runtime(self):
        dropin = ROOT / "scripts/systemd/pipelinegen.service.d/whisper.conf"
        self.assertTrue(dropin.is_file())
        content = dropin.read_text()
        self.assertIn("[Service]", content)
        self.assertIn(
            "Environment=\"PATH=/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/.venv-whisper/bin:",
            content,
        )
        self.assertIn(
            "Environment=\"VELOX_WHISPER_DEVICE=auto\"",
            content,
        )
        self.assertIn(
            "Environment=\"VELOX_WHISPER_MODEL=base\"",
            content,
        )
        self.assertIn("VELOX_WHISPER_CUDA_LIB_DIR=", content)
        self.assertIn("cuda_nvrtc/lib", content)
        self.assertIn("/usr/local/lib/ollama/cuda_v12", content)
        self.assertNotRegex(content, r"(?:VELOX_ADMIN_TOKEN|VELOX_WORKER_TOKEN|METRICS_AUTH_TOKEN)=")

    def test_start_server_preserves_systemd_whisper_environment(self):
        start_server = (ROOT / "start_server.sh").read_text()
        for name in (
            "CANONICAL_PATH",
            "CANONICAL_LD_LIBRARY_PATH",
            "CANONICAL_WHISPER_DEVICE",
            "CANONICAL_WHISPER_MODEL",
            "CANONICAL_WHISPER_CUDA_LIB_DIR",
        ):
            self.assertIn(name, start_server)

    def test_probe_and_make_target_are_registered(self):
        probe = ROOT / "scripts/verify-whisper.sh"
        self.assertTrue(probe.is_file())
        self.assertTrue(probe.stat().st_mode & 0o111)
        probe_text = probe.read_text()
        self.assertIn("faster_whisper", probe_text)
        self.assertIn("ctranslate2", probe_text)
        self.assertIn("MANIFEST=/opt/whisper/requirements.lock.txt", probe_text)
        self.assertIn('expected-${package}', probe_text)

        compose = (ROOT / "docker-compose.yml").read_text()
        self.assertIn("pipelinegen-worker:", compose)
        self.assertIn('VELOX_WHISPER_DEVICE: "auto"', compose)
        gpu_compose = (ROOT / "docker-compose.gpu.yml").read_text()
        self.assertIn("pipelinegen-worker:", gpu_compose)
        self.assertIn("driver: nvidia", gpu_compose)
        self.assertIn("count: all", gpu_compose)
        self.assertIn("capabilities: [gpu]", gpu_compose)

        makefile = (ROOT / "make/docker.mk").read_text()
        self.assertIn("docker-verify-whisper:", makefile)
        self.assertIn("scripts/verify-whisper.sh", makefile)
        root_makefile = (ROOT / "Makefile").read_text()
        self.assertIn("docker-verify-whisper", root_makefile)


if __name__ == "__main__":
    unittest.main()
