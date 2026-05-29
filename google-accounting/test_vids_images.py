"""
Test script per Vids Image Generation.
Runnalo direttamente: python test_vids_images.py
Vedrai il browser Chrome aprirsi e tutta la console log.
"""

import asyncio
import logging
import sys
from pathlib import Path

# Setup logging su console
Path("logs").mkdir(exist_ok=True)
logging.basicConfig(
    level=logging.DEBUG,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    handlers=[
        logging.StreamHandler(sys.stdout),
        logging.FileHandler("logs/test_vids_images.log"),
    ],
)

from automation.vids import GoogleVidsAutomation, generate_vids_image_v1


import hashlib

def get_file_hash(path_str):
    if not path_str:
        return None
    p = Path(path_str)
    if not p.exists():
        return None
    return hashlib.md5(p.read_bytes()).hexdigest()

async def run_single_test(idx, prompt):
    print(f"[START Task {idx}] Prompt: {prompt}")
    try:
        result = await generate_vids_image_v1(
            video_id="1QOY4nINvvf5kOB4uG50DrrpLa92KIqrhglvvyriptC4",
            prompt=prompt,
            account="favamassimo",
            headless=True,
        )
        file_hash = get_file_hash(result)
        print(f"[FINISHED Task {idx}] Result: {result} | Hash: {file_hash}")
        return {"idx": idx, "prompt": prompt, "result": result, "hash": file_hash}
    except Exception as e:
        print(f"[ERR Task {idx}] Failed: {e}")
        return {"idx": idx, "prompt": prompt, "result": None, "hash": None, "err": str(e)}

async def main():
    prompts = [
        "un gatto arancione che gioca con un gomitolo, stile cartoon, 16:9",
        "un razzo spaziale che decolla verso la luna di notte, fotorealistico, 16:9",
        "una tazza di caffe fumante su un tavolo di legno in una baita, 16:9",
        "un auto sportiva futuristica rossa che sfreccia in una citta cyberpunk, 16:9"
    ]

    print("=" * 60)
    print("TEST GENERAZIONE IMMAGINI VIDS IN PARALLELO (4 SESSIONI)")
    print("=" * 60)
    
    tasks = [run_single_test(i, prompts[i]) for i in range(4)]
    results = await asyncio.gather(*tasks)
    
    print("\n" + "=" * 60)
    print("RISULTATI FINALI:")
    print("=" * 60)
    
    hashes = []
    success_count = 0
    for r in results:
        status = "[OK]" if r["result"] else "[FAIL]"
        if r["result"]:
            success_count += 1
            hashes.append(r["hash"])
        print(f"Task {r['idx']} {status}: Prompt='{r['prompt']}' -> Path={r['result']} | MD5={r['hash']}")
        
    print("=" * 60)
    if success_count == len(results):
        unique_hashes = len(set(hashes))
        print(f"Tutte le {success_count} immagini sono state generate con successo!")
        print(f"Numero di hash unici: {unique_hashes} su {success_count}")
        if unique_hashes == success_count:
            print("CONFERMATO: Ogni sessione ha scaricato un'immagine unica e diversa! 🎉")
        else:
            print("ATTENZIONE: Rilevati hash duplicati! Alcune immagini potrebbero essere identiche.")
    else:
        print(f"Generazione parziale: {success_count}/4 riuscite.")
    print("=" * 60)

if __name__ == "__main__":
    asyncio.run(main())
