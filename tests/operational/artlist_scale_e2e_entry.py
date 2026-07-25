#!/usr/bin/env python3
"""Fail-closed entrypoint for the Artlist scale runner."""

from __future__ import annotations

from typing import Any

from artlist_scale_e2e import ScaleRunner, Settings


class FailClosedScaleRunner(ScaleRunner):
    """Stops downstream/quota work as soon as a phase is incomplete."""

    def run_phase(self, *args: Any, **kwargs: Any) -> list[dict[str, Any]]:
        phase = str(args[0] if args else kwargs.get("phase", "unknown"))
        failure_count_before = len(self.failures)
        results = super().run_phase(*args, **kwargs)
        new_failures = self.failures[failure_count_before:]
        if new_failures:
            raise RuntimeError(
                f"{phase} phase failed; downstream quota work aborted: "
                + "; ".join(new_failures)
            )
        return results

    def phase_items(self, *args: Any, **kwargs: Any) -> list[dict[str, Any]]:
        phase = str(args[0] if args else kwargs.get("phase", "unknown"))
        failure_count_before = len(self.failures)
        items = super().phase_items(*args, **kwargs)
        new_failures = self.failures[failure_count_before:]
        if new_failures:
            raise RuntimeError(
                f"{phase} item validation failed; downstream quota work aborted: "
                + "; ".join(new_failures)
            )
        return items

    def replay(
        self,
        target_ids: list[str],
        identity_before: dict[str, dict[str, str]],
    ) -> dict[str, Any]:
        if self.failures:
            report = {
                "enabled": self.s.run_replay,
                "aborted": True,
                "reason": "earlier validation failures",
            }
            self.write_json("replay/validation.json", report)
            return report
        return super().replay(target_ids, identity_before)


def main() -> int:
    try:
        settings = Settings.load()
    except Exception as exc:  # noqa: BLE001 - operational CLI boundary
        print(f"configuration error: {exc}")
        return 2
    return FailClosedScaleRunner(settings).execute()


if __name__ == "__main__":
    raise SystemExit(main())
