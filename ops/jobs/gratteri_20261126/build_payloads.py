#!/usr/bin/env python3
"""Build canonical POST /api/clips/process payloads for the Gratteri batch.

18 curated hooks from 4 Nicola Gratteri interviews. Destination: the explicit
Drive folder Saggezza/Gratteri (1oW6xyqgA9Q-vb2K0eucM4bwCiZrwDqFS), flat
(create_subfolder=false), so every clip lands directly in it.

require_transcript_ready=true makes the READY transcript a hard pre-commit
requirement: the clip is never committed/indexed without its text track
(Whisper first, YouTube captions as fallback per config.yaml).

Output: payloads/<video_id>.process.json (dto.ExtractRequest) + manifest.json.
The builder fails on duplicate windows/names, out-of-range durations, or
timestamps beyond the known video duration.
"""

from __future__ import annotations

import json
from pathlib import Path

HERE = Path(__file__).resolve().parent
PAYLOADS = HERE / "payloads"

DESTINATION = {
    "folder_id": "1oW6xyqgA9Q-vb2K0eucM4bwCiZrwDqFS",  # Saggezza/Gratteri
    "create_subfolder": False,
}

CATEGORY = "intervista"
TAGS = ["Gratteri", "mafia", "politica"]
PEOPLE = ["Nicola Gratteri"]

# Known durations probed with yt-dlp (seconds). The fourth video is
# geo-restricted from this host; its duration is unknown and is not enforced.
VIDEOS = [
    {
        "video_id": "WdbdwtO2v0E",
        "title": "Gratteri: perché un capomafia resta pericoloso in carcere?",
        "channel": "Grandi Lezioni su Crimine e Mafie",
        "duration": 3410,
        "hooks": [
            {
                "start": "02:23",
                "end": "02:36",
                "name": "Le mafie esistono perché nutrite dalla classe dirigente",
                "quote": "Le mafie esistono in Italia perché nutrite, volute o create dalla classe dirigente.",
                "topic": "Complicità storica e sistemica tra Stato, classe dirigente e organizzazioni criminali.",
            },
            {
                "start": "08:08",
                "end": "08:23",
                "name": "La superficialità di chi gestisce il potere crea mostri",
                "quote": "La superficialità e l'ignoranza di chi gestisce il potere crea mostri.",
                "topic": "Accusa trasversale a governi, leggi sbagliate e gestione delle emergenze.",
            },
            {
                "start": "10:25",
                "end": "10:36",
                "name": "I candidati vanno a casa dei mafiosi a chiedere voti",
                "quote": "Sono i candidati alle elezioni che vanno a casa dei mafiosi e chiedono pacchetti di voti.",
                "topic": "Voto di scambio, campagna elettorale e sottomissione della politica ai boss.",
            },
            {
                "start": "32:23",
                "end": "32:36",
                "name": "Il generale dell'esercito nel libro paga dei narcos",
                "quote": "Me l'ha data il generale dell'esercito... era nel mio libro paga.",
                "topic": "Corruzione ai vertici militari e istituzionali al servizio dei narcos, senza nomi.",
            },
            {
                "start": "52:43",
                "end": "52:54",
                "name": "Chi discuteva con gente al di sopra delle nostre teste",
                "quote": "Era uno che discuteva con gente al di sopra delle nostre... non vi dico chi, ma al di sopra delle nostre teste.",
                "topic": "Poteri occulti, intoccabili e rapporti tra criminalità e piani alti dello Stato.",
            },
            {
                "start": "56:13",
                "end": "56:25",
                "name": "L'antimafia di facciata e l'ipocrisia dei talk show",
                "quote": "Finiamola con questa ipocrisia, perché sono poi le stesse persone che si battono il petto e vanno nei talk show.",
                "topic": "Antimafia di facciata, ipocrisia mediatica e politica.",
            },
        ],
    },
    {
        "video_id": "CVUzdDyyPW8",
        "title": "INTERVISTA A NICOLA GRATTERI",
        "channel": "Pubble",
        "duration": 2197,
        "hooks": [
            {
                "start": "10:15",
                "end": "10:25",
                "name": "Le nomine sono una mezza truffa",
                "quote": "In realtà è una mezza truffa, perché loro prima scelgono i loro componenti di fiducia.",
                "topic": "Nomine truccate, controllo politico sugli organi di garanzia e potere.",
            },
            {
                "start": "14:33",
                "end": "14:43",
                "name": "Il potere non vuole essere controllato",
                "quote": "Il potere non vuole essere controllato.",
                "topic": "Sentenza secca per aprire video su riforme, abuso d'ufficio o impunità.",
            },
            {
                "start": "14:44",
                "end": "14:56",
                "name": "Leggi e concorsi regalati ai raccomandati",
                "quote": "È un regalo fatto ai raccomandati.",
                "topic": "Favoritismi nella pubblica amministrazione, clientelismo e leggi ad personam.",
            },
            {
                "start": "31:04",
                "end": "31:15",
                "name": "Altre cariche dello Stato hanno continuato in malafede",
                "quote": "Ho visto che altre cariche dello Stato hanno continuato in malafede.",
                "topic": "Scontro diretto con i vertici dello Stato senza citare nomi espliciti.",
            },
            {
                "start": "31:35",
                "end": "31:55",
                "name": "Comportamenti scorretti da chi non te lo aspetti",
                "quote": "Io ricordo tutto il bene e il male, ho visto comportamenti scorretti da persone dalle quali non me l'aspettavo.",
                "topic": "Tradimenti interni alle istituzioni e retroscena personali.",
            },
        ],
    },
    {
        "video_id": "a9Lr_OQJ984",
        "title": "CREDERE IN UN MONDO MIGLIORE! 💪🏼 NICOLA GRATTERI passa dal BSMT!",
        "channel": "The BSMT by Gianluca Gazzoli",
        "duration": 6488,
        "hooks": [
            {
                "start": "24:08",
                "end": "24:17",
                "name": "Cinque giorni di tempo per occultare le prove",
                "quote": "Oggi non troviamo nulla: tu alla fine dai cinque giorni di tempo a questa persona.",
                "topic": "Leggi che favoriscono indagati e criminali garantendo loro tempo per scappare o occultare prove.",
            },
            {
                "start": "33:30",
                "end": "33:41",
                "name": "Mafie sempre meno violente e sempre più finanziarie",
                "quote": "Ci somigliano sempre più, sono sempre meno violente e riescono a mimetizzarsi facilmente nel mondo della finanza.",
                "topic": "Mafia dei colletti bianchi, finanza e infiltrazione nei piani alti dell'economia.",
            },
            {
                "start": "34:30",
                "end": "34:43",
                "name": "L'avviluppamento dei potenti e le loro debolezze",
                "quote": "Inizia l'avviluppamento nei confronti della persona potente, specie se la persona potente ha delle debolezze.",
                "topic": "Ricatti ai potenti, lobby e aggancio mafioso delle figure chiave dello Stato.",
            },
            {
                "start": "34:57",
                "end": "35:15",
                "name": "La società si vende per cinque o diecimila euro",
                "quote": "Oggi non hanno bisogno di uccidere perché la società negli ultimi decenni si vende per cinque o diecimila euro.",
                "topic": "Corruzione sistemica di funzionari, professionisti e amministratori.",
            },
        ],
    },
    {
        "video_id": "NVtQAg7VK74",
        "title": "Gratteri: violenza giovanile causata da analfabetismo",
        "channel": "",
        "duration": None,
        "hooks": [
            {
                "start": "09:15",
                "end": "09:27",
                "name": "Un decreto legge per sessanta minorenni",
                "quote": "Per sessanta minorenni abbiamo fatto un decreto legge e l'abbiamo raccontato come la soluzione di tutti i problemi.",
                "topic": "Propaganda politica e leggi-spot inefficaci.",
            },
            {
                "start": "20:34",
                "end": "20:54",
                "name": "Il 51% crede nella magistratura, il 12% nella politica",
                "quote": "Il 51% degli italiani ha fiducia nella magistratura, mentre il 12% ha fiducia nella politica: chi ha il 12% critica chi ha il 51%.",
                "topic": "Delegittimazione politica e crollo di credibilità delle istituzioni.",
            },
            {
                "start": "21:51",
                "end": "22:04",
                "name": "Pugno duro sui reati di strada, garantismo sui colletti bianchi",
                "quote": "Usano il pugno duro per i reati di strada e poi diventano iper-garantisti nei confronti dei colletti bianchi e dei centri di potere.",
                "topic": "Doppio standard della giustizia: legge severa coi deboli e impunità per i centri di potere.",
            },
        ],
    },
]


def to_seconds(value: str) -> int:
    """Parse MM:SS or HH:MM:SS into whole seconds."""
    parts = [int(p) for p in value.split(":")]
    if len(parts) == 2:
        return parts[0] * 60 + parts[1]
    if len(parts) == 3:
        return parts[0] * 3600 + parts[1] * 60 + parts[2]
    raise ValueError(f"invalid timestamp: {value!r}")


def main() -> None:
    PAYLOADS.mkdir(parents=True, exist_ok=True)
    manifest = {
        "generated_for": "Saggezza/Gratteri",
        "destination_folder_id": DESTINATION["folder_id"],
        "generate": {
            "strategy": "verify",
            "require_transcript_ready": True,
            "keep_audio": True,
            "write_summary": True,
        },
        "videos": [],
        "totals": {"videos": 0, "clips": 0, "duration_seconds": 0},
    }
    seen_windows: set[tuple[str, int, int]] = set()
    seen_names: set[str] = set()

    for video in VIDEOS:
        vid = video["video_id"]
        segments = []
        expected_ids = []
        for hook in video["hooks"]:
            start_s = to_seconds(hook["start"])
            end_s = to_seconds(hook["end"])
            duration = end_s - start_s
            if not 4 <= duration <= 60:
                raise SystemExit(f"{vid} {hook['start']}-{hook['end']}: duration {duration}s outside 4..60s")
            if video["duration"] is not None and end_s > video["duration"]:
                raise SystemExit(f"{vid}: end {end_s}s beyond video duration {video['duration']}s")
            key = (vid, start_s, end_s)
            if key in seen_windows:
                raise SystemExit(f"{vid}: duplicate window {hook['start']}-{hook['end']}")
            if hook["name"] in seen_names:
                raise SystemExit(f"duplicate segment name: {hook['name']}")
            seen_windows.add(key)
            seen_names.add(hook["name"])
            clip_id = f"yt_{vid}_{start_s}_{end_s}_v1"
            expected_ids.append(clip_id)
            segments.append(
                {
                    "start": hook["start"],
                    "end": hook["end"],
                    "name": hook["name"],
                    "category": CATEGORY,
                    "source_title": video["title"],
                    "source_channel": video["channel"],
                    "summary": f"{hook['quote']} {hook['topic']}",
                    "topics": [hook["topic"]],
                    "tags": TAGS + [hook["name"].lower()],
                    "mentioned_people": PEOPLE,
                }
            )

        payload = {
            "url": f"https://www.youtube.com/watch?v={vid}",
            "strategy": "verify",
            "concurrency": 3,
            "force_keyframes": True,
            "keep_audio": True,
            "write_summary": True,
            "require_transcript_ready": True,
            "destination": DESTINATION,
            "segments": segments,
        }
        out = PAYLOADS / f"{vid}.process.json"
        out.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

        total_duration = sum(to_seconds(s["end"]) - to_seconds(s["start"]) for s in segments)
        manifest["videos"].append(
            {
                "video_id": vid,
                "title": video["title"],
                "channel": video["channel"],
                "source_duration_seconds": video["duration"],
                "clip_count": len(segments),
                "clip_seconds": total_duration,
                "expected_clip_ids": expected_ids,
                "payload": str(out.relative_to(HERE)),
            }
        )
        manifest["totals"]["videos"] += 1
        manifest["totals"]["clips"] += len(segments)
        manifest["totals"]["duration_seconds"] += total_duration

    (HERE / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    print(json.dumps(manifest["totals"], indent=1))
    for v in manifest["videos"]:
        print(f'{v["video_id"]}: {v["clip_count"]} clip / {v["clip_seconds"]}s -> {v["payload"]}')


if __name__ == "__main__":
    main()
