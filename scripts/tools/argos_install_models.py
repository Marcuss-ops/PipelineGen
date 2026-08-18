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

Requires: pip install argostranslate (see scripts/requirements-argos.txt).
Models are downloaded from the Argos package index (~100-300 MB each) and
installed into the default package directory (overridable with
ARGOS_PACKAGE_DIR).
"""

import os
import sys


def _target_languages():
    raw = os.environ.get("ARGOS_LANGUAGES", "").strip()
    if raw:
        return [c.strip().lower() for c in raw.split(",") if c.strip()]
    # Canonical multilingual registry (config/multilingual.yaml), minus the
    # source pivot language "en" (installed as the pivot target below).
    return ["it", "pl", "ru", "de", "es", "pt", "fr", "tr", "id"]


def main():
    try:
        import argostranslate.package as package
    except ImportError:
        print("argostranslate not installed. Run: pip3 install argostranslate", file=sys.stderr)
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
    print("Done: all language models installed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
