import unittest
from unittest import mock

from scripts.services.device_policy import (
    assert_model_device,
    embedding_health_payload,
    model_device,
    reranker_health_payload,
    resolve_device,
)


class DevicePolicyTests(unittest.TestCase):
    def test_auto_selects_cuda_when_available(self):
        selection = resolve_device("auto", cuda_available=True)
        self.assertEqual(selection.effective, "cuda")
        self.assertTrue(selection.cuda_available)

    def test_auto_falls_back_to_cpu_when_gpu_not_required(self):
        selection = resolve_device("auto", cuda_available=False)
        self.assertEqual(selection.effective, "cpu")

    def test_explicit_cuda_fails_closed_when_unavailable(self):
        with self.assertRaisesRegex(RuntimeError, "explicitly requested"):
            resolve_device("cuda", cuda_available=False)

    def test_require_gpu_fails_closed_without_cuda(self):
        with self.assertRaisesRegex(RuntimeError, "GPU is required"):
            resolve_device("auto", require_gpu=True, cuda_available=False)

    def test_require_gpu_rejects_explicit_cpu(self):
        with self.assertRaisesRegex(RuntimeError, "explicit CPU"):
            resolve_device("cpu", require_gpu=True, cuda_available=True)

    def test_invalid_device_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "invalid device"):
            resolve_device("tpu", cuda_available=False)

    def test_model_device_and_gpu_assertion(self):
        model = mock.Mock(device="cuda:0")
        self.assertEqual(model_device(model), "cuda:0")
        selection = resolve_device("cuda", cuda_available=True)
        self.assertEqual(assert_model_device(model, selection, "test"), "cuda:0")

    def test_gpu_request_rejects_model_that_loaded_on_cpu(self):
        model = mock.Mock(device="cpu")
        selection = resolve_device("cuda", cuda_available=True)
        with self.assertRaisesRegex(RuntimeError, "running on cpu"):
            assert_model_device(model, selection, "test")

    def test_health_payloads_expose_effective_devices(self):
        selection = resolve_device("auto", cuda_available=True)
        embedding = embedding_health_payload(
            queue_depth=0,
            total_requests=4,
            inference_slots=2,
            text_device="cuda:0",
            visual_device="cuda:0",
            audio_device=None,
            selection=selection,
        )
        self.assertEqual(embedding["device"], "cuda")
        self.assertEqual(embedding["text_device"], "cuda:0")
        self.assertIsNone(embedding["audio_device"])
        self.assertFalse(embedding["gpu_required"])

        reranker = reranker_health_payload(
            model_name="test",
            model_device_name="cuda:0",
            selection=selection,
        )
        self.assertEqual(reranker["device"], "cuda")
        self.assertEqual(reranker["model_device"], "cuda:0")


if __name__ == "__main__":
    unittest.main()
