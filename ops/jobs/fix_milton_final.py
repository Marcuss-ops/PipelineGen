#!/usr/bin/env python3
"""Repair the delivered Milton MP4 from its saved plan and local audio stems."""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
import urllib.request
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
JOB = Path("/tmp/milton-final-job-full.json")
SOURCE = ROOT / "output/Milton_Leite_FINAL.mp4"
DEST = ROOT / "output/Milton_Leite_FINAL_CORRETTO.mp4"
STEMS_DIR = ROOT / "data/media/voiceovers"
CLIPS_DIR = ROOT / "data/tmp/audioassets/assets"
BGM = ROOT / "data/media/sound_effects/bgm1.mp3"
CLIP_IDS = [
    "yt_kBWlon1GMs0_0_15_v1",
    "yt_ogu6YuDUdnQ_65_84_v1",
    "yt_DLFi4zEjI4Q_55_68_v1",
]
INTRO_ASS = [
    ROOT / "data/tmp/localization/yt_kBWlon1GMs0_0_15_v1.pt.ass",
    ROOT / "data/tmp/localization/yt_ogu6YuDUdnQ_65_84_v1.pt.ass",
    ROOT / "data/tmp/localization/yt_DLFi4zEjI4Q_55_68_v1.pt.ass",
]
VOICE_STEM = "scene_milton-introcaption-stockclean-overlay25-final-202"


def run(args: list[str]) -> None:
    subprocess.run(args, check=True)


def probe_duration(path: Path) -> float:
    raw = subprocess.check_output([
        "ffprobe", "-v", "error", "-show_entries", "format=duration",
        "-of", "default=nw=1:nk=1", str(path),
    ], text=True)
    return float(raw.strip())


def sec(value: float) -> str:
    return f"{value:.6f}"


def ass_time(value: str) -> float:
    h, m, s = value.split(":")
    return int(h) * 3600 + int(m) * 60 + float(s)


def ass_stamp(value: float) -> str:
    centiseconds = round(value * 100)
    h, centiseconds = divmod(centiseconds, 360000)
    m, centiseconds = divmod(centiseconds, 6000)
    s, cs = divmod(centiseconds, 100)
    return f"{h}:{m:02d}:{s:02d}.{cs:02d}"


def write_intro_ass(path: Path) -> None:
    texts = [p.read_text(encoding="utf-8-sig") for p in INTRO_ASS]
    header = texts[0].split("[Events]", 1)[0]
    events: list[str] = []
    offset = 0.0
    for index, content in enumerate(texts):
        section = content.split("[Events]", 1)[1]
        if index:
            offset = index * 5.0
        for line in section.splitlines():
            if not line.startswith("Dialogue:"):
                continue
            prefix, payload = line.split(":", 1)
            fields = payload.split(",", 9)
            fields[1] = ass_stamp(offset + ass_time(fields[1]))
            fields[2] = ass_stamp(offset + ass_time(fields[2]))
            events.append(prefix + ":" + ",".join(fields))
    path.write_text(header + "[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" + "\n".join(events) + "\n", encoding="utf-8")


def download(url: str, path: Path) -> None:
    request = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    with urllib.request.urlopen(request, timeout=60) as response, path.open("wb") as out:
        out.write(response.read())


def main() -> None:
    if not SOURCE.is_file() or not JOB.is_file():
        raise SystemExit("Missing source MP4 or saved full-job response")
    job = json.loads(JOB.read_text())
    result = job["result"]["result"]
    duration = probe_duration(SOURCE)
    temp = Path(tempfile.mkdtemp(prefix="milton-final-fix-"))

    # Reassemble the existing voiceover and source-clip stems with music at
    # -26 dB and sidechain ducking under speech.
    clip_events = result["audio_plan"]["tracks"][0]["events"]
    voice_track = next(t for t in result["audio_plan"]["tracks"] if t["role"] == "VOICEOVER")
    voice_events = voice_track["events"]
    audio_inputs: list[Path] = []
    for event in clip_events:
        audio_inputs.append(CLIPS_DIR / f"{event['asset_id']}.mp4")
    for scene_index in range(10):
        audio_inputs.append(STEMS_DIR / f"{VOICE_STEM}_scene-{scene_index}_pt.mp3")
    if any(not path.is_file() for path in audio_inputs) or not BGM.is_file():
        missing = [str(p) for p in audio_inputs if not p.is_file()]
        raise SystemExit(f"Missing audio inputs: {missing}")

    audio_cmd = ["ffmpeg", "-hide_banner", "-y", "-loglevel", "warning"]
    # Add the intro source clips and ten ready-made narration stems.
    for path in audio_inputs:
        audio_cmd.extend(["-i", str(path)])
    audio_cmd.extend(["-stream_loop", "-1", "-i", str(BGM)])
    audio_filters: list[str] = []
    voice_labels: list[str] = []
    for i, event in enumerate(clip_events):
        start = event["timeline_start_us"] / 1_000_000
        dur = event["duration_us"] / 1_000_000
        delay = round(start * 1000)
        label = f"clip{i}"
        audio_filters.append(f"[{i}:a]atrim=duration={sec(dur)},asetpts=PTS-STARTPTS,volume=-10dB,adelay={delay}|{delay}[{label}]")
        voice_labels.append(f"[{label}]")
    for i, event in enumerate(voice_events):
        input_index = len(clip_events) + i
        start = event["timeline_start_us"] / 1_000_000
        dur = event["duration_us"] / 1_000_000
        delay = round(start * 1000)
        label = f"voice{i}"
        audio_filters.append(f"[{input_index}:a]atrim=duration={sec(dur)},asetpts=PTS-STARTPTS,adelay={delay}|{delay}[{label}]")
        voice_labels.append(f"[{label}]")
    audio_filters.append(f"{''.join(voice_labels)}amix=inputs={len(voice_labels)}:duration=longest:normalize=0,asplit=2[voice_mix][voice_sidechain]")
    music_input = len(audio_inputs)
    music_duration = max(0.0, duration - 15.0)
    audio_filters.append(f"[{music_input}:a]atrim=duration={sec(music_duration)},asetpts=PTS-STARTPTS,volume=-26dB,adelay=15000|15000,apad,atrim=duration={sec(duration)}[music]")
    audio_filters.append("[music][voice_sidechain]sidechaincompress=threshold=0.025:ratio=8:attack=15:release=600[ducked]")
    audio_filters.append("[voice_mix][ducked]amix=inputs=2:duration=longest:normalize=0,alimiter=limit=0.95[aout]")
    audio_cmd.extend([
        "-filter_complex", ";".join(audio_filters), "-map", "[aout]",
        "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
        "-t", sec(duration), str(temp / "mix.m4a"),
    ])
    print("Mixing voice, intro clip audio and quieter ducked music...", flush=True)
    run(audio_cmd)

    # Reuse each planned photo, enlarge it, and place it at the visual center
    # during its already-reserved five-second replacement window.
    image_dir = temp / "images"
    image_dir.mkdir()
    image_items = [item for item in result["overlay_plan"]["items"] if item["kind"] == "image"]
    images: list[tuple[Path, float, float]] = []
    for index, item in enumerate(image_items):
        ref = item["asset_refs"][0]
        image_path = image_dir / f"image-{index:02d}.jpg"
        download(ref["url"], image_path)
        images.append((image_path, item["start_ms"] / 1000.0, item["end_ms"] / 1000.0))

    intro_ass = temp / "intro.pt.ass"
    write_intro_ass(intro_ass)
    # Escape filter-graph path punctuation without changing the on-disk path.
    ass_path = str(intro_ass).replace("\\", "\\\\").replace(":", "\\:").replace("'", "\\'")
    video_cmd = ["ffmpeg", "-hide_banner", "-y", "-loglevel", "warning", "-i", str(SOURCE)]
    for image_path, _, _ in images:
        video_cmd.extend(["-loop", "1", "-framerate", "24", "-t", "5", "-i", str(image_path)])
    audio_index = 1 + len(images)
    video_cmd.extend(["-i", str(temp / "mix.m4a")])
    video_filters = [f"[0:v]ass='{ass_path}'[v0]"]
    last = "v0"
    for i, (_, start, end) in enumerate(images):
        source_index = i + 1
        background = f"white{i}"
        centered = f"centered{i}"
        shifted = f"shifted{i}"
        out = f"v{i + 1}"
        video_filters.append(f"color=c=white:s=1920x1080:r=24:d=5,format=yuv420p[{background}]")
        video_filters.append(f"[{source_index}:v]scale=680:620:force_original_aspect_ratio=decrease,format=rgba[photo{i}]")
        video_filters.append(f"[{background}][photo{i}]overlay=x=(W-w)/2:y=(H-h)/2:shortest=1[{centered}]")
        video_filters.append(f"[{centered}]setpts=PTS-STARTPTS+{sec(start)}/TB[{shifted}]")
        video_filters.append(f"[{last}][{shifted}]overlay=x=0:y=0:eof_action=pass:repeatlast=0:enable='between(t,{sec(start)},{sec(end)})'[{out}]")
        last = out
    video_cmd.extend([
        "-filter_complex", ";".join(video_filters), "-map", f"[{last}]", "-map", f"{audio_index}:a:0",
        "-c:v", "h264_nvenc", "-preset", "p5", "-cq", "21", "-b:v", "0",
        "-c:a", "copy", "-movflags", "+faststart", "-t", sec(duration), str(DEST),
    ])
    print(f"Centering {len(images)} photos and burning subtitles into the three opening clips...", flush=True)
    run(video_cmd)
    print(f"Saved {DEST} ({DEST.stat().st_size} bytes)", flush=True)


if __name__ == "__main__":
    main()
