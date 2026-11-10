"""Engine adapters for the local TTS sidecar.

Design constraints:
  * No engine is imported at module load. A broken optional dependency must
    not prevent the sidecar from starting or reporting availability.
  * Pocket TTS is the only first-class native adapter because its Python API
    is verified (`TTSModel.load_model` / `get_state_for_audio_prompt` /
    `generate_audio`).
  * Phonon and the larger GPU models are exposed through `CommandEngine`
    (a CLI template) instead of guessing undocumented Python APIs. The
    template must write a WAV (or any ffmpeg-readable audio) to `{out}`.
  * `PhononEngine.available()` probes `import ptts` in a SUBPROCESS: the
    packaged wheel can raise SIGILL on CPUs without the required ISA, and an
    in-process probe would kill the sidecar.

Environment:
  LOCAL_TTS_CMD_<ENGINE>   command template, e.g.
                           LOCAL_TTS_CMD_QWEN3="qwen3-tts --text {text} --out {out}"
                           placeholders: {text} {out} {voice} {lang}
  LOCAL_TTS_CMD_TIMEOUT    per-command timeout seconds (default 600)
  POCKET_TTS_LANGUAGE      pocket model language (default english)
  POCKET_TTS_MAX_TOKENS    override the automatic token budget
  POCKET_TTS_QUANTIZE      "1" enables pocket quantization
  LOCAL_TTS_DEFAULT_VOICE  fallback voice (default alba)
"""

import importlib.util
import os
import shlex
import shutil
import subprocess
import sys
import threading
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, Tuple


class EngineError(Exception):
    """Synthesis failed after the engine was selected."""


class EngineUnavailable(EngineError):
    """The engine cannot run in this environment."""


@dataclass
class SynthResult:
    wav_path: str
    sample_rate: int
    voice: str
    synth_ms: int


class BaseEngine:
    name = "base"
    license = "unknown"
    supports_timing = False

    def available(self) -> Tuple[bool, str]:
        raise NotImplementedError

    def synthesize(self, text: str, lang: str, voice: str, out_wav: str) -> SynthResult:
        raise NotImplementedError


class PocketEngine(BaseEngine):
    """Kyutai Pocket TTS — CPU-first, ~100M main model (plus 18M/7M/2M)."""

    name = "pocket"
    license = "MIT (code) / CC-BY-4.0 (weights); predefined voice terms apply"

    _model = None
    _states: Dict[str, object] = {}
    _lock = threading.Lock()

    def available(self) -> Tuple[bool, str]:
        if importlib.util.find_spec("pocket_tts") is None:
            return False, "python package pocket-tts is not installed"
        return True, "pocket-tts installed (weights load lazily on first request)"

    def _load_model(self):
        from pocket_tts import TTSModel

        language = os.environ.get("POCKET_TTS_LANGUAGE", "english")
        quantize = os.environ.get("POCKET_TTS_QUANTIZE", "0") == "1"
        return TTSModel.load_model(language=language, quantize=quantize)

    def synthesize(self, text: str, lang: str, voice: str, out_wav: str) -> SynthResult:
        if importlib.util.find_spec("pocket_tts") is None:
            raise EngineUnavailable("python package pocket-tts is not installed")
        try:
            import numpy as np
            import soundfile as sf
        except ImportError as exc:  # pragma: no cover - environment guard
            raise EngineUnavailable(f"pocket audio dependencies missing: {exc}") from exc

        started = time.monotonic()
        selected_voice = voice or os.environ.get("LOCAL_TTS_DEFAULT_VOICE", "alba")

        # Serialize model load + generation: Pocket is CPU-bound and the
        # clonable state is mutable. The sidecar semaphore also defaults to 1.
        with self._lock:
            if self._model is None:
                self._model = self._load_model()

            state = self._states.get(selected_voice)
            if state is None:
                state = self._model.get_state_for_audio_prompt(selected_voice)
                self._states[selected_voice] = state

            # Pocket's default max_tokens=50 is ~4 s of audio, far below a
            # scene. Budget ~4 tokens per word (EOS stops it early).
            words = max(1, len(text.split()))
            configured = int(os.environ.get("POCKET_TTS_MAX_TOKENS", "0") or 0)
            max_tokens = configured if configured > 0 else max(64, words * 4)
            max_tokens = min(max_tokens, 4096)

            audio = self._model.generate_audio(
                state, text, max_tokens=max_tokens, copy_state=True
            )
            sample_rate = int(self._model.sample_rate)

        array = audio.detach().cpu().numpy().reshape(-1)
        sf.write(out_wav, array, sample_rate, subtype="PCM_16")
        return SynthResult(
            wav_path=out_wav,
            sample_rate=sample_rate,
            voice=selected_voice,
            synth_ms=int((time.monotonic() - started) * 1000),
        )


class CommandEngine(BaseEngine):
    """Generic CLI adapter for engines without a verified Python API."""

    def __init__(self, name: str, env_var: str | None = None):
        self.name = name
        self._env_var = env_var or f"LOCAL_TTS_CMD_{name.upper()}"

    @property
    def template(self) -> str:
        return os.environ.get(self._env_var, "").strip()

    def available(self) -> Tuple[bool, str]:
        if not self.template:
            return False, f"{self._env_var} is not set"
        try:
            argv = shlex.split(self.template)
        except ValueError as exc:
            return False, f"invalid {self._env_var}: {exc}"
        if not argv:
            return False, f"{self._env_var} is empty"
        executable = argv[0]
        if not (Path(executable).exists() or shutil.which(executable)):
            return False, f"executable not found: {executable}"
        return True, f"{self._env_var} configured"

    def synthesize(self, text: str, lang: str, voice: str, out_wav: str) -> SynthResult:
        if not self.template:
            raise EngineUnavailable(f"{self._env_var} is not set")
        started = time.monotonic()
        argv = [
            token.format(text=text, out=out_wav, voice=voice or "", lang=lang)
            for token in shlex.split(self.template)
        ]
        timeout = float(os.environ.get("LOCAL_TTS_CMD_TIMEOUT", "600"))
        proc = subprocess.run(
            argv, capture_output=True, text=True, timeout=timeout, check=False
        )
        if proc.returncode != 0:
            tail = (proc.stderr or proc.stdout or "").strip()[-800:]
            raise EngineError(
                f"{self.name} command failed (exit {proc.returncode}): {tail}"
            )
        if not os.path.exists(out_wav) or os.path.getsize(out_wav) == 0:
            raise EngineError(f"{self.name} command produced no audio at {out_wav}")
        return SynthResult(
            wav_path=out_wav,
            sample_rate=0,
            voice=voice,
            synth_ms=int((time.monotonic() - started) * 1000),
        )


class PhononEngine(CommandEngine):
    """Gradium Phonon 31M (Apache-2.0) through its runner.

    The packaged `ptts` wheel is known to raise SIGILL on CPUs without the
    required vector ISA (verified on Intel Ivy Bridge). The probe therefore
    runs in a subprocess and reports the failure instead of crashing the
    sidecar. Set LOCAL_TTS_CMD_PHONON to a working runner to enable it.
    """

    name = "phonon"
    license = "Apache-2.0 (phonon-31m-en); check per-model terms"

    def __init__(self):
        super().__init__("phonon", "LOCAL_TTS_CMD_PHONON")

    def available(self) -> Tuple[bool, str]:
        configured, detail = super().available()
        if configured:
            return True, detail
        try:
            probe = subprocess.run(
                [sys.executable, "-c", "import ptts"],
                capture_output=True,
                text=True,
                timeout=20,
                check=False,
            )
        except Exception as exc:  # pragma: no cover - environment guard
            return False, f"{detail}; ptts probe failed: {exc}"
        if probe.returncode != 0:
            return False, (
                f"{detail}; `python -c 'import ptts'` failed on this host "
                f"(exit {probe.returncode}, likely unsupported CPU instruction); "
                "set LOCAL_TTS_CMD_PHONON to a working runner"
            )
        return False, (
            f"{detail}; ptts imports but no command template is configured"
        )


def build_registry() -> Dict[str, BaseEngine]:
    """Return every engine name the wire protocol accepts.

    `pocket` and `phonon` are first-class. The four GPU candidates from the
    2026-10 shortlist stay provable through the same harness and the same
    /synthesize contract as soon as their CLI is configured.
    """
    registry: Dict[str, BaseEngine] = {
        "pocket": PocketEngine(),
        "phonon": PhononEngine(),
    }
    for extra in ("qwen3", "cosyvoice", "fish", "audio8"):
        registry[extra] = CommandEngine(extra)
    return registry
