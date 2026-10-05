import asyncio
import importlib.util
import pathlib
import sys
import types

from fastapi import FastAPI
from fastapi.testclient import TestClient


def load_ner_module():
    package_name = "scripts.services.embedding_server"
    module_name = f"{package_name}.ner"
    previous_package = sys.modules.get(package_name)
    package = types.ModuleType(package_name)
    package.__path__ = [str(pathlib.Path(__file__).parent / "embedding_server")]
    package._inference_sem = asyncio.Semaphore(2)
    sys.modules[package_name] = package
    try:
        spec = importlib.util.spec_from_file_location(module_name, pathlib.Path(__file__).parent / "embedding_server" / "ner.py")
        module = importlib.util.module_from_spec(spec)
        sys.modules[module_name] = module
        spec.loader.exec_module(module)
        return module
    finally:
        if previous_package is None:
            sys.modules.pop(package_name, None)
        else:
            sys.modules[package_name] = previous_package


ner = load_ner_module()


class FakeEntity:
    def __init__(self, text, label, start, end):
        self.text = text
        self.label_ = label
        self.start_char = start
        self.end_char = end


class FakeDoc:
    ents = [FakeEntity("Elon Musk", "PER", 0, 9), FakeEntity("Tesla", "ORG", 20, 25)]


def make_client():
    app = FastAPI()
    app.include_router(ner.router)
    return TestClient(app)


def test_extract_caps_entities_and_reports_timing(monkeypatch):
    monkeypatch.setattr(ner, "_load_ner_model", lambda: lambda text: FakeDoc())
    response = make_client().post("/ner/extract", json={
        "source_text": "Elon Musk discussed Tesla.", "language": "en-US", "entity_count": 1,
    })
    assert response.status_code == 200
    body = response.json()
    assert body["entities"] == [{"text": "Elon Musk", "label": "PER", "start_char": 0, "end_char": 9}]
    assert body["model_load_ms"] == 0.0
    assert body["inference_ms"] >= 0


def test_zero_entity_count_uses_shared_top_three_default(monkeypatch):
    class FourEntityDoc:
        ents = [
            FakeEntity("A", "ORG", 0, 1),
            FakeEntity("B", "ORG", 2, 3),
            FakeEntity("C", "ORG", 4, 5),
            FakeEntity("D", "ORG", 6, 7),
        ]

    monkeypatch.setattr(ner, "_load_ner_model", lambda: lambda text: FourEntityDoc())
    response = make_client().post("/ner/extract", json={
        "source_text": "A B C D", "language": "en", "entity_count": 0,
    })
    assert response.status_code == 200
    assert [entity["text"] for entity in response.json()["entities"]] == ["A", "B", "C"]


def test_extract_preserves_python_unicode_codepoint_offsets(monkeypatch):
    class UnicodeDoc:
        ents = [FakeEntity("Ada Lovelace", "PER", 2, 14)]

    monkeypatch.setattr(ner, "_load_ner_model", lambda: lambda text: UnicodeDoc())
    source = "😀 Ada Lovelace spoke."
    response = make_client().post("/ner/extract", json={"source_text": source, "language": "en"})
    assert response.status_code == 200
    entity = response.json()["entities"][0]
    assert source[entity["start_char"]:entity["end_char"]] == entity["text"] == "Ada Lovelace"
    assert entity["start_char"] == 2


def test_extract_rejects_unavailable_languages_and_bad_protocol():
    client = make_client()
    assert client.post("/ner/extract", json={"source_text": "Tesla", "language": "ja"}).status_code == 400
    assert client.post("/ner/extract", json={
        "version": "ner.v2", "source_text": "Tesla", "language": "en",
    }).status_code == 400


def test_request_bounds_and_inference_errors_fail_closed(monkeypatch):
    client = make_client()
    assert client.post("/ner/extract", json={"source_text": "  ", "language": "en"}).status_code == 400
    assert client.post("/ner/extract", json={
        "source_text": "x" * 100_001, "language": "en",
    }).status_code == 422
    monkeypatch.setattr(ner, "_load_ner_model", lambda: lambda text: (_ for _ in ()).throw(ValueError("bad text")))
    response = client.post("/ner/extract", json={"source_text": "Tesla", "language": "en"})
    assert response.status_code == 503
    assert response.json()["detail"] == "spaCy NER inference failed"


def test_missing_model_fails_closed(monkeypatch):
    monkeypatch.setattr(ner, "_model", None)
    monkeypatch.setattr(ner, "_model_name", "")
    monkeypatch.setattr(ner, "spacy", type("FakeSpacy", (), {
        "load": staticmethod(lambda name: (_ for _ in ()).throw(OSError("model missing")))
    }))
    try:
        ner._load_ner_model()
    except RuntimeError as exc:
        assert "unavailable" in str(exc)
    else:
        raise AssertionError("missing model must not silently fall back")
