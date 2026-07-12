#!/usr/bin/env python3
import logging
import os
import sys

from playwright.sync_api import sync_playwright
from storage_utils import save_storage_snapshot, storage_looks_usable

# ── logger setup ─────────────────────────────────────────────────────
# godlike/07 NO-FAKE-AVAILABILITY: configure root logger at module
# load time with format='%(message)s' so callers (Google Slides login
# helper CLI invocation + downstream automation that captures stdout
# via subprocess) see plain-text diagnostic output WITHOUT the
# `INFO:root:` noise that the default `%(levelname)s:%(name)s:%(message)s`
# adds — keeping the CLI UX byte-equivalent with the pre-migration
# print()-only surface.
logger = logging.getLogger(__name__)
logging.basicConfig(
    level=logging.INFO,
    format="%(message)s",
    stream=sys.stdout,
)

MASTER_STORAGE = "data/google_slides_storage.json"
PROFILE_DIR = "data/google_slides_session_profile"


def main():
    logger.info("==================================================================")
    logger.info(" Google Slides Login Helper")
    logger.info("==================================================================")
    logger.info("Avvio del browser in corso...")

    with sync_playwright() as p:
        os.makedirs(PROFILE_DIR, exist_ok=True)
        context = p.chromium.launch_persistent_context(
            PROFILE_DIR,
            headless=False,
            args=[
                "--disable-blink-features=AutomationControlled",
                "--no-sandbox",
                "--disable-setuid-sandbox",
            ],
        )
        page = context.new_page()
        logger.info("Navigazione su https://slides.new...")
        page.goto("https://slides.new")

        # ── TTY prompt block ───────────────────────────────────────
        # MUST stay as bare print() (NOT logger.info) because the
        # subsequent blocking `input()` call requires an UNBUFFERED
        # stdout write of the prompt so the operator sees the
        # instruction immediately before the read; threading the
        # prompt through logging can interleave with other handlers
        # and break the synchronous TTY UX. Per the user directive
        # "keeping CLI UX via stdout writer adapters" the bare print
        # IS the adapter here (Python's print() writes to sys.stdout
        # by default; the input() prompt hangs on that exact fd).
        # ──────────────────────────────────────────────────────────
        print()
        print("--> EFFETTUA IL LOGIN nella finestra del browser che si è aperta.")
        print("--> Una volta completato il login e caricata la pagina di Google Slides,")
        print("    torna qui sul terminale e premi INVIO per salvare la sessione.")

        try:
            input()
        except KeyboardInterrupt:
            logger.warning("\nOperazione annullata.")
            context.close()
            sys.exit(1)

        storage = context.storage_state()

        if "accounts.google.com" in page.url or not storage_looks_usable(storage):
            logger.error(
                "Sessione non salvata: il login non risulta completato o lo snapshot è vuoto."
            )
            context.close()
            sys.exit(1)

        # Save both to MASTER_STORAGE and profile storage, preserving backups.
        save_storage_snapshot(MASTER_STORAGE, storage, backup_path=f"{MASTER_STORAGE}.backup")

        profile_storage_path = f"{MASTER_STORAGE}.profile_0"
        save_storage_snapshot(profile_storage_path, storage, backup_path=f"{profile_storage_path}.backup")

        logger.info("==================================================================")
        logger.info("✅ Sessione salvata con successo!")
        logger.info(f"   - {MASTER_STORAGE}")
        logger.info(f"   - {profile_storage_path}")
        logger.info("==================================================================")
        context.close()


if __name__ == "__main__":
    main()
