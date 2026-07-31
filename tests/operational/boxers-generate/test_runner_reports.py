#!/usr/bin/env python3
"""Black-box tests for runner report publication and polling races."""

from __future__ import annotations

import json
import os
import sqlite3
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parent
RUNNER = ROOT / "run.sh"
REGISTRY = ROOT / "fixtures" / "boxers_stock_registry.json"


class RunnerReportPublicationTest(unittest.TestCase):
    def test_stale_running_parent_full_is_not_published(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            db_path = root / "assets.sqlite"
            report_dir = root / "reports"
            fake_bin = root / "bin"
            fake_bin.mkdir()
            calls = root / "curl.calls"
            state = root / "full.count"
            fake_curl = fake_bin / "curl"
            fake_curl.write_text(
                """#!/usr/bin/env bash
set -euo pipefail
out=''
method=GET
url="${!#}"
while (($#)); do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -X) method="$2"; shift 2 ;;
    -w) shift 2 ;;
    *) shift ;;
  esac
done
printf '%s %s\n' "$method" "$url" >> "$CALLS_FILE"
if [[ "$method" == "POST" ]]; then
  body='{"job_id":"job-race"}'
  code=202
elif [[ "$url" == */api/jobs/job-race/full ]]; then
  count=0
  [[ -f "$FULL_COUNT_FILE" ]] && count=$(cat "$FULL_COUNT_FILE")
  count=$((count + 1))
  printf '%s' "$count" > "$FULL_COUNT_FILE"
  if (( count == 1 )); then
    body='{"status":"RUNNING","job":{"status":"RUNNING"}}'
  else
    body='{"status":"SUCCEEDED","job":{"status":"SUCCEEDED"},"result":{"data":{"items":[{"result":{"status":"SUCCEEDED","output":{"text":"SRC-TYSON-01 SRC-TYSON-02 SRC-TYSON-03 SRC-TYSON-04","specscene":{"scenes":[]}}}}]}}}'
  fi
  code=200
elif [[ "$url" == */api/jobs/job-race ]]; then
  body='{"status":"completed"}'
  code=200
else
  body='{}'
  code=404
fi
printf '%s' "$body" > "$out"
printf '%s' "$code"
""",
                encoding="utf-8",
            )
            fake_curl.chmod(0o755)

            registry = json.loads(REGISTRY.read_text(encoding="utf-8"))
            with sqlite3.connect(db_path) as connection:
                connection.execute(
                    "CREATE TABLE media_assets ("
                    "id TEXT PRIMARY KEY, lifecycle_state TEXT NOT NULL, "
                    "source TEXT NOT NULL, drive_link TEXT NOT NULL, name TEXT NOT NULL)"
                )
                for boxer in registry["boxers"].values():
                    for role, asset in boxer.get("assets", {}).items():
                        connection.execute(
                            "INSERT INTO media_assets VALUES (?, 'ACTIVE', 'youtube', ?, ?)",
                            (asset["asset_id"], asset["drive_link"], f"{boxer['subject']} {role}"),
                        )
                connection.commit()

            env = os.environ.copy()
            env.update(
                {
                    "VELOX_DB": str(db_path),
                    "BOXERS_VOICEOVER_FOLDER_ID": "test-folder",
                    "VELOX_ADMIN_TOKEN": "test-token",
                    "TARGET_SCENARIO": "01",
                    "BOXERS_REPORTS_DIR": str(report_dir),
                    "CALLS_FILE": str(calls),
                    "FULL_COUNT_FILE": str(state),
                    "PATH": f"{fake_bin}:{env['PATH']}",
                }
            )
            result = subprocess.run(
                ["bash", str(RUNNER)],
                cwd=ROOT.parent.parent.parent,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stdout + "\n" + result.stderr)
            self.assertGreaterEqual(int(state.read_text(encoding="utf-8")), 2)
            calls_text = calls.read_text(encoding="utf-8")
            self.assertIn("GET http://127.0.0.1:8000/api/jobs/job-race/full", calls_text)

            raw = report_dir / "raw" / "01_Source segments_job.json"
            verification = report_dir / "01_Source segments_verification_report.json"
            self.assertTrue(raw.exists(), f"reports={list(report_dir.rglob('*'))}; stdout={result.stdout}; stderr={result.stderr}")
            self.assertTrue(verification.exists(), f"reports={list(report_dir.rglob('*'))}; stdout={result.stdout}; stderr={result.stderr}")
            self.assertNotIn("RUNNING", raw.read_text(encoding="utf-8"))
            self.assertNotIn("RUNNING", verification.read_text(encoding="utf-8"))
            self.assertFalse((report_dir / "01_Source segments_report.json").exists())
            self.assertFalse(list((report_dir / ".pending").glob("*")))


if __name__ == "__main__":
    unittest.main()
