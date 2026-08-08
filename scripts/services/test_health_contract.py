import importlib
import importlib.util
import sys
import types
import unittest
from pathlib import Path
from unittest import mock

from scripts.services.device_policy import resolve_device


ROOT = Path(__file__).resolve().parents[2]


class HealthContractTests(unittest.TestCase):
    def test_embedding_health_source_exposes_all_effective_model_devices(self):
        source = (ROOT / "scripts/services/embedding_server/__init__.py").read_text()
        for field in ("text_device", "visual_device", "audio_device"):
            self.assertIn(field, source)
        self.assertIn("embedding_health_payload", source)

    def test_reranker_health_source_exposes_effective_model_device(self):
        source = (ROOT / "scripts/services/reranker_server.py").read_text()
        self.assertIn("MODEL_DEVICE", source)
        self.assertIn("reranker_health_payload", source)
        payload_source = (ROOT / "scripts/services/device_policy.py").read_text()
        for field in ("model_device", "requested_device", "gpu_required"):
            self.assertIn(field, payload_source)

    def test_gpu_required_selection_is_fail_closed_for_both_services(self):
        for requested in ("auto", "cuda"):
            with self.subTest(requested=requested):
                with self.assertRaises(RuntimeError):
                    resolve_device(requested, require_gpu=True, cuda_available=False)

    def test_health_routes_are_registered_in_both_sidecars(self):
        embedding_source = (ROOT / "scripts/services/embedding_server/__init__.py").read_text()
        reranker_source = (ROOT / "scripts/services/reranker_server.py").read_text()
        self.assertIn('@app.get("/health")', embedding_source)
        self.assertIn('@app.get("/health")', reranker_source)

    def test_fastapi_testclient_calls_both_health_endpoints(self):
        """Call the real sidecar routes with heavyweight model imports mocked."""
        if importlib.util.find_spec("fastapi") is None:
            self.skipTest("FastAPI is not installed in this environment")

        from fastapi.testclient import TestClient

        class FakeSentenceTransformer:
            def __init__(self, *_args, device="cpu", **_kwargs):
                self.device = device

            def get_sentence_embedding_dimension(self):
                return 2

        fake_sentence_transformers = types.ModuleType("sentence_transformers")
        fake_sentence_transformers.SentenceTransformer = FakeSentenceTransformer
        fake_sentence_transformers.CrossEncoder = FakeSentenceTransformer
        fake_spacy = types.ModuleType("spacy")
        fake_spacy.load = lambda *_args, **_kwargs: mock.Mock()
        fake_imagehash = types.ModuleType("imagehash")

        with mock.patch.dict(
            sys.modules,
            {
                "sentence_transformers": fake_sentence_transformers,
                "spacy": fake_spacy,
                "imagehash": fake_imagehash,
            },
        ), mock.patch.dict(
            __import__("os").environ,
            {
                "PIPELINEGEN_EMBEDDING_DEVICE": "cpu",
                "PIPELINEGEN_EMBEDDING_REQUIRE_GPU": "0",
                "SKIP_SIGLIP": "1",
                "SKIP_CLAP": "1",
            },
            clear=False,
        ):
            for name in ("scripts.services.embedding_server", "scripts.services.reranker_server"):
                sys.modules.pop(name, None)
            embedding = importlib.import_module("scripts.services.embedding_server")
            reranker = importlib.import_module("scripts.services.reranker_server")
            self.assertEqual(TestClient(embedding.app).get("/health").status_code, 200)
            embedding_health = TestClient(embedding.app).get("/health").json()
            self.assertEqual(embedding_health["device"], "cpu")
            self.assertEqual(embedding_health["text_device"], "cpu")
            self.assertIn("visual_device", embedding_health)
            self.assertEqual(TestClient(reranker.app).get("/health").status_code, 200)
            reranker_health = TestClient(reranker.app).get("/health").json()
            self.assertEqual(reranker_health["device"], "cpu")
            self.assertEqual(reranker_health["model_device"], "cpu")


if __name__ == "__main__":
    unittest.main()
