#!/usr/bin/env python3
"""
test_overlay_prepare_certification.py — minimal regression guard for the
audit script. Replays the certification against the live artifact set and
pins the verdict +14 blocking gates, so any drift in the broker record or
in the verifier surfaces immediately under go test ./internal/platform/...

Run:
    python3 tests/operational/test_overlay_prepare_certification.py
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


# tests/operational/<this>.py -> parents[0]=operational, parents[1]=tests,
# parents[2]=refactored. The verifier lives inside the refactored module.
REFACTORED_ROOT = Path(__file__).resolve().parents[2]
SCRIPT = REFACTORED_ROOT / "scripts/overlay-cert/verify_overlay_prepare_live.py"
FULL = REFACTORED_ROOT / "artifacts/verify/michael-jordan-overlay-prepare-retry.full.json"
PAYLOAD = REFACTORED_ROOT / "artifacts/verify/michael-jordan-overlay-prepare.payload.json"
DISPATCH = REFACTORED_ROOT / "artifacts/verify/michael-jordan-overlay-prepare-retry.dispatch.json"


class OverlayPrepareCertificationTest(unittest.TestCase):
    def test_live_artifact_certifies_overlay_prepare(self) -> None:
        self.assertTrue(SCRIPT.exists(), f"missing verifier: {SCRIPT}")
        self.assertTrue(FULL.exists(), f"missing live full.json: {FULL}")
        self.assertTrue(PAYLOAD.exists(), f"missing live payload: {PAYLOAD}")
        self.assertTrue(DISPATCH.exists(), f"missing live dispatch: {DISPATCH}")

        with tempfile.TemporaryDirectory() as tmpdir:
            out_path = Path(tmpdir) / "cert.json"
            proc = subprocess.run(
                [
                    sys.executable,
                    str(SCRIPT),
                    "--full", str(FULL),
                    "--payload", str(PAYLOAD),
                    "--dispatch", str(DISPATCH),
                    "--objectstore", "http://localhost:9000",
                    "--out", str(out_path),
                ],
                check=False,
                capture_output=True,
                text=True,
                timeout=60,
            )
            self.assertEqual(proc.returncode, 0, msg=f"verifier failed:\nSTDOUT:\n{proc.stdout}\nSTDERR:\n{proc.stderr}")
            cert = json.loads(out_path.read_text())
            self.assertEqual(cert["verdict"], "PASS", msg=json.dumps(cert, indent=2))

            gates = {g["gate"]: g for g in cert["gates"]}
            # 14 blocking gates must pass (G1b is informational only).
            expected_passes = {
                "G1_id_carries_prepare_discriminator",
                "G2_job_type_overlay_prepare",
                "G3_schema_version_renderinggen_overlay_prepare_v1",
                "G4_plan_id_non_empty",
                "G5_video_id_non_empty",
                "G6_width_positive",
                "G7_height_positive",
                "G8_fps_positive",
                "G9_intents_non_empty",
                "G10_intents_timing_state_PENDING",
                "G11_state_completed",
                "G12_worker_recorded",
                "G13_observability_timestamps",
                "G14_payload_asset_refs_carry_sha256_and_url",
                "G15_broker_assets_dedup_by_intent_hash",
                "G16_objectstore_reachable_at_intent_hash",
            }
            for gate_id in expected_passes:
                self.assertIn(gate_id, gates, f"gate {gate_id} not produced")
                self.assertEqual(gates[gate_id]["status"], "PASS",
                                 f"gate {gate_id} status should be PASS, observed={gates[gate_id]['status']}")

            # G1b must be present and FAIL (informational drift) — pin the
            # exactly-known drift so future changes surface loudly.
            self.assertIn("G1b_strict_prepare_plan_id_shape", gates)
            self.assertEqual(gates["G1b_strict_prepare_plan_id_shape"]["status"], "FAIL")

            # Pin the live artifact shape we depend on.
            self.assertEqual(cert["schema_version"], "renderinggen.overlay-prepare.v1")
            self.assertEqual(cert["job_type"], "overlay.prepare")


if __name__ == "__main__":
    unittest.main()
