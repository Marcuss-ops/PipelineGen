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
            "/opt/whisper-venv/bin/python -m pip install --no-cache-dir",
            dockerfile,
        )
        self.assertIn("--requirement /opt/whisper/requirements.lock.txt", dockerfile)
        self.assertIn('ENV PATH="/opt/whisper-venv/bin:${PATH}"', dockerfile)
        self.assertIn(
            'LD_LIBRARY_PATH="/opt/whisper-venv/cublas-lib:/opt/whisper-venv/cuda-nvrtc-lib:/opt/whisper-venv/cudnn-lib"',
            dockerfile,
        )
        self.assertIn(
            'VELOX_WHISPER_CUDA_LIB_DIR="/opt/whisper-venv/cublas-lib"',
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
        # Fail-closed host contract: cuda is pinned; a broken CUDA stack or a
        # missing runtime must block startup, never silently degrade to CPU.
        self.assertIn(
            "Environment=\"VELOX_WHISPER_DEVICE=cuda\"",
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
        self.assertIn("ExecStartPre=", content)
        self.assertIn(".venv-whisper/bin/python3", content)
        self.assertIn("scripts/tools/whisper_preflight.py", content)

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

    def test_canonical_preflight_is_the_single_runtime_source(self):
        preflight = ROOT / "scripts/tools/whisper_preflight.py"
        self.assertTrue(preflight.is_file())
        self.assertTrue(preflight.stat().st_mode & 0o111)
        text = preflight.read_text()
        runtime = (ROOT / "scripts/tools/whisper_runtime.py").read_text()
        for needle in (
            "VELOX_WHISPER_DEVICE",
            "VELOX_WHISPER_MODEL",
            "CUDA_LIBRARY_SONAMES",
            "cuda_library_dirs",
            "prepare_cuda_runtime",
            "CUDA_LIBRARIES",
            "_check_cuda_libraries",
            "_model_check",
            "KNOWN_MODEL_NAMES",
            "unsupported Whisper model name",
            "Python 3.9 or newer",
            "cuda_devices",
            '"faster-whisper"',
            '"ctranslate2"',
            '"ok"',
            "get_cuda_device_count",
            "sys.exit(main())",
        ):
            self.assertIn(needle, text)
        for needle in (
            "VELOX_WHISPER_CUDA_LIB_DIR",
            "LD_LIBRARY_PATH",
            "prepare_cuda_runtime",
            "libcublas.so.12",
            "libnvrtc.so.12",
            "libcudnn.so.9",
        ):
            self.assertIn(needle, runtime)
        # The preflight is the only runtime gate the dropin and the image
        # probe may delegate to; no duplicated long python -c one-liners.
        dropin = (ROOT / "scripts/systemd/pipelinegen.service.d/whisper.conf").read_text()
        self.assertIn("scripts/tools/whisper_preflight.py", dropin)
        self.assertNotRegex(dropin, r"ExecStartPre=.*python3 -c")
        probe = (ROOT / "scripts/verify-whisper.sh").read_text()
        self.assertIn("whisper_preflight.py", probe)
        bridge = (ROOT / "scripts/tools/transcribe_detect_lang.py").read_text()
        self.assertIn("from whisper_runtime import prepare_cuda_runtime", bridge)
        self.assertNotIn('"/usr/local/lib/ollama/cuda_v12"', bridge)

        makefile = (ROOT / "make/verify.mk").read_text()
        self.assertIn("whisper-preflight:", makefile)
        self.assertIn("scripts/tools/whisper_preflight.py", makefile)
        root_makefile = (ROOT / "Makefile").read_text()
        self.assertIn("whisper-preflight", root_makefile)

    def test_probe_and_make_target_are_registered(self):
        probe = ROOT / "scripts/verify-whisper.sh"
        self.assertTrue(probe.is_file())
        self.assertTrue(probe.stat().st_mode & 0o111)
        probe_text = probe.read_text()
        self.assertIn("whisper_preflight.py", probe_text)
        self.assertIn("/opt/whisper-venv/bin/python3", probe_text)
        self.assertIn("canonical Whisper preflight", probe_text)
        self.assertNotIn("import faster_whisper, ctranslate2", probe_text)
        self.assertNotRegex(probe_text, r"(?m)^\s*(?:from\s+|import\s+)faster_whisper")
        self.assertNotRegex(probe_text, r"(?m)^\s*(?:from\s+|import\s+)ctranslate2")

        compose = (ROOT / "docker-compose.yml").read_text()
        self.assertIn("pipelinegen-worker:", compose)
        self.assertIn('VELOX_WHISPER_DEVICE: "auto"', compose)
        self.assertIn('VELOX_WHISPER_MODEL: "base"', compose)
        self.assertIn('VELOX_WHISPER_CUDA_LIB_DIR: "/opt/whisper-venv/cublas-lib"', compose)
        self.assertIn("driver: nvidia", compose)
        self.assertIn("count: all", compose)
        self.assertIn("capabilities: [gpu]", compose)
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
