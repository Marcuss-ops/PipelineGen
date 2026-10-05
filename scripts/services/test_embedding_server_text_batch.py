import asyncio
import importlib.util
import pathlib
import sys
import types

from fastapi import FastAPI
from fastapi.testclient import TestClient


ROOT = pathlib.Path(__file__).parent
PACKAGE_NAME = "scripts.services.embedding_server"


def load_text_module():
    previous_package = sys.modules.get(PACKAGE_NAME)
    package = types.ModuleType(PACKAGE_NAME)
    package.__path__ = [str(ROOT / "embedding_server")]
    package.TEXT_CONTRACT_HASH = "test-contract-hash"
    package.TEXT_CONTRACT_VERSION = "v1"
    package.TEXT_DOCUMENT_PREFIX = "passage: "
    package.TEXT_MODEL_NAME = "test-e5"
    package.TEXT_MODEL_VERSION = "test-revision"
    package.TEXT_QUERY_PREFIX = "query: "
    package.TEXT_SEMANTIC_DOCUMENT_VERSION = "v3"
    package._inference_sem = asyncio.Semaphore(2)
    package.model = None
    package.nlp = None
    sys.modules[PACKAGE_NAME] = package
    try:
        module_name = f"{PACKAGE_NAME}.text"
        spec = importlib.util.spec_from_file_location(
            module_name, ROOT / "embedding_server" / "text.py"
        )
        module = importlib.util.module_from_spec(spec)
        sys.modules[module_name] = module
        spec.loader.exec_module(module)
        return module
    finally:
        if previous_package is None:
            sys.modules.pop(PACKAGE_NAME, None)
        else:
            sys.modules[PACKAGE_NAME] = previous_package


text = load_text_module()


class FakeArray:
    def __init__(self, values):
        self.values = values

    def tolist(self):
        return self.values


class FakeEncoder:
    def __init__(self):
        self.calls = []
        self.error = None

    def encode(self, inputs, normalize_embeddings):
        self.calls.append((inputs, normalize_embeddings))
        if self.error:
            raise self.error
        if isinstance(inputs, str):
            return FakeArray([1.0, 2.0, 3.0])
        return FakeArray([[float(i), 2.0, 3.0] for i, _ in enumerate(inputs, 1)])

    def get_sentence_embedding_dimension(self):
        return 3


encoder = FakeEncoder()
text.model = encoder
app = FastAPI()
app.include_router(text.router)
client = TestClient(app)


def test_batch_embeddings_preserve_order_and_e5_contract():
    response = client.post(
        "/embed_batch",
        json={"texts": [" First  TEXT ", "Second sentence"], "type": "passage"},
    )
    assert response.status_code == 200
    body = response.json()
    assert body["count"] == 2
    assert body["dimensions"] == 3
    assert body["type"] == "passage"
    assert body["model"] == "test-e5"
    assert body["model_version"] == "test-revision"
    assert body["contract_hash"] == "test-contract-hash"
    assert body["embeddings"] == [[1.0, 2.0, 3.0], [2.0, 2.0, 3.0]]
    assert encoder.calls[-1] == (["passage: first text", "passage: second sentence"], True)


def test_query_batch_and_existing_single_endpoint_keep_their_contract():
    batch = client.post("/embed_batch", json={"texts": ["One", "Two"]})
    assert batch.status_code == 200
    assert encoder.calls[-1] == (["query: one", "query: two"], True)

    single = client.post("/embed", json={"text": "Single TEXT", "type": "query"})
    assert single.status_code == 200
    body = single.json()
    assert body["embedding"] == [1.0, 2.0, 3.0]
    assert body["dimensions"] == 3
    assert body["normalized_text"] == "single text"
    assert encoder.calls[-1] == ("query: single text", True)


def test_batch_rejects_empty_oversized_and_blank_texts():
    assert client.post("/embed_batch", json={"texts": []}).status_code == 422
    assert client.post("/embed_batch", json={"texts": ["x"] * 33}).status_code == 422
    assert client.post("/embed_batch", json={"texts": ["ok", "  "]}).status_code == 422
    assert client.post("/embed_batch", json={"texts": ["x" * 100_001]}).status_code == 422


def test_batch_fails_closed_on_encoder_error():
    encoder.error = RuntimeError("test inference failure")
    try:
        response = client.post("/embed_batch", json={"texts": ["first", "second"]})
    finally:
        encoder.error = None
    assert response.status_code == 500
    assert "test inference failure" in response.json()["detail"]


def test_batch_rejects_wrong_embedding_dimension():
    class WrongDimensionEncoder(FakeEncoder):
        def encode(self, inputs, normalize_embeddings):
            self.calls.append((inputs, normalize_embeddings))
            return FakeArray([[1.0, 2.0] for _ in inputs])

    original = text.model
    text.model = WrongDimensionEncoder()
    try:
        response = client.post("/embed_batch", json={"texts": ["first", "second"]})
    finally:
        text.model = original
    assert response.status_code == 500
    assert "dimension mismatch" in response.json()["detail"]


def test_batch_rejects_wrong_embedding_count():
    class WrongCountEncoder(FakeEncoder):
        def encode(self, inputs, normalize_embeddings):
            self.calls.append((inputs, normalize_embeddings))
            return FakeArray([[1.0, 2.0, 3.0]])

    original = text.model
    text.model = WrongCountEncoder()
    try:
        response = client.post("/embed_batch", json={"texts": ["first", "second"]})
    finally:
        text.model = original
    assert response.status_code == 500
    assert "count mismatch" in response.json()["detail"]
