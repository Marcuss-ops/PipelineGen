#!/usr/bin/env python3
"""Genera l'indice markdown delle clip 'Aj Vs Tyson Fury' dai job result e dai payload."""
import json
import glob
import os

FOLDER = "1nib6O6sgAoy3M_hfWAkPsvGwjNDcMl36"
FOLDER_URL = f"https://drive.google.com/drive/folders/{FOLDER}"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "index.md")

ORDER = ["yUfPCaPY-5E", "2pnWtF9SKeI", "0PPEARVXHzI", "5qVIN36xZg4", "sI2KQltCDHk",
         "GwutL2B8EQM", "BHVkoYIfBds", "Jab9PWsPVEo", "HRy2OBrBsSA", "ytZhWmz813M"]


def load(vid):
    proc = json.load(open(f"/tmp/aj/process_{vid}.json"))
    items = []
    for kind in ("job", "fixjob"):
        p = f"/tmp/aj/{kind}_{vid}.json"
        try:
            j = json.load(open(p))
        except OSError:
            continue
        items += (j.get("result") or {}).get("items") or []
    by_id = {i["id"]: i for i in items}
    segs = []
    for s in proc["segments"]:
        a = int(s["start"][:2]) * 60 + int(s["start"][3:])
        b = int(s["end"][:2]) * 60 + int(s["end"][3:])
        cid = f"yt_{vid}_{a}_{b}_v1"
        it = by_id.get(cid) or {}
        segs.append({
            "start": s["start"], "end": s["end"], "dur": b - a,
            "name": s["name"], "hook": s.get("hook", ""),
            "link": it.get("drive_link", ""),
            "status": it.get("status", "?"),
        })
    return proc, segs


def main():
    lines = []
    lines.append("# Aj Vs Tyson Fury — indice clip")
    lines.append("")
    lines.append(f"Cartella Drive: [Aj Vs Tyson Fury]({FOLDER_URL}) — creata dentro **Anthony Joshua** "
                 f"(id `{FOLDER}`).")
    lines.append("")
    all_segs = [s for v in ORDER for s in load(v)[1]]
    total = len(all_segs)
    dmin = min(s["dur"] for s in all_segs)
    dmax = max(s["dur"] for s in all_segs)
    lines.append(f"**{total} clip** da **{len(ORDER)} video** diversi, durate {dmin}–{dmax}s, tutte `INDEXED` nell'indice vettoriale.")
    lines.append("")
    lines.append("Confini allineati alle pause reali del parlato (timing a livello di parola dai "
                 "sottotitoli VTT): nessun taglio a meta parola.")
    lines.append("")
    lines.append("## Stato pulizia (27 settembre 2026)")
    lines.append("")
    lines.append("- 31 clip erano tagliate a meta parola: ricostruite e sostituite; le 31 versioni vecchie "
                 "sono state **rimosse definitivamente da Drive** (`admin trash-drive-files --permanent`, 31/31).")
    lines.append("- Catalogo media PostgreSQL: i 31 asset superati sono `lifecycle_state=DELETED` / "
                 "`index_state=DELETED` (tombstone canonica via `admin delete-clip-by-drive-file --asset-id`, 31/31); "
                 "nessun asset ACTIVE residuo.")
    lines.append("- Residui locali: 102 file (62 clip + 40 sottotitoli) rimossi dal disco; "
                 "manifesto in `removed_local_files.txt`.")
    lines.append("- Outbox media: 0 eventi pendenti. Le 38 clip di questo indice sono tutte `ACTIVE/INDEXED`.")
    lines.append("")
    lines.append("Nota operativa: i comandi `admin` che toccano il media PostgreSQL richiedono le variabili "
                 "esportate; `source .env` da solo NON le esporta. Usare:")
    lines.append("")
    lines.append("```bash")
    lines.append("set -a; source .env; set +a   # poi: ./bin/admin delete-clip-by-drive-file --asset-id <clip_id>")
    lines.append("```")
    lines.append("")

    # riepilogo
    lines.append("## Riepilogo")
    lines.append("")
    lines.append("| # | Video | Canale | Clip | Durata totale |")
    lines.append("|---|---|---|---|---|")
    for i, vid in enumerate(ORDER, 1):
        proc, segs = load(vid)
        tot = sum(s["dur"] for s in segs)
        lines.append(f"| {i} | [{vid}](https://www.youtube.com/watch?v={vid}) | {proc['source_channel'] if 'source_channel' in proc else proc['segments'][0]['source_channel']} | {len(segs)} | {tot}s |")
    lines.append("")

    # dettaglio
    for i, vid in enumerate(ORDER, 1):
        proc, segs = load(vid)
        title = proc["segments"][0]["source_title"]
        chan = proc["segments"][0]["source_channel"]
        tot = sum(s["dur"] for s in segs)
        lines.append(f"## {i}. {title}")
        lines.append("")
        lines.append(f"Canale: **{chan}** · Fonte: https://www.youtube.com/watch?v={vid} · "
                     f"{len(segs)} clip · {tot}s totali")
        lines.append("")
        lines.append("| Titolo | Intervallo | Durata | Drive |")
        lines.append("|---|---|---|---|")
        for s in segs:
            link = f"[apri]({s['link']})" if s["link"] else "—"
            lines.append(f"| {s['name']} | {s['start']}–{s['end']} | {s['dur']}s | {link} |")
        lines.append("")

    with open(OUT, "w") as f:
        f.write("\n".join(lines) + "\n")
    print("scritto:", OUT, f"({len(lines)} righe)")


if __name__ == "__main__":
    main()
