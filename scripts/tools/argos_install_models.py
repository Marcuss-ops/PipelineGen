#!/usr/bin/env python3
"""
scripts/tools/argos_install_models.py — install Argos Translate language
models for the multilingual registry.

Installs direct en->L and L->en packages for every pipeline language so
that any (source -> target) pair in the registry can be translated, either
directly or via Argos' automatic English pivot (X -> en -> Y).

Usage:
    python3 scripts/tools/argos_install_models.py
    ARGOS_LANGUAGES="it,pl,ru,de,es,pt,fr,tr,id" python3 scripts/tools/argos_install_models.py
    ARGOS_PACKAGES_DIR=/somewhere python3 scripts/tools/argos_install_models.py

Requires: pip install argostranslate (see scripts/requirements-argos.txt).
Models are downloaded from the Argos package index (~100-300 MB each) and
installed into ARGOS_PACKAGES_DIR, which defaults to the REPO-LOCAL
data/argos-packages so the models stay on the deployment volume instead of
disappearing into the operator's home directory.

CONTRACT (one owner of "where the models live"):
  * the directory this script installs into is exactly the directory the
    sidecar is started with (Go adapter -> ARGOS_PACKAGES_DIR env var);
  * the variable name is ARGOS_PACKAGES_DIR (PLURAL) — that is the name
    argostranslate >= 1.9 reads (scripts/bridges/argos_bridge reads it
    indirectly through argostranslate.settings). ARGOS_PACKAGE_DIR (singular)
    is silently ignored by the library, which is how a "successful" install
    still left the sidecar answering "no model for en->it".
"""

import os
import sys
from pathlib import Path

# Repo root = this file's grandparent (scripts/tools/argos_install_models.py).
REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_PACKAGES_DIR = REPO_ROOT / "data" / "argos-packages"


def _resolve_packages_dir() -> Path:
    """Resolve and export ARGOS_PACKAGES_DIR BEFORE argostranslate is imported.

    argostranslate.settings reads the variable at import time, so setting it
    after the import (the previous behaviour, with the singular name) has no
    effect at all.
    """
    configured = os.environ.get("ARGOS_PACKAGES_DIR", "").strip()
    packages_dir = Path(configured) if configured else DEFAULT_PACKAGES_DIR
    packages_dir.mkdir(parents=True, exist_ok=True)
    os.environ["ARGOS_PACKAGES_DIR"] = str(packages_dir)
    return packages_dir


def _target_languages():
    raw = os.environ.get("ARGOS_LANGUAGES", "").strip()
    if raw:
        return [c.strip().lower() for c in raw.split(",") if c.strip()]
    # Canonical multilingual registry (config/multilingual.yaml), minus the
    # source pivot language "en" (installed as the pivot target below).
    return ["it", "pl", "ru", "de", "es", "pt", "fr", "tr", "id"]


def main():
    packages_dir = _resolve_packages_dir()
    print("Argos packages dir: %s" % packages_dir)

    try:
        import argostranslate.package as package
        import argostranslate.settings as settings
    except ImportError:
        print("argostranslate not installed. Run: pip3 install argostranslate", file=sys.stderr)
        return 1

    # Fail loudly if the library resolved a different directory than this
    # script exports: a silent mismatch is exactly how the sidecar ends up
    # seeing no models after a green install.
    if Path(settings.package_data_dir).resolve() != packages_dir.resolve():
        print(
            "FATAL: argostranslate resolved package_data_dir=%s but this script installs into %s"
            % (settings.package_data_dir, packages_dir),
            file=sys.stderr,
        )
        return 1

    langs = _target_languages()
    print("Updating Argos package index...")
    package.update_package_index()
    available = package.get_available_packages()

    # Install en->L and L->en for each target so Argos can pivot any
    # registry source through English.
    pairs = []
    for lang in langs:
        pairs.append(("en", lang))
        pairs.append((lang, "en"))

    failed = []
    for from_code, to_code in pairs:
        print("Installing %s->%s ..." % (from_code, to_code))
        matches = [
            p for p in available
            if p.from_code == from_code and p.to_code == to_code
        ]
        if not matches:
            print("  SKIP (%s->%s): no package available" % (from_code, to_code), file=sys.stderr)
            failed.append("%s->%s" % (from_code, to_code))
            continue
        try:
            matches[0].install()  # download + install_from_path + cleanup
        except Exception as exc:  # noqa: BLE001
            print("  FAILED (%s->%s): %s" % (from_code, to_code, exc), file=sys.stderr)
            failed.append("%s->%s" % (from_code, to_code))
            continue

    if failed:
        print("Done with %d missing pair(s): %s" % (len(failed), ", ".join(failed)), file=sys.stderr)
        return 2
    print("Done: all language models installed into %s." % packages_dir)
    return 0


if __name__ == "__main__":
    sys.exit(main())
