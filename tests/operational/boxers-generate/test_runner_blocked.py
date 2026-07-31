#!/usr/bin/env python3
"""Black-box regression test for the scenario 07 dependency preflight."""

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


class BlockedScenarioRunnerTest(unittest.TestCase):
    def test_scenario_07_blocks_without_invoking_curl(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            db_path = root / "assets.sqlite"
            curl_calls = root / "curl.calls"
            fake_bin = root / "bin"
            fake_bin.mkdir()
            fake_curl = fake_bin / "curl"
            fake_curl.write_text(
                "#!/usr/bin/env bash\n"
                f"printf 'curl-called\\n' >> {curl_calls!s}\n"
                "exit 99\n",
                encoding="utf-8",
            )
            fake_curl.chmod(0o755)

            with sqlite3.connect(db_path) as connection:
                connection.execute(
                    "CREATE TABLE media_assets ("
                    "id TEXT PRIMARY KEY, lifecycle_state TEXT NOT NULL, "
                    "source TEXT NOT NULL, drive_link TEXT NOT NULL, name TEXT NOT NULL)"
                )
                registry = json.loads(REGISTRY.read_text(encoding="utf-8"))
                for role, asset in registry["boxers"]["mike_tyson"]["assets"].items():
                    connection.execute(
                        "INSERT INTO media_assets VALUES (?, 'ACTIVE', 'youtube', ?, ?)",
                        (asset["asset_id"], asset["drive_link"], f"Mike Tyson {role}"),
                    )
                for role, asset in registry["boxers"]["muhammad_ali"]["assets"].items():
                    connection.execute(
                        "INSERT INTO media_assets VALUES (?, 'ACTIVE', 'youtube', ?, ?)",
                        (asset["asset_id"], asset["drive_link"], f"Muhammad Ali {role}"),
                    )
                for role, asset in registry["boxers"]["manny_pacquiao"]["assets"].items():
                    connection.execute(
                        "INSERT INTO media_assets VALUES (?, 'ACTIVE', 'youtube', ?, ?)",
                        (asset["asset_id"], asset["drive_link"], f"Manny Pacquiao {role}"),
                    )
                connection.commit()

            env = os.environ.copy()
            env.update({
                "VELOX_DB": str(db_path),
                "BOXERS_VOICEOVER_FOLDER_ID": "test-folder",
                "VELOX_ADMIN_TOKEN": "test-token",
                "TARGET_SCENARIO": "07",
                "PATH": f"{fake_bin}:{env['PATH']}",
            })
            result = subprocess.run(
                ["bash", str(RUNNER)],
                cwd=ROOT.parent.parent.parent,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('"status": "BLOCKED"', result.stdout)
            self.assertIn("no POST/job/voiceover was attempted", result.stdout)
            self.assertIn("no jobs or voiceovers were created", result.stdout)
            self.assertFalse(curl_calls.exists(), result.stdout)


if __name__ == "__main__":
    unittest.main()
