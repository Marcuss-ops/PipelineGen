"""Hermetic tests for the TTS rate contract across both bridge sidecars.

These tests never touch the network. The Edge CLI test installs a fake
`edge_tts` package on PYTHONPATH so the real argument parsing and
`Communicate(rate=...)` wiring are exercised without the provider.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

BRIDGES_DIR = Path(__file__).resolve().parents[1]
if str(BRIDGES_DIR) not in sys.path:
    sys.path.insert(0, str(BRIDGES_DIR))

from edge_tts_bridge import request as edge_request
from local_tts_bridge import request as local_request


class EdgeRateRequestTests(unittest.TestCase):
    def _parse(self, rate):
        return edge_request.SynthesizeRequest.from_dict(
            {"text": "hello", "out": "/tmp/rate.mp3", "rate": rate}
        )

    def test_empty_rate_preserves_provider_default(self):
        self.assertEqual(self._parse("").rate, "")

    def test_valid_rates_are_canonicalised(self):
        cases = {
            "+5%": "+5%",
            "+10%": "+10%",
            "-20%": "-20%",
            "+0%": "+0%",
            "-0%": "+0%",
            "+100%": "+100%",
            "-50%": "-50%",
        }
        for raw, want in cases.items():
            with self.subTest(raw=raw):
                self.assertEqual(self._parse(raw).rate, want)

    def test_invalid_rates_fail_closed(self):
        for raw in ("10%", "+150%", "-60%", "1.1x", "abc", "+1", "+1%%"):
            with self.subTest(raw=raw):
                with self.assertRaises(edge_request.SynthesizeRequestError):
                    self._parse(raw)


class LocalRateRequestTests(unittest.TestCase):
    def _parse(self, rate):
        return local_request.SynthesizeRequest.from_dict(
            {
                "text": "hello",
                "out": "/tmp/rate.mp3",
                "engine": "pocket",
                "lang": "en",
                "rate": rate,
            },
            "pocket",
        )

    def test_empty_rate_preserves_engine_default(self):
        self.assertEqual(self._parse("").rate, "")

    def test_valid_rates_are_canonicalised(self):
        self.assertEqual(self._parse("+5%").rate, "+5%")
        self.assertEqual(self._parse("+10%").rate, "+10%")
        self.assertEqual(self._parse("-20%").rate, "-20%")
        self.assertEqual(self._parse("-0%").rate, "+0%")

    def test_invalid_rates_fail_closed(self):
        for raw in ("10%", "+101%", "-51%", "1.1x", "abc", "+1", "+1%%"):
            with self.subTest(raw=raw):
                with self.assertRaises(local_request.SynthesizeRequestError):
                    self._parse(raw)

    def test_atempo_filter_is_identity_for_default_and_zero(self):
        self.assertEqual(local_request.atempo_filter(""), [])
        self.assertEqual(local_request.atempo_filter("+0%"), [])

    def test_atempo_filter_maps_percent_to_factor(self):
        self.assertEqual(local_request.atempo_filter("+5%"), ["-af", "atempo=1.05"])
        self.assertEqual(local_request.atempo_filter("+10%"), ["-af", "atempo=1.1"])
        self.assertEqual(local_request.atempo_filter("-20%"), ["-af", "atempo=0.8"])
        self.assertEqual(local_request.atempo_filter("+100%"), ["-af", "atempo=2"])
        self.assertEqual(local_request.atempo_filter("-50%"), ["-af", "atempo=0.5"])

    def test_atempo_filter_rejects_invalid_rate(self):
        with self.assertRaises(local_request.SynthesizeRequestError):
            local_request.atempo_filter("1.1x")


@unittest.skipUnless(
    shutil.which("ffmpeg") and shutil.which("ffprobe"),
    "ffmpeg/ffprobe not installed",
)
class AtempoIntegrationTests(unittest.TestCase):
    def test_atempo_changes_real_audio_duration(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = os.path.join(tmp, "tone.wav")
            subprocess.run(
                [
                    shutil.which("ffmpeg"),
                    "-y",
                    "-loglevel",
                    "error",
                    "-f",
                    "lavfi",
                    "-i",
                    "sine=frequency=440:duration=2",
                    "-ar",
                    "48000",
                    "-ac",
                    "2",
                    source,
                ],
                check=True,
            )

            def render(rate, name):
                out = os.path.join(tmp, name)
                subprocess.run(
                    [
                        shutil.which("ffmpeg"),
                        "-y",
                        "-loglevel",
                        "error",
                        "-i",
                        source,
                        "-ac",
                        "2",
                        "-ar",
                        "48000",
                        *local_request.atempo_filter(rate),
                        out,
                    ],
                    check=True,
                )
                return self._duration(out)

            base = render("+0%", "base.mp3")
            fast5 = render("+5%", "fast5.mp3")
            fast = render("+20%", "fast.mp3")
            slow = render("-20%", "slow.mp3")
            slow50 = render("-50%", "slow50.mp3")
            fast100 = render("+100%", "fast100.mp3")

            self.assertAlmostEqual(base, 2.0, delta=0.15)
            self.assertAlmostEqual(fast5, base / 1.05, delta=0.05)
            self.assertAlmostEqual(slow50, base / 0.5, delta=0.15)
            self.assertAlmostEqual(fast100, base / 2.0, delta=0.15)
            self.assertLess(fast, base * 0.9)
            self.assertGreater(slow, base * 1.1)

    @staticmethod
    def _duration(path):
        raw = subprocess.run(
            [
                shutil.which("ffprobe"),
                "-v",
                "error",
                "-show_entries",
                "format=duration",
                "-of",
                "default=nw=1:nk=1",
                path,
            ],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()
        return float(raw)


class EdgeCliRateTests(unittest.TestCase):
    def _fake_edge_tts(self, root):
        package = Path(root) / "edge_tts"
        package.mkdir(parents=True)
        (package / "__init__.py").write_text(
            "import os\n"
            "class Communicate:\n"
            "    def __init__(self, text, voice, boundary=None, rate=None):\n"
            "        self.rate = rate\n"
            "        with open(os.environ['FAKE_EDGE_RATE_FILE'], 'w', encoding='utf-8') as fh:\n"
            "            fh.write(rate or '')\n"
            "    async def stream(self):\n"
            "        yield {'type': 'audio', 'data': b'FAKE_MP3'}\n"
            "async def list_voices(*args, **kwargs):\n"
            "    return []\n",
            encoding="utf-8",
        )
        (package / "constants.py").write_text(
            'TRUSTED_CLIENT_TOKEN = "test-token"\n', encoding="utf-8"
        )
        (package / "drm.py").write_text(
            "class DRM:\n    parse_rfc2616_date = None\n", encoding="utf-8"
        )
        (package / "communicate.py").write_text('WSS_URL = ""\n', encoding="utf-8")

    def _run_cli(self, tmp, rate):
        fake_root = os.path.join(tmp, "fake")
        self._fake_edge_tts(fake_root)
        record = os.path.join(tmp, "rate.txt")
        out = os.path.join(tmp, "out.mp3")
        env = os.environ.copy()
        env["PYTHONPATH"] = os.pathsep.join([fake_root, str(BRIDGES_DIR)])
        env["FAKE_EDGE_RATE_FILE"] = record
        proc = subprocess.run(
            [
                sys.executable,
                str(BRIDGES_DIR / "tts_edge.py"),
                "--text",
                "hello world",
                "--lang",
                "en",
                "--out",
                out,
                "--voice",
                "en-US-Test",
                "--rate",
                rate,
            ],
            env=env,
            capture_output=True,
            text=True,
        )
        return proc, record, out

    def test_cli_forwards_rate_to_communicate(self):
        with tempfile.TemporaryDirectory() as tmp:
            proc, record, out = self._run_cli(tmp, "+10%")
            self.assertEqual(proc.returncode, 0, proc.stderr)
            payload = json.loads(proc.stdout.strip().splitlines()[-1])
            self.assertTrue(payload["ok"])
            self.assertTrue(os.path.exists(out))
            self.assertEqual(Path(record).read_text(encoding="utf-8"), "+10%")

    def test_cli_accepts_negative_rate_separated_by_space(self):
        with tempfile.TemporaryDirectory() as tmp:
            proc, record, out = self._run_cli(tmp, "-20%")
            self.assertEqual(proc.returncode, 0, proc.stderr)
            payload = json.loads(proc.stdout.strip().splitlines()[-1])
            self.assertTrue(payload["ok"])
            self.assertTrue(os.path.exists(out))
            self.assertEqual(Path(record).read_text(encoding="utf-8"), "-20%")

    def test_cli_rejects_out_of_range_rate_before_synthesis(self):
        with tempfile.TemporaryDirectory() as tmp:
            proc, record, out = self._run_cli(tmp, "+150%")
            self.assertEqual(proc.returncode, 1)
            payload = json.loads(proc.stdout.strip().splitlines()[-1])
            self.assertFalse(payload["ok"])
            self.assertIn('invalid "rate"', payload["error"])
            self.assertFalse(os.path.exists(record))
            self.assertFalse(os.path.exists(out))
