#!/usr/bin/env python3
"""Assembly certification audit — evidence for the transform_assemble.rs REMOVAL GATE.

The Rust assembly gate skips the contract_id check for certifications that
carry none ("legacy certification"). That skip is removable ONLY once every
durable certification surface provably holds zero contract-less certs
("compilation alone is not sufficient" — observability-measurement-matrix).

Rust gate semantics being audited (pipelinegen-muscles/src/transform_assemble.rs):
  skip the contract_id check  IFF  stream_signature_sha256 present
                              OR  contract_id == VELOX_ASSEMBLY_READY_V1.

Two certification surface shapes exist and are distinguished via the
offenders' "matched_by" field:
  - copy_certification : an explicit certification envelope was found;
    a missing contract_id here is unambiguous.
  - copy_eligible      : an artifact eligibility flag whose surface does not
    carry any contract identity column (the durable Go RenderArtifact has no
    contract_id/stream_signature field) — these prove the STORED side holds
    no contract fact even when the wire did.

Evidence sources audited here (refactored SQLite side; the Postgres half lives
in RenderingGen/queue/ops/render_artifacts_contract_audit.sql):

  1. run_observability.report_json  — canonical RunReport operations: a
     certified copy/overlay render operation carries its identity in
     OperationInfo.MetadataJSON (contract_id / stream_signature_sha256).
  2. jobs.result_json               — durable job results: certification
     summaries surface alongside render analytics (render_ms/encode_ms).

Exit codes:
  0 = gate UNLOCKED  — >=1 certification verified, zero contract-less found
  1 = gate LOCKED    — contract-less certifications exist (IDs are printed)
  2 = NO EVIDENCE    — zero certifications found; never treat as unlocked

Usage:
  python3 ops/maintenance/audit_assembly_certifications.py \
      [--obs-db PATH] [--jobs-db PATH]

Defaults follow ops/benchmarks/20260826-baselines/extract.py conventions:
  --obs-db  data/observability/api_requests.db.sqlite
  --jobs-db data/media/media.db.sqlite
"""
import argparse
import json
import os
import sqlite3
import sys

EXPECTED_CONTRACT = "VELOX_ASSEMBLY_READY_V1"


def parse_doc(raw):
    """Parse one JSON document; returns {} on absent/unparsable input."""
    if not raw:
        return {}
    try:
        doc = json.loads(raw)
    except json.JSONDecodeError:
        return {}
    return doc if isinstance(doc, dict) else {}


def walk_dicts(node):
    """Yield every dict in a JSON tree."""
    if isinstance(node, dict):
        yield node
        for value in node.values():
            yield from walk_dicts(value)
    elif isinstance(node, list):
        for item in node:
            yield from walk_dicts(item)


def looks_like_certification(node):
    """True when a result-tree node talks about copy certification."""
    if isinstance(node.get("copy_certification"), dict):
        return True
    return bool(node.get("copy_eligible"))


def audit_run_reports(obs_db_path):
    """(checked, offenders) over run_observability.report_json operations."""
    offenders, checked = [], 0
    con = sqlite3.connect(obs_db_path)
    try:
        rows = con.execute("SELECT report_json FROM run_observability").fetchall()
    finally:
        con.close()
    for (raw,) in rows:
        report = parse_doc(raw)
        for op in report.get("operations", []):
            meta = parse_doc(op.get("metadata_json"))
            has_sig = bool(meta.get("stream_signature_sha256"))
            contract = meta.get("contract_id")
            if not has_sig and contract is None and "contract" not in meta:
                # Operation without certification metadata — out of scope.
                continue
            checked += 1
            if not has_sig and contract != EXPECTED_CONTRACT:
                offenders.append({
                    "source": "run_observability",
                    "run_id": report.get("RunID") or report.get("run_id"),
                    "operation": "{}/{}".format(op.get("stage"), op.get("operation")),
                    "contract_id": contract,
                    "stream_signature_sha256": None,
                })
    return checked, offenders


def audit_job_results(jobs_db_path):
    """(checked, offenders) over jobs.result_json certification summaries."""
    offenders, checked = [], 0
    con = sqlite3.connect(jobs_db_path)
    try:
        rows = con.execute(
            "SELECT id, result_json FROM jobs WHERE result_json IS NOT NULL"
        ).fetchall()
    finally:
        con.close()
    for job_id, raw in rows:
        result = parse_doc(raw)
        for node in walk_dicts(result):
            if not looks_like_certification(node):
                continue
            explicit_cert = isinstance(node.get("copy_certification"), dict)
            cert = node.get("copy_certification") or {}
            contract = cert.get("contract_id") or node.get("contract_id")
            signature = (
                cert.get("stream_signature_sha256")
                or node.get("stream_signature_sha256")
            )
            checked += 1
            if not signature and contract != EXPECTED_CONTRACT:
                offenders.append({
                    "source": "jobs.result_json",
                    "job_id": job_id,
                    "kind": node.get("kind") or node.get("artifact_kind"),
                    "matched_by": "copy_certification" if explicit_cert else "copy_eligible",
                    "contract_id": contract,
                    "stream_signature_sha256": "<present>" if signature else None,
                })
            break  # one certification sample per result envelope
    return checked, offenders


def main():
    parser = argparse.ArgumentParser(
        description="Audit SQLite certification surfaces for the assembly "
                    "contract_id REMOVAL GATE."
    )
    parser.add_argument("--obs-db", default=os.path.join(
        "data", "observability", "api_requests.db.sqlite"))
    parser.add_argument("--jobs-db", default=os.path.join(
        "data", "media", "media.db.sqlite"))
    args = parser.parse_args()

    total_checked, offenders = 0, []

    if os.path.exists(args.obs_db):
        n, off = audit_run_reports(args.obs_db)
        print("[1/2] run_observability.report_json : {} certifications verified".format(n))
        total_checked += n
        offenders += off
    else:
        print("[1/2] run_observability.report_json : DB non trovato ({}) — skip"
              .format(args.obs_db))

    if os.path.exists(args.jobs_db):
        n, off = audit_job_results(args.jobs_db)
        print("[2/2] jobs.result_json              : {} certifications verified".format(n))
        total_checked += n
        offenders += off
    else:
        print("[2/2] jobs.result_json              : DB non trovato ({}) — skip"
              .format(args.jobs_db))

    print()
    print("superfici di certificazione verificate : {}".format(total_checked))
    print("certificazioni SENZA contract_id       : {}".format(len(offenders)))

    if total_checked == 0:
        print("\nNO EVIDENCE — nessuna certificazione verificata: il gate resta "
              "LOCKED finche' l'audit non gira sui dati reali.")
        return 2

    if offenders:
        print("\ncertificazioni da rigenerare o backfillare:")
        for item in offenders:
            print(json.dumps(item, sort_keys=True))
        print("\nGATE ASSEMBLY CONTRACT_ID: LOCKED — il ramo legacy in "
              "transform_assemble.rs deve restare.")
        return 1

    print("\nGATE ASSEMBLY CONTRACT_ID: UNLOCKED — zero certificazioni senza "
          "contract_id; il check-skip contract-less in transform_assemble.rs "
          "e' rimovibile. NOTA: produci anche l'evidenza Postgres "
          "(RenderingGen/queue/ops/render_artifacts_contract_audit.sql) prima "
          "di cancellare il ramo.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
