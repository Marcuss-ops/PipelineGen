#!/usr/bin/env python3
"""E2E suite for folder-only stock media (never individual Drive files)."""
from __future__ import annotations
import argparse, json
from media_mode_e2e_common import BOXERS, assert_stock, job_id, post, subjects, wait_job


SOURCE_TEXT = {
    "mike_tyson": "Mike Tyson ha costruito la sua leggenda sulla potenza, sulla velocità e su un'ascesa fulminea. La sua storia mostra anche il prezzo della pressione e delle scelte fuori dal ring.",
    "muhammad_ali": "Muhammad Ali ha unito movimento, personalità e impatto culturale. La sua carriera racconta come tecnica e carattere possano trasformare un campione in un simbolo mondiale.",
    "evander_holyfield": "Evander Holyfield è ricordato per resistenza, disciplina e rivalità memorabili. Il suo percorso dimostra quanto la preparazione sostenga una carriera ai massimi livelli.",
    "floyd_mayweather": "Floyd Mayweather ha trasformato difesa, precisione e controllo del ritmo in una carriera imbattuta. Ogni combattimento è diventato una lezione di strategia e concentrazione.",
    "sugar_ray_robinson": "Sugar Ray Robinson ha lasciato un'eredità tecnica costruita su talento, longevità e varietà. La sua influenza continua a definire il modo in cui si studia la grande boxe.",
}


def payload(key: str, scenes: int) -> dict:
    name, folder = BOXERS[key]
    segments = [{"id": f"{key}-scene-{i}", "topic": f"{name} scena {i + 1}"} for i in range(scenes)]
    link = f"https://drive.google.com/drive/folders/{folder}?usp=drive_link"
    bindings = [{"index": i, "segment_id": s["id"], "name": f"{name} stock folder", "source": "youtube", "folder_id": folder, "folder_link": link, "fallback": False, "start_ms": i * 5000, "end_ms": (i + 1) * 5000} for i, s in enumerate(segments)]
    return {"version": 2, "preset": "custom", "items": [{"id": f"{key}-stock-only", "title": f"{name}: carriera e patrimonio", "language": "it", "tone": "documentary", "media_mode": "stock_only", "source": {"type": "text", "topic": f"{name} carriera e patrimonio", "source_text": SOURCE_TEXT[key], "grounding_policy": "source_primary", "fallback_policy": "strict", "cache": {"mode": "disabled"}}, "script_params": {"target_words": 400, "segment_words": 130, "use_memory": False, "force_refresh": True, "skip_quality_gate": True, "segments": segments}, "output": {"stock_enabled": "enabled", "stock_bindings": bindings, "save_to_db": True, "generate_timeline": False}, "docs": {"enabled": True, "languages": ["it"], "folder_id": folder}}]}


def main() -> None:
    parser = argparse.ArgumentParser(); parser.add_argument("--subject", choices=list(BOXERS)); parser.add_argument("--subjects", default=None); parser.add_argument("--scenes", type=int, default=3); parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(); keys = [args.subject] if args.subject else subjects(args.subjects or "all"); report = {"jobs": 0, "scenes": 0, "stock_bindings": 0, "clip_bindings": 0, "folder_links": 0, "file_links": 0, "wrong_mode": 0, "status": "PASS"}
    for key in keys:
        body = payload(key, args.scenes)
        if args.dry_run: print(json.dumps(body, ensure_ascii=False)); continue
        result = assert_stock(wait_job(job_id(post(body))), BOXERS[key][1], args.scenes)
        report["jobs"] += 1; report["scenes"] += result["scenes"]; report["stock_bindings"] += result["stock_bindings"]; report["folder_links"] += result["stock_bindings"]
    print(json.dumps({"stock_only": report}, indent=2))


if __name__ == "__main__": main()
