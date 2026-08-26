import importlib.util
from pathlib import Path
import unittest


MODULE_PATH = Path(__file__).with_name("verify-component-coverage.py")
SPEC = importlib.util.spec_from_file_location("verify_component_coverage", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {MODULE_PATH}")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class RegistryDerivedOverlapTests(unittest.TestCase):
    def test_overlap_is_allowed_when_current_registry_paths_overlap(self):
        registry = {
            "drive": {"paths": ["internal/platform/drive/"]},
            "storage": {"paths": ["internal/platform/drive/"]},
        }
        path = "internal/platform/drive/publisher.go"
        path_owners = MODULE.owners(path, registry)

        self.assertEqual(path_owners, ["drive", "storage"])
        self.assertTrue(MODULE.overlap_is_allowed(path, path_owners, registry))

    def test_overlap_does_not_accept_owner_not_declared_by_registry(self):
        registry = {
            "drive": {"paths": ["internal/platform/drive/"]},
            "storage": {"paths": ["internal/capabilities/assets/storage/"]},
        }
        path = "internal/platform/drive/publisher.go"

        self.assertFalse(MODULE.overlap_is_allowed(path, ["drive", "storage"], registry))

    def test_legacy_path_is_not_allowed_by_textual_prefix_rules(self):
        registry = {
            "script": {"paths": ["internal/capabilities/scripts/"]},
            "translation": {"paths": ["internal/capabilities/translation/"]},
        }
        path = "internal/application/scripts/adapters/processor.go"

        self.assertEqual(MODULE.owners(path, registry), [])
        self.assertFalse(MODULE.overlap_is_allowed(path, ["script", "translation"], registry))

    def test_unexpected_owner_is_rejected_when_only_one_registry_path_matches(self):
        registry = {
            "drive": {"paths": ["internal/platform/drive/"]},
            "storage": {"paths": ["internal/capabilities/assets/storage/"]},
        }
        path = "internal/platform/drive/publisher.go"

        self.assertEqual(MODULE.owners(path, registry), ["drive"])
        self.assertFalse(MODULE.overlap_is_allowed(path, ["drive", "storage"], registry))

    def test_fallback_domains_follow_the_current_internal_roots(self):
        self.assertEqual(MODULE.fallback_owner("internal/app/wiring.go")[0], "app")
        self.assertEqual(MODULE.fallback_owner("internal/kernel/asset/state.go")[0], "kernel")
        self.assertEqual(MODULE.fallback_owner("internal/capabilities/jobs/worker.go")[0], "capabilities")
        self.assertEqual(MODULE.fallback_owner("internal/platform/sqlite/store.go")[0], "platform")
        self.assertIsNone(MODULE.fallback_owner("internal/application/legacy.go"))


if __name__ == "__main__":
    unittest.main()
